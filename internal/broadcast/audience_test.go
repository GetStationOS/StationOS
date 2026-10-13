package broadcast

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

type memoryAudience struct {
	audience.Repository
	mu       sync.Mutex
	messages []audience.Message
	reviewed chan struct{}
	used     chan string
}

func (r *memoryAudience) ForShow(_ context.Context, station, key string, status audience.Status, _ time.Time, _ int) ([]audience.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []audience.Message
	for _, m := range r.messages {
		if m.StationID == station && m.OccurrenceKey == key && m.Status == status {
			out = append(out, m)
		}
	}
	return out, nil
}
func (r *memoryAudience) Review(_ context.Context, _ string, decisions []audience.Decision, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range decisions {
		for i := range r.messages {
			if r.messages[i].ID == d.ID && r.messages[i].Status == audience.Pending {
				r.messages[i].Status = audience.Rejected
				if d.Approve {
					r.messages[i].Status = audience.Approved
				}
			}
		}
	}
	if r.reviewed != nil {
		close(r.reviewed)
		r.reviewed = nil
	}
	return nil
}
func (r *memoryAudience) Usable(_ context.Context, station, key string, ids []string, _ time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		ok := false
		for _, m := range r.messages {
			if m.ID == id && m.StationID == station && m.OccurrenceKey == key && m.Status == audience.Approved {
				ok = true
			}
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}
func (r *memoryAudience) MarkUsed(_ context.Context, _ string, ids []string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		for i := range r.messages {
			if r.messages[i].ID == id && r.messages[i].Status == audience.Approved {
				r.messages[i].Status = audience.Used
				r.used <- id
			}
		}
	}
	return nil
}

type audienceLinks struct {
	called      chan production.LinkRequest
	omitRequest bool
}

func (p audienceLinks) Review(_ context.Context, _ schedule.Occurrence, items []production.AudienceItem) ([]audience.Decision, error) {
	var out []audience.Decision
	for _, item := range items {
		out = append(out, audience.Decision{ID: item.ID, Approve: true})
	}
	return out, nil
}
func (p audienceLinks) Link(context.Context, production.LinkRequest) ([]byte, error) { return nil, nil }
func (p audienceLinks) AudienceLink(_ context.Context, req production.LinkRequest) ([]byte, []string, error) {
	p.called <- req
	if len(req.Audience) > 0 {
		if req.Audience[0].Kind == audience.Request {
			if p.omitRequest {
				return []byte("NO INTRODUCTION"), nil, nil
			}
			return []byte("REQUEST"), []string{req.Audience[0].ID}, nil
		}
		return []byte("SHOUT"), []string{req.Audience[0].ID}, nil
	}
	return nil, nil, nil
}

type audiencePools struct{}

type sectionAudienceLinks struct {
	audienceLinks
	ids []string
}

func (p sectionAudienceLinks) AudienceLink(_ context.Context, req production.LinkRequest) ([]byte, []string, error) {
	p.called <- req
	return []byte("LISTENER SHOUTOUTS"), p.ids, nil
}

