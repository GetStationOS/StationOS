package broadcast

import (
	"context"
	"io"
	"log/slog"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
	"sync/atomic"
	"testing"
	"time"
)

type timedSchedule struct {
	start time.Time
	shows []schedule.Show
}

func (s timedSchedule) Resolve(_ context.Context, id string, at time.Time) (schedule.Timeline, error) {
	return schedule.Resolve(s.shows, id, "fallback", "Default", at, time.UTC), nil
}

type showPools struct{}

func (showPools) Assignment(context.Context, string) (string, error) { return "fallback", nil }
func (showPools) Assign(context.Context, string, string) error       { return nil }
func (showPools) Pool(_ context.Context, id string) ([]content.AudioItem, error) {
	return []content.AudioItem{length(id, 90)}, nil
}

type readyLinks struct{ called chan production.LinkRequest }

func (p readyLinks) Link(_ context.Context, r production.LinkRequest) ([]byte, error) {
	p.called <- r
	return []byte("HANDOVER"), nil
}
func TestTrackFinishesAndHandoverKeepsBothListenersConnected(t *testing.T) {
	start := time.Date(2026, 10, 12, 9, 59, 0, 0, time.UTC)
	var seconds atomic.Int64
	source := timedSchedule{start: start, shows: []schedule.Show{{ID: "pop", StationID: "radio", Name: "Pop", Day: 0, Start: 540, End: 600, PlaylistID: "pop-pool", DJ: djs.Profile{ID: "alex", Name: "Alex", HasKey: true}}, {ID: "jazz", StationID: "radio", Name: "Jazz", Day: 0, Start: 600, End: 660, PlaylistID: "jazz-pool", DJ: djs.Profile{ID: "miles", Name: "Miles"}}}}
	links := readyLinks{called: make(chan production.LinkRequest, 4)}
	encoder := &fakeEncoder{calls: make(chan opening, 16)}
	m := New(showPools{}, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Schedule: source, Links: links, Now: func() time.Time { return start.Add(time.Duration(seconds.Load()) * time.Second) }})
	defer m.Close()
	a, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	first := opened(t, encoder)
	if first.item.ID != "pop-pool" {
		t.Fatal(first.item)
	}
	req := <-links.called
	if !req.Handover || req.Following.DJ.Name != "Miles" || req.Current.ID != "pop-pool" || req.Next.ID != "jazz-pool" {
		t.Fatal(req)
	}
	first.write.Write([]byte("POP"))
	if string(receive(t, a)) != "POP" || string(receive(t, b)) != "POP" {
		t.Fatal("listeners differ")
	}
	seconds.Store(61)
	first.write.Write([]byte("FINISH"))
	if string(receive(t, a)) != "FINISH" || string(receive(t, b)) != "FINISH" {
		t.Fatal("track interrupted")
	}
	if m.State("radio").Programme.DJ.Name != "Alex" {
		t.Fatal("DJ changed mid-track")
	}
	first.write.Close()
	if string(receive(t, a)) != "HANDOVER" || string(receive(t, b)) != "HANDOVER" {
		t.Fatal("handover missing")
	}
	second := opened(t, encoder)
	if second.item.ID != "jazz-pool" {
		t.Fatal("wrong show playlist", second.item)
	}
	second.write.Write([]byte("JAZZ"))
	if string(receive(t, a)) != "JAZZ" || string(receive(t, b)) != "JAZZ" {
		t.Fatal("new show not shared")
	}
	if s := m.State("radio"); s.Programme.DJ.Name != "Miles" || s.Listeners != 2 {
		t.Fatal(s)
	}
	select {
	case <-a.Done:
		t.Fatal("handover disconnected listener")
	default:
	}
}

type slowPreparation struct{ started chan struct{} }

func (p slowPreparation) Prepare(ctx context.Context, _ schedule.Occurrence, _ schedule.Occurrence, _ []content.AudioItem) (production.Prepared, error) {
	close(p.started)
	<-ctx.Done()
	return production.Prepared{}, ctx.Err()
}
func TestSlowAIIsNeverRequiredToStartMusic(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	p := slowPreparation{started: make(chan struct{})}
	source := timedSchedule{shows: []schedule.Show{{ID: "show", StationID: "radio", Day: 0, Start: 540, End: 600, PlaylistID: "pop", DJ: djs.Profile{ID: "dj", HasKey: true}}}}
	e := &fakeEncoder{calls: make(chan opening, 16)}
	m := New(showPools{}, e, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Schedule: source, Producer: p, Now: func() time.Time { return now }})
	defer m.Close()
	sub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first := opened(t, e)
	first.write.Write([]byte("MUSIC"))
	if string(receive(t, sub)) != "MUSIC" {
		t.Fatal("AI stalled playout")
	}
}

type statusPreparation struct{ fail bool }

func (p statusPreparation) Prepare(_ context.Context, _ schedule.Occurrence, _ schedule.Occurrence, pool []content.AudioItem) (production.Prepared, error) {
	if p.fail {
		return production.Prepared{}, production.ErrUnavailable
	}
	return production.Prepared{Plan: production.Plan{TrackIDs: []string{pool[0].ID}}}, nil
}
func TestDJPreparationStatusUpdatesDuringCurrentTrack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "failed"}[fail], func(t *testing.T) {
			now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
			source := timedSchedule{shows: []schedule.Show{{ID: "show", StationID: "radio", Day: 0, Start: 540, End: 600, PlaylistID: "pop", DJ: djs.Profile{ID: "dj", HasKey: true}}}}
			e := &fakeEncoder{calls: make(chan opening, 16)}
			m := New(showPools{}, e, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Schedule: source, Producer: statusPreparation{fail: fail}, Now: func() time.Time { return now }})
			defer m.Close()
			sub, err := m.Subscribe(context.Background(), "radio")
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			current := opened(t, e)
			want := "Ready"
			if fail {
				want = "Unavailable"
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				current.write.Write([]byte("music"))
				receive(t, sub)
				if m.State("radio").PresenterStatus == want {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("stale presenter status", m.State("radio").PresenterStatus)
				}
			}
			if e.count.Load() != 1 {
				t.Fatal("status update restarted the track")
			}
		})
	}
}
