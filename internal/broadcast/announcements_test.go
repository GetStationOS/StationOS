package broadcast

import (
	"bytes"
	"context"
	"errors"
	"stationos.local/stationos/internal/announcements"
	"stationos.local/stationos/internal/changefeed"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
	"sync"
	"testing"
	"time"
)

type announcementMemory struct {
	announcements.Repository
	mu       sync.Mutex
	message  announcements.Message
	receipts map[string]announcements.Receipt
	bus      *changefeed.Bus
}

func (r *announcementMemory) List(context.Context, string) ([]announcements.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []announcements.Message{r.message}, nil
}
func (r *announcementMemory) Settings(context.Context, string) (announcements.Settings, error) {
	return announcements.Settings{DJID: "presenter", GapMinutes: 10}, nil
}
func (r *announcementMemory) LastAired(context.Context, string) (time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.message.LastAired, nil
}
func (r *announcementMemory) Record(_ context.Context, a announcements.Receipt) error {
	r.mu.Lock()
	r.receipts[a.ID] = a
	r.message.LastAired = a.Started
	r.message.Next = false
	r.mu.Unlock()
	r.bus.Publish("announcements")
	return nil
}

type announcementDJ struct{ djs.Repository }

func (announcementDJ) Get(context.Context, string) (djs.Profile, error) {
	return djs.Profile{ID: "presenter", Name: "Presenter"}, nil
}

type announcementSpeech struct {
	called  chan announcements.Message
	release <-chan struct{}
	err     error
}

func (p announcementSpeech) Announcement(ctx context.Context, a announcements.Message, dj djs.Profile) ([]byte, error) {
	select {
	case p.called <- a:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	production.ReportSpeechTranscript(ctx, a.Text)
	return []byte("ANNOUNCEMENT"), nil
}
func announcementFixture(t *testing.T, p announcementSpeech) (*Manager, *fakeEncoder, *announcementMemory) {
	t.Helper()
	now := time.Now()
	bus := &changefeed.Bus{}
	repo := &announcementMemory{message: announcements.Message{ID: "notice", StationID: "s", Version: 1, Title: "News", Text: "A producer message", Mode: "exact", Status: "active", Repeat: "once", Starts: now.Add(-time.Hour), Expires: now.Add(time.Hour)}, receipts: map[string]announcements.Receipt{}, bus: bus}
	m, e, _ := fixture(t, Options{Announcements: repo, AnnouncementProducer: p, Changes: bus, DJs: announcementDJ{}})
	return m, e, repo
}
func TestAnnouncementSharedBroadcastAndActualReceipt(t *testing.T) {
	calls := make(chan announcements.Message, 8)
	m, e, repo := announcementFixture(t, announcementSpeech{called: calls})
	store := &recordingStore{events: map[string]operations.Event{}}
	rec, err := operations.New(context.Background(), store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()
	m.options.Operations = rec
	if m.State("s").Status != "Dormant" {
		t.Fatal("state started broadcast")
	}
	select {
	case <-calls:
		t.Fatal("dormant synthesis")
	default:
	}
	one, err := m.Subscribe(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := m.Subscribe(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	current := opened(t, e)
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("not prepared")
	}
	waitFor(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.runs["s"] != nil })
	// Preparation does not record an aired receipt.
	repo.mu.Lock()
	if len(repo.receipts) != 0 {
		t.Fatal("preparation counted as airing")
	}
	repo.mu.Unlock()
	// Wait until the single worker has published readiness before crossing the boundary.
	// The public current track remains music while preparation finishes.
	waitFor(t, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		for _, e := range store.events {
			if e.Phase == "announcement-preparation" {
				return true
			}
		}
		return false
	})
	go func() { current.write.Write([]byte("MUSIC")); current.write.Close() }()
	for _, sub := range []*Subscription{one, two} {
		if got := receive(t, sub); !bytes.Equal(got, []byte("MUSIC")) {
			t.Fatal(string(got))
		}
	}
	_ = opened(t, e)
	for _, sub := range []*Subscription{one, two} {
		if got := receive(t, sub); !bytes.Equal(got, []byte("ANNOUNCEMENT")) {
			t.Fatal(string(got))
		}
	}
	waitFor(t, func() bool {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		for _, a := range repo.receipts {
			if a.Outcome == "completed" && a.Version == 1 && a.Text == "A producer message" {
				return true
			}
		}
		return false
	})
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.receipts) != 1 {
		t.Fatal("listeners produced separate receipts", repo.receipts)
	}
}
func TestEditedOrExpiredAnnouncementCannotUsePreparedAudio(t *testing.T) {
	calls := make(chan announcements.Message, 8)
	m, _, repo := announcementFixture(t, announcementSpeech{called: calls})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := m.startAnnouncements(ctx, "s")
	defer func() { cancel(); <-w.done }()
	w.target(schedule.Occurrence{StationID: "s"}, time.Now())
	<-calls
	waitFor(t, func() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.ready != nil })
	ready := w.take()
	if !m.announcementValid(ctx, "s", ready, schedule.Occurrence{}, time.Now()) {
		t.Fatal("valid prepared message rejected")
	}
	repo.mu.Lock()
	repo.message.Version++
	repo.message.Text = "Edited"
	repo.mu.Unlock()
	if m.announcementValid(ctx, "s", ready, schedule.Occurrence{}, time.Now()) {
		t.Fatal("stale version played")
	}
	repo.mu.Lock()
	repo.message.Version--
	repo.message.Expires = time.Now().Add(-time.Second)
	repo.mu.Unlock()
	if m.announcementValid(ctx, "s", ready, schedule.Occurrence{}, time.Now()) {
		t.Fatal("expired message played")
	}
}
func TestAnnouncementFailureKeepsMusicAndNoReceipt(t *testing.T) {
	calls := make(chan announcements.Message, 8)
	m, e, repo := announcementFixture(t, announcementSpeech{called: calls, err: errors.New("offline")})
	sub, err := m.Subscribe(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	current := opened(t, e)
	<-calls
	go func() { current.write.Write([]byte("MUSIC")); current.write.Close() }()
	if got := receive(t, sub); string(got) != "MUSIC" {
		t.Fatal(string(got))
	}
	_ = opened(t, e)
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.receipts) != 0 {
		t.Fatal("failed preparation counted as airtime")
	}
}

func TestAnnouncementReservesTimeForCommittedMusic(t *testing.T) {
	now := time.Now()
	a := &preparedAnnouncement{Audio: make([]byte, 16000*30)}
	song := time.Minute
	next := content.AudioItem{Duration: &song}
	show := schedule.Occurrence{End: now.Add(90 * time.Second)}
	if announcementFits(a, show, next, now) {
		t.Fatal("announcement overran programme with committed music")
	}
	show.End = now.Add(92 * time.Second)
	if !announcementFits(a, show, next, now) {
		t.Fatal("fitting announcement rejected")
	}
	show.End = time.Time{}
	if !announcementFits(a, show, next, now) {
		t.Fatal("continuous music rejected")
	}
}