func TestShoutoutSectionMarksOnlySpokenMessagesUsed(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	show := schedule.Show{ID: "show", StationID: "radio", Name: "Breakfast", Day: 0, Start: 540, End: 600, PlaylistID: "p", AcceptShoutouts: true, DJ: djs.Profile{ID: "dj", HasKey: true}}
	occurrence := schedule.Resolve([]schedule.Show{show}, "radio", "p", "Music", now, time.UTC).Current
	repo := &memoryAudience{used: make(chan string, 4)}
	for _, id := range []string{"a", "b", "c", "d"} {
		repo.messages = append(repo.messages, audience.Message{ID: id, StationID: "radio", OccurrenceKey: occurrence.Key, Kind: audience.Shoutout, Status: audience.Approved, Text: "Hello family"})
	}
	links := sectionAudienceLinks{audienceLinks: audienceLinks{called: make(chan production.LinkRequest, 4)}, ids: []string{"a", "c"}}
	encoder := &fakeEncoder{calls: make(chan opening, 16)}
	m := New(audiencePools{}, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Schedule: timedSchedule{shows: []schedule.Show{show}}, Audience: repo, Links: links, Now: func() time.Time { return now }})
	defer m.Close()
	sub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first := opened(t, encoder)
	select {
	case req := <-links.called:
		if len(req.Audience) != 3 || req.Audience[0].ID != "a" || req.Audience[2].ID != "c" {
			t.Fatal("incorrect bounded shoutout section", req)
		}
	case <-time.After(time.Second):
		t.Fatal("no shoutout preparation")
	}
	select {
	case id := <-repo.used:
		t.Fatal("preparation consumed message", id)
	default:
	}
	time.Sleep(50 * time.Millisecond)
	first.write.Write([]byte("MUSIC"))
	if string(receive(t, sub)) != "MUSIC" {
		t.Fatal("music missing")
	}
	first.write.Close()
	if string(receive(t, sub)) != "LISTENER SHOUTOUTS" {
		t.Fatal("listener section missing")
	}
	for _, want := range []string{"a", "c"} {
		select {
		case got := <-repo.used:
			if got != want {
				t.Fatal(got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("missing speech receipt", want)
		}
	}
	remaining, err := repo.ForShow(context.Background(), "radio", occurrence.Key, audience.Approved, now, 10)
	if err != nil || len(remaining) != 2 || remaining[0].ID != "b" || remaining[1].ID != "d" {
		t.Fatal("unspoken messages lost approval", remaining, err)
	}
}

type failFirstAudienceLink struct {
	audienceLinks
	failed bool
}

func (p *failFirstAudienceLink) AudienceLink(ctx context.Context, req production.LinkRequest) ([]byte, []string, error) {
	if !p.failed {
		p.failed = true
		p.called <- req
		return nil, nil, errors.New("OpenAI responses timed out")
	}
	return p.audienceLinks.AudienceLink(ctx, req)
}

func TestFailedShoutoutIsOfferedAgainDuringNextTrackAndSpoken(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	show := schedule.Show{ID: "show", StationID: "radio", Name: "Breakfast", Day: 0, Start: 540, End: 600, PlaylistID: "p", AcceptShoutouts: true, DJ: djs.Profile{ID: "dj", HasKey: true}}
	occurrence := schedule.Resolve([]schedule.Show{show}, "radio", "p", "Music", now, time.UTC).Current
	repo := &memoryAudience{messages: []audience.Message{{ID: "shout", StationID: "radio", OccurrenceKey: occurrence.Key, Kind: audience.Shoutout, Status: audience.Pending, Text: "Hello Sam"}}, used: make(chan string, 4)}
	links := &failFirstAudienceLink{audienceLinks: audienceLinks{called: make(chan production.LinkRequest, 4)}}
	encoder := &fakeEncoder{calls: make(chan opening, 16)}
	m := New(audiencePools{}, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Schedule: timedSchedule{shows: []schedule.Show{show}}, Audience: repo, Links: links, Now: func() time.Time { return now }})
	defer m.Close()
	sub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	for attempt := 0; attempt < 2; attempt++ {
		music := opened(t, encoder)
		select {
		case req := <-links.called:
			if len(req.Audience) != 1 || req.Audience[0].ID != "shout" {
				t.Fatal("shoutout was not offered again", req)
			}
		case <-time.After(time.Second):
			t.Fatal("missing preparation attempt", attempt)
		}
		approved, err := repo.ForShow(context.Background(), "radio", occurrence.Key, audience.Approved, now, 10)
		if err != nil || len(approved) != 1 {
			t.Fatal("unplayed shoutout lost approval", approved, err)
		}
		// Let the worker publish its result before ending this controlled track.
		time.Sleep(50 * time.Millisecond)
		music.write.Write([]byte("MUSIC"))
		if string(receive(t, sub)) != "MUSIC" {
			t.Fatal("normal music interrupted")
		}
		music.write.Close()
	}
	if string(receive(t, sub)) != "SHOUT" {
		t.Fatal("retried shoutout was not spoken")
	}
	select {
	case id := <-repo.used:
		if id != "shout" {
			t.Fatal(id)
		}
	case <-time.After(time.Second):
		t.Fatal("missing spoken receipt")
	}
}

