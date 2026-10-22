package broadcast

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"stationos.local/stationos/internal/content"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/schedule"
)

type queueStore struct {
	sync.Mutex
	values map[string]episodes.Episode
}

func (s *queueStore) Get(_ context.Context, key string) (episodes.Episode, error) {
	s.Lock()
	defer s.Unlock()
	e, ok := s.values[episodes.SessionKey(key)]
	if !ok {
		return e, episodes.ErrNotFound
	}
	raw, _ := json.Marshal(e)
	json.Unmarshal(raw, &e)
	return e, nil
}
func (s *queueStore) Update(_ context.Context, o schedule.Occurrence, fn func(*episodes.Episode) error) error {
	s.Lock()
	defer s.Unlock()
	if s.values == nil {
		s.values = map[string]episodes.Episode{}
	}
	key := episodes.SessionKey(o.Key)
	e, ok := s.values[key]
	if !ok {
		e = episodes.New(o)
	}
	if err := fn(&e); err != nil {
		return err
	}
	e.Version++
	s.values[key] = e
	return nil
}

type queueText struct {
	entered chan struct{}
	release chan struct{}
}

func (q queueText) Identity(context.Context, string) (string, error) { return "test", nil }
func (q queueText) Kind(context.Context, string) (string, error)     { return "compatible", nil }
func (q queueText) Generate(ctx context.Context, r ai.TextRequest) (string, error) {
	if q.entered != nil {
		select {
		case q.entered <- struct{}{}:
		default:
		}
		select {
		case <-q.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	var input struct {
		History []struct{ Tracks []episodes.Track } `json:"session_tool_history"`
	}
	if err := json.Unmarshal([]byte(r.Input), &input); err != nil {
		return "", err
	}
	for _, h := range input.History {
		if len(h.Tracks) > 0 {
			return fmt.Sprintf(`{"Action":"plan","Tracks":[%q]}`, h.Tracks[0].ID), nil
		}
	}
	return "", fmt.Errorf("no offered tracks: %s", r.Input)
}

func TestAcceptedQueueSurvivesSlowPlanningAndAdvances(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	source := timedSchedule{shows: []schedule.Show{{ID: "show", StationID: "radio", Day: int(now.Weekday()+6) % 7, Start: 0, End: 0, PlaylistID: "pool", DJ: djs.Profile{ID: "dj", TextReady: true}}}}
	service := &episodes.Service{Store: &queueStore{}, Text: queueText{}}
	m, encoder, repo := fixture(t, Options{Schedule: source, Selector: service, Session: service, Now: func() time.Time { return now }})
	repo.pool = nil
	for i := 0; i < 16; i++ {
		item := length(fmt.Sprint(i), 180)
		item.Artist = "Artist " + item.ID
		item.Album = "Album " + item.ID
		repo.pool = append(repo.pool, item)
	}
	service.Pool = repo
	service.Eligibility = m
	timeline, err := source.Resolve(ctx, "radio", now)
	if err != nil {
		t.Fatal(err)
	}
	o := timeline.Current
	if err := service.Begin(ctx, o); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := service.Plan(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	sub, err := m.Subscribe(ctx, "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	current := opened(t, encoder)
	for turn := 0; turn < 5; turn++ {
		current.write.Write([]byte("MUSIC"))
		receive(t, sub)
		// Audio delivery precedes its persisted airtime receipt. Planning from
		// the old receipt is correctly rejected as stale, so wait for the
		// current track's receipt before testing uninterrupted queue refill.
		waitFor(t, func() bool {
			episode, err := service.Store.Get(ctx, o.Key)
			if err != nil {
				return false
			}
			for _, aired := range episode.Aired {
				if aired.ID == current.item.ID {
					return true
				}
			}
			return false
		})
		state := m.State("radio")
		promised := state.Next.ID
		if promised == "" {
			t.Fatal("music published without next", state)
		}
		if turn == 0 {
			blocked := queueText{entered: make(chan struct{}, 1), release: make(chan struct{})}
			service.Text = blocked
			done := make(chan error, 1)
			go func() { done <- service.Plan(ctx, o) }()
			select {
			case <-blocked.entered:
			case <-time.After(time.Second):
				t.Fatal("background planning did not begin")
			}
			for i := 0; i < 3; i++ {
				current.write.Write([]byte("MUSIC"))
				receive(t, sub)
				s := m.State("radio")
				if s.Next.ID != promised || s.Current.ID != current.item.ID {
					t.Fatal("published music changed while planning", s)
				}
			}
			close(blocked.release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			service.Text = queueText{}
		} else {
			if err := service.Plan(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		saved, err := service.Store.Get(ctx, o.Key)
		if err != nil {
			t.Fatal(err)
		}
		if len(saved.Upcoming()) < 2 {
			t.Fatalf("ready buffer not replenished: %+v", saved)
		}
		for _, track := range saved.Upcoming() {
			if !track.Approved {
				t.Fatal("unaccepted track in ready queue", track)
			}
		}
		current.write.Close()
		current = opened(t, encoder)
		if current.item.ID != promised {
			t.Fatalf("committed next %s replaced by %s", promised, current.item.ID)
		}
	}
}

type boundaryPools struct{ available []content.AudioItem }

func (p boundaryPools) Assignment(context.Context, string) (string, error) { return "", nil }
func (p boundaryPools) Assign(context.Context, string, string) error       { return nil }
func (p boundaryPools) Pool(_ context.Context, id string) ([]content.AudioItem, error) {
	if id == "good" {
		return p.available, nil
	}
	return nil, nil
}
func TestEmptyShowPlaylistKeepsPromisedAudioAtBoundary(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 59, 0, 0, time.UTC)
	var clock atomic.Int64
	source := timedSchedule{shows: []schedule.Show{{ID: "before", StationID: "radio", Day: 1, Start: 540, End: 600, PlaylistID: "good"}, {ID: "after", StationID: "radio", Day: 1, Start: 600, End: 660, PlaylistID: "empty"}}}
	encoder := &fakeEncoder{calls: make(chan opening, 16)}
	repo := boundaryPools{available: []content.AudioItem{length("a", 180), length("b", 180), length("c", 180)}}
	m := New(repo, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Schedule: source, Now: func() time.Time { return now.Add(time.Duration(clock.Load()) * time.Second) }})
	defer m.Close()
	sub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first := opened(t, encoder)
	first.write.Write([]byte("AUDIO"))
	receive(t, sub)
	promised := m.State("radio").Next.ID
	if promised == "" {
		t.Fatal("no next before boundary")
	}
	clock.Store(181)
	first.write.Close()
	next := opened(t, encoder)
	if next.item.ID != promised {
		t.Fatal("fallback replaced promised next", next.item, promised)
	}
	next.write.Write([]byte("AUDIO"))
	receive(t, sub)
	s := m.State("radio")
	if s.Status != "Broadcasting" || s.Next.ID == "" || s.Programme.ShowID != "" || s.Programme.PlaylistID != "good" {
		t.Fatal("empty scheduled playlist stopped production or mislabelled fallback", s)
	}
}
