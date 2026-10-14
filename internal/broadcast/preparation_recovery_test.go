package broadcast

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

type fundedPreparation struct {
	available atomic.Bool
	attempts  atomic.Int32
	called    chan int
	gate      <-chan struct{}
}

func (p *fundedPreparation) Prepare(ctx context.Context, _ schedule.Occurrence, _ schedule.Occurrence, pool []content.AudioItem) (production.Prepared, error) {
	n := int(p.attempts.Add(1))
	p.called <- n
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return production.Prepared{}, ctx.Err()
		}
	}
	if !p.available.Load() {
		return production.Prepared{}, production.ErrUnavailable
	}
	return production.Prepared{Plan: production.Plan{TrackIDs: []string{pool[0].ID}}}, nil
}
func recoveryBroadcast(t *testing.T, p *fundedPreparation, seconds *atomic.Int64, local ...bool) (*Manager, *fakeEncoder, *Subscription, opening) {
	t.Helper()
	start := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	profile := djs.Profile{ID: "dj", HasKey: true}
	if len(local) > 0 && local[0] {
		profile.HasKey = false
		profile.TextReady = true
	}
	source := timedSchedule{shows: []schedule.Show{{ID: "show", StationID: "radio", Day: 0, Start: 540, End: 600, PlaylistID: "pop", DJ: profile}}}
	encoder := &fakeEncoder{calls: make(chan opening, 16)}
	manager := New(showPools{}, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Schedule: source, Producer: p, Now: func() time.Time { return start.Add(time.Duration(seconds.Load()) * time.Second) }})
	t.Cleanup(manager.Close)
	sub, err := manager.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)
	return manager, encoder, sub, opened(t, encoder)
}
func musicStatus(t *testing.T, m *Manager, sub *Subscription, current opening, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := current.write.Write([]byte("music")); err != nil {
			t.Fatal(err)
		}
		if string(receive(t, sub)) != "music" {
			t.Fatal("music interrupted")
		}
		if m.State("radio").PresenterStatus == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("presenter status did not recover", m.State("radio"))
		}
	}
}
func preparationAttempt(t *testing.T, p *fundedPreparation, want int) {
	t.Helper()
	select {
	case n := <-p.called:
		if n != want {
			t.Fatal(n, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("preparation not retried", want)
	}
}
func TestDJRecoversAfterFundingAtNextSongWithoutRestartingStation(t *testing.T) {
	p := &fundedPreparation{called: make(chan int, 8)}
	var seconds atomic.Int64
	manager, encoder, sub, current := recoveryBroadcast(t, p, &seconds)
	preparationAttempt(t, p, 1)
	musicStatus(t, manager, sub, current, "Unavailable")
	p.available.Store(true)
	// Even a long song gets no extra preparation attempt in its middle.
	seconds.Store(600)
	for range 5 {
		musicStatus(t, manager, sub, current, "Unavailable")
	}
	if p.attempts.Load() != 1 || encoder.count.Load() != 1 {
		t.Fatal("retried mid-song")
	}
	current.write.Close()
	second := opened(t, encoder)
	preparationAttempt(t, p, 2)
	musicStatus(t, manager, sub, second, "Ready")
	// A successful plan is retained at later song boundaries.
	second.write.Close()
	third := opened(t, encoder)
	musicStatus(t, manager, sub, third, "Ready")
	if p.attempts.Load() != 2 || encoder.count.Load() != 3 {
		t.Fatal("success repeated generation", p.attempts.Load(), encoder.count.Load())
	}
	select {
	case <-sub.Done:
		t.Fatal("recovery disconnected listener")
	default:
	}
}
func TestPersistentDJFailureRetriesOnceAtEachSongBoundary(t *testing.T) {
	p := &fundedPreparation{called: make(chan int, 8)}
	var seconds atomic.Int64
	manager, encoder, sub, current := recoveryBroadcast(t, p, &seconds)
	preparationAttempt(t, p, 1)
	musicStatus(t, manager, sub, current, "Unavailable")
	for i := 0; i < 3; i++ {
		for range 5 {
			musicStatus(t, manager, sub, current, "Unavailable")
		}
		if p.attempts.Load() != int32(i+1) {
			t.Fatal("multiple retries within one song", p.attempts.Load())
		}
		// No elapsed-time threshold: even a short song permits the next attempt.
		current.write.Close()
		current = opened(t, encoder)
		preparationAttempt(t, p, i+2)
		musicStatus(t, manager, sub, current, "Unavailable")
	}
	seconds.Store(3600)
	current.write.Close()
	fallback := opened(t, encoder)
	musicStatus(t, manager, sub, fallback, "Music only")
	if p.attempts.Load() != 4 {
		t.Fatal("retried an ended show", p.attempts.Load())
	}
}

func TestSongBoundariesDoNotRepeatPendingDJPreparation(t *testing.T) {
	p := &fundedPreparation{called: make(chan int, 8), gate: make(chan struct{})}
	var seconds atomic.Int64
	manager, encoder, sub, current := recoveryBroadcast(t, p, &seconds)
	preparationAttempt(t, p, 1)
	for range 3 {
		musicStatus(t, manager, sub, current, "Preparing")
		current.write.Close()
		current = opened(t, encoder)
	}
	musicStatus(t, manager, sub, current, "Preparing")
	if p.attempts.Load() != 1 {
		t.Fatal("song boundaries duplicated pending AI work", p.attempts.Load())
	}
	// The blocked provider must still be cancelled when listening stops.
	sub.Close()
	manager.Close()
}

func TestLocalPreparationRecoversWithoutCloudKeyAtSongBoundary(t *testing.T) {
	p := &fundedPreparation{called: make(chan int, 8)}
	var seconds atomic.Int64
	manager, encoder, sub, current := recoveryBroadcast(t, p, &seconds, true)
	preparationAttempt(t, p, 1)
	musicStatus(t, manager, sub, current, "Unavailable")
	p.available.Store(true)
	for range 3 {
		musicStatus(t, manager, sub, current, "Unavailable")
	}
	if p.attempts.Load() != 1 {
		t.Fatal("local inference retried during music")
	}
	current.write.Close()
	next := opened(t, encoder)
	preparationAttempt(t, p, 2)
	musicStatus(t, manager, sub, next, "Ready")
	next.write.Close()
	third := opened(t, encoder)
	musicStatus(t, manager, sub, third, "Ready")
	if p.attempts.Load() != 2 {
		t.Fatal("local success duplicated")
	}
}

func TestReadyLinkRetainsFailedCurationAndStillPlays(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	source := timedSchedule{shows: []schedule.Show{{ID: "show", StationID: "radio", Day: 0, Start: 540, End: 600, PlaylistID: "pop", DJ: djs.Profile{ID: "dj", HasKey: true}}}}
	encoder := &fakeEncoder{calls: make(chan opening, 16)}
	links := readyLinks{called: make(chan production.LinkRequest, 8)}
	manager := New(showPools{}, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Schedule: source, Producer: statusPreparation{fail: true}, Links: links, Now: func() time.Time { return now }})
	defer manager.Close()
	sub, err := manager.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first := opened(t, encoder)
	until := time.Now().Add(2 * time.Second)
	for {
		first.write.Write([]byte("MUSIC"))
		if string(receive(t, sub)) != "MUSIC" {
			t.Fatal("music interrupted")
		}
		state := manager.State("radio")
		if state.PresenterStatus == "Ready" && state.CurationStatus == "Unavailable" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("ready speech masked curation failure", state)
		}
	}
	first.write.Close()
	next := opened(t, encoder)
	// The ready spoken link is still emitted despite failed show curation.
	if string(receive(t, sub)) != "HANDOVER" {
		t.Fatal("ready link was suppressed by failed curation")
	}
	next.write.Write([]byte("NEXT"))
	if string(receive(t, sub)) != "NEXT" {
		t.Fatal("next music did not continue")
	}
}