func (audiencePools) Assignment(context.Context, string) (string, error) { return "p", nil }
func (audiencePools) Assign(context.Context, string, string) error       { return nil }
func (audiencePools) Pool(context.Context, string) ([]content.AudioItem, error) {
	return []content.AudioItem{length("a", 60), length("b", 60)}, nil
}
func TestAudienceReviewAndReceiptFollowActualSharedBroadcast(t *testing.T) {
	for _, dismiss := range []bool{false, true} {
		t.Run(map[bool]string{false: "broadcast", true: "dismiss prepared speech"}[dismiss], func(t *testing.T) {
			now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
			show := schedule.Show{ID: "show", StationID: "radio", Name: "Breakfast", Day: 0, Start: 540, End: 600, PlaylistID: "p", AcceptRequests: true, AcceptShoutouts: true, DJ: djs.Profile{ID: "dj", HasKey: true}}
			occurrence := schedule.Resolve([]schedule.Show{show}, "radio", "p", "Music", now, time.UTC).Current
			repo := &memoryAudience{messages: []audience.Message{{ID: "shout", StationID: "radio", OccurrenceKey: occurrence.Key, Kind: audience.Shoutout, Status: audience.Pending, Text: "Hello Sam"}}, used: make(chan string, 4)}
			links := audienceLinks{called: make(chan production.LinkRequest, 4)}
			encoder := &fakeEncoder{calls: make(chan opening, 16)}
			m := New(audiencePools{}, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Schedule: timedSchedule{shows: []schedule.Show{show}}, Audience: repo, Links: links, Now: func() time.Time { return now }})
			defer m.Close()
			sub, err := m.Subscribe(context.Background(), "radio")
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			first := opened(t, encoder)
			if first.item.ID == "" {
				t.Fatal("music missing")
			}
			select {
			case req := <-links.called:
				if len(req.Audience) != 1 || req.Audience[0].ID != "shout" {
					t.Fatal(req)
				}
			case <-time.After(time.Second):
				t.Fatal("AI review did not run")
			}
			select {
			case id := <-repo.used:
				t.Fatal("preparation consumed", id)
			default:
			}
			// Give the bounded speech preparation worker time to deliver its ready result.
			time.Sleep(50 * time.Millisecond)
			if dismiss {
				repo.mu.Lock()
				repo.messages[0].Status = audience.Rejected
				repo.mu.Unlock()
			}
			first.write.Write([]byte("MUSIC"))
			if string(receive(t, sub)) != "MUSIC" {
				t.Fatal("music missing")
			}
			first.write.Close()
			if !dismiss {
				if string(receive(t, sub)) != "SHOUT" {
					t.Fatal("approved shout missing")
				}
				select {
				case id := <-repo.used:
					if id != "shout" {
						t.Fatal(id)
					}
				case <-time.After(time.Second):
					t.Fatal("speech receipt missing")
				}
			} else {
				next := opened(t, encoder)
				next.write.Write([]byte("NEXT"))
				if string(receive(t, sub)) != "NEXT" {
					t.Fatal("dismissed speech broadcast")
				}
				next.write.Close()
				select {
				case id := <-repo.used:
					t.Fatal("dismissed consumed", id)
				default:
				}
			}
		})
	}
}
func TestRequestedTrackMustFitRevalidatedCandidates(t *testing.T) {
	messages := []audience.Message{{Kind: audience.Request, TrackID: "outside"}, {Kind: audience.Request, TrackID: "long"}, {Kind: audience.Request, TrackID: "fit"}}
	pool := []content.AudioItem{length("long", 300), length("fit", 120)}
	if got := requestTrack(messages, pool, 2*time.Minute); got.ID != "fit" {
		t.Fatal(got)
	}
	if got := requestTrack(messages, pool, -time.Second); got.ID != "" {
		t.Fatal("request overrun", got)
	}
	filtered, _ := rotationPool(pool, Rotation{Interval: time.Hour, LastPlayed: map[string]time.Time{"fit": time.Now()}}, time.Now(), "fit")
	if got := requestTrack(messages, filtered, 2*time.Minute); got.ID != "" {
		t.Fatal("rotation bypass", got)
	}
}

func TestRequestIntroductionPrecedesItsExactRecordingAndReceipt(t *testing.T) {
	for _, mode := range []string{"broadcast", "dismiss", "omit"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
			show := schedule.Show{ID: "show", StationID: "radio", Name: "Breakfast", Day: 0, Start: 540, End: 600, PlaylistID: "p", AcceptRequests: true, AcceptShoutouts: true, DJ: djs.Profile{ID: "dj", HasKey: true}}
			occurrence := schedule.Resolve([]schedule.Show{show}, "radio", "p", "Music", now, time.UTC).Current
			repo := &memoryAudience{messages: []audience.Message{{ID: "shout", StationID: "radio", OccurrenceKey: occurrence.Key, Kind: audience.Shoutout, Status: audience.Approved, Text: "Hello"}, {ID: "request", StationID: "radio", OccurrenceKey: occurrence.Key, Kind: audience.Request, Status: audience.Approved, Name: "Sam from Birmingham", TrackID: "a"}}, used: make(chan string, 4)}
			history := &memoryHistory{played: map[string]map[string]time.Time{"radio": {"a": now.Add(-4 * time.Hour)}}}
			links := audienceLinks{called: make(chan production.LinkRequest, 8), omitRequest: mode == "omit"}
			encoder := &fakeEncoder{calls: make(chan opening, 16)}
			m := New(audiencePools{}, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{History: history, Schedule: timedSchedule{shows: []schedule.Show{show}}, Audience: repo, Links: links, Now: func() time.Time { return now }})
			defer m.Close()
			sub, err := m.Subscribe(context.Background(), "radio")
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			first := opened(t, encoder)
			if first.item.ID != "b" {
				t.Fatal("cold start should continue normal music", first.item)
			}
			select {
			case req := <-links.called:
				if req.Next.ID != "a" || len(req.Audience) != 1 || req.Audience[0].ID != "request" || req.Audience[0].Name != "Sam from Birmingham" {
					t.Fatal("exact queued request not passed to DJ", req)
				}
			case <-time.After(time.Second):
				t.Fatal("request prompt missing")
			}
			time.Sleep(50 * time.Millisecond)
			if mode == "dismiss" {
				repo.mu.Lock()
				repo.messages[1].Status = audience.Rejected
				repo.mu.Unlock()
			}
			first.write.Write([]byte("NORMAL"))
			if string(receive(t, sub)) != "NORMAL" {
				t.Fatal("normal music missing")
			}
			first.write.Close()
			requested := opened(t, encoder)
			if requested.item.ID != "a" {
				t.Fatal("wrong recording", requested.item)
			}
			if mode == "broadcast" {
				if string(receive(t, sub)) != "REQUEST" {
					t.Fatal("requester introduction missing")
				}
				select {
				case id := <-repo.used:
					t.Fatal("introduction consumed request before music", id)
				default:
				}
				state := m.State("radio")
				if state.Phase != "chatter" || state.Next.ID != "a" {
					t.Fatal("UI did not show announced track", state)
				}
			}
			requested.write.Write([]byte("DREAMS"))
			if string(receive(t, sub)) != "DREAMS" {
				t.Fatal("announced recording missing")
			}
			select {
			case id := <-repo.used:
				if mode != "broadcast" {
					t.Fatal("unannounced request consumed", id)
				}
				if id != "request" {
					t.Fatal(id)
				}
			case <-time.After(100 * time.Millisecond):
				if mode == "broadcast" {
					t.Fatal("request not fulfilled after actual music")
				}
			}
			requested.write.Close()
		})
	}
}
