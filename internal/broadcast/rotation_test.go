package broadcast

import (
	"context"
	"errors"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRotationCandidates(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	pool := []content.AudioItem{length("a", 240), length("b", 240), length("c", 240)}
	cases := []struct {
		name     string
		played   map[string]time.Time
		previous string
		want     string
		relaxed  bool
	}{
		{"exact interval eligible", map[string]time.Time{"a": now.Add(-3 * time.Hour), "b": now.Add(-time.Hour), "c": now}, "c", "a", false},
		{"oldest fallback", map[string]time.Time{"a": now.Add(-time.Hour), "b": now.Add(-2 * time.Hour), "c": now}, "c", "b", true},
		{"avoid consecutive even when oldest", map[string]time.Time{"a": now, "b": now.Add(-time.Hour), "c": now.Add(-2 * time.Hour)}, "c", "b", true},
		{"unplayed beats recent", map[string]time.Time{"a": now, "c": now}, "c", "b", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, relaxed := rotationPool(pool, Rotation{Interval: 3 * time.Hour, LastPlayed: tc.played}, now, tc.previous)
			if len(got) != 1 || got[0].ID != tc.want || relaxed != tc.relaxed {
				t.Fatal(got, relaxed)
			}
			for _, remaining := range []time.Duration{0, 5 * time.Minute} {
				for i := 0; i < 10; i++ {
					if picked := selectTrack(got, tc.previous, remaining); picked.ID != tc.want {
						t.Fatal("selector bypassed rotation", picked)
					}
				}
			}
		})
	}
	got, relaxed := rotationPool(pool[:1], Rotation{Interval: 3 * time.Hour, LastPlayed: map[string]time.Time{"a": now}}, now, "a")
	if len(got) != 1 || !relaxed {
		t.Fatal("single-track pool should continue", got, relaxed)
	}
}

type memoryHistory struct {
	mu      sync.Mutex
	played  map[string]map[string]time.Time
	records int
	fail    bool
}

func (h *memoryHistory) Rotation(_ context.Context, id string, at time.Time) (Rotation, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := Rotation{Interval: 3 * time.Hour, LastPlayed: map[string]time.Time{}}
	var latest time.Time
	for track, started := range h.played[id] {
		result.LastPlayed[track] = started
		if started.After(latest) {
			latest = started
			result.LastTrackID = track
		}
	}
	return result, nil
}
func (h *memoryHistory) RecordPlay(_ context.Context, id, track string, at time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fail {
		return errors.New("database unavailable")
	}
	if h.played == nil {
		h.played = map[string]map[string]time.Time{}
	}
	if h.played[id] == nil {
		h.played[id] = map[string]time.Time{}
	}
	h.played[id][track] = at
	h.records++
	return nil
}
func (h *memoryHistory) count() int { h.mu.Lock(); defer h.mu.Unlock(); return h.records }
func TestRotationRecordsDeliveryAndSurvivesReconnect(t *testing.T) {
	now := time.Now()
	history := &memoryHistory{played: map[string]map[string]time.Time{"radio": {"one": now.Add(-time.Minute)}}}
	m, e, _ := fixture(t, Options{History: history})
	sub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	first := opened(t, e)
	if first.item.ID != "two" || history.count() != 0 {
		t.Fatal("selection counted or history ignored", first.item, history.count())
	}
	if _, err = first.write.Write([]byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	receive(t, sub)
	waitFor(t, func() bool { return history.count() == 1 })
	sub.Close()
	secondSub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer secondSub.Close()
	second := opened(t, e)
	if second.item.ID != "one" {
		t.Fatal("reconnect forgot last play", second.item)
	}
	if _, err = second.write.Write([]byte{4, 5}); err != nil {
		t.Fatal(err)
	}
	receive(t, secondSub)
	waitFor(t, func() bool { return history.count() == 2 })
	if !m.State("radio").RepeatRelaxed {
		t.Fatal("fallback not shown")
	}
}
func TestRotationDoesNotReplaceCommittedTrack(t *testing.T) {
	history := &memoryHistory{}
	m, e, repo := fixture(t, Options{History: history})
	repo.pool = append(repo.pool, content.AudioItem{ID: "three"})
	sub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first := opened(t, e)
	first.write.Write([]byte{1})
	receive(t, sub)
	waitFor(t, func() bool { return history.count() == 1 })
	queued := m.State("radio").Next.ID
	if queued == "" {
		t.Fatal("no queued track")
	}
	// Simulate newly recorded history before the queued song reaches airtime.
	history.RecordPlay(context.Background(), "radio", queued, time.Now())
	first.write.Close()
	next := opened(t, e)
	if next.item.ID != queued {
		t.Fatal("committed queue changed", next.item)
	}
}
func TestRotationWriteFailureStopsProduction(t *testing.T) {
	history := &memoryHistory{fail: true}
	m, e, _ := fixture(t, Options{History: history})
	sub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	first := opened(t, e)
	first.write.Write([]byte{1})
	waitFor(t, func() bool { return m.State("radio").Status == "Unavailable" })
	if e.count.Load() != 1 {
		t.Fatal("continued without recording history")
	}
	sub.Close()
}

// Shows share exact content identities even when their playlist IDs differ.
func TestRotationSurvivesShowHandover(t *testing.T) {
	start := time.Date(2026, 10, 12, 9, 59, 0, 0, time.UTC)
	var seconds atomic.Int64
	source := timedSchedule{shows: []schedule.Show{
		{ID: "morning", StationID: "radio", Day: 0, Start: 540, End: 600, PlaylistID: "morning"},
		{ID: "daytime", StationID: "radio", Day: 0, Start: 600, End: 660, PlaylistID: "daytime"},
	}}
	history := &memoryHistory{played: map[string]map[string]time.Time{"radio": {"two": start.Add(-time.Minute)}}}
	m, e, _ := fixture(t, Options{History: history, Schedule: source, Now: func() time.Time { return start.Add(time.Duration(seconds.Load()) * time.Second) }})
	sub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first := opened(t, e)
	if first.item.ID != "one" {
		t.Fatal(first.item)
	}
	first.write.Write([]byte{1})
	receive(t, sub)
	waitFor(t, func() bool { return history.count() == 1 })
	seconds.Store(61)
	first.write.Close()
	next := opened(t, e)
	if next.item.ID != "two" {
		t.Fatal("show handover reset history", next.item)
	}
	next.write.Write([]byte{2})
	receive(t, sub)
	if !m.State("radio").RepeatRelaxed || m.State("radio").Programme.ShowID != "daytime" {
		t.Fatal(m.State("radio"))
	}
}

type rotationSelector struct{ pool chan []content.AudioItem }

func (p rotationSelector) SelectTrack(ctx context.Context, request production.SelectionRequest) (string, error) {
	p.pool <- request.Candidates
	// Misbehaving providers cannot bypass the offered eligible candidates.
	return "one", nil
}
func TestRotationFiltersCurationAndRejectsStaleChoices(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	history := &memoryHistory{played: map[string]map[string]time.Time{"radio": {"one": now.Add(-time.Minute)}}}
	producer := rotationSelector{pool: make(chan []content.AudioItem, 4)}
	source := timedSchedule{shows: []schedule.Show{{ID: "show", StationID: "radio", Day: 0, Start: 540, End: 600, PlaylistID: "pool", DJ: djs.Profile{ID: "dj", HasKey: true}}}}
	m, e, repo := fixture(t, Options{History: history, Schedule: source, Selector: producer, Now: func() time.Time { return now }})
	repo.pool = append(repo.pool, content.AudioItem{ID: "three"})
	sub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first := opened(t, e)
	select {
	case candidates := <-producer.pool:
		for _, track := range candidates {
			if track.ID == "one" {
				t.Fatal("recent track sent to curator")
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("curator not called")
	}
	first.write.Write([]byte{1})
	receive(t, sub)
	waitFor(t, func() bool { return history.count() == 1 })
	first.write.Close()
	next := opened(t, e)
	if next.item.ID == "one" || next.item.ID == first.item.ID {
		t.Fatal("invalid choice bypassed rotation", next.item)
	}
}
func TestRotationUsesExactTrackIdentity(t *testing.T) {
	now := time.Now()
	pool := []content.AudioItem{{ID: "copy-a", Title: "Same song"}, {ID: "copy-b", Title: "Same song"}}
	got, relaxed := rotationPool(pool, Rotation{Interval: 3 * time.Hour, LastPlayed: map[string]time.Time{"copy-a": now}}, now, "copy-a")
	if len(got) != 1 || got[0].ID != "copy-b" || relaxed {
		t.Fatal("system detected duplicate recording", got, relaxed)
	}
}

func TestRotationDoesNotRecordZeroByteFailure(t *testing.T) {
	history := &memoryHistory{}
	m, e, _ := fixture(t, Options{History: history})
	sub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first := opened(t, e)
	first.write.Close()
	opened(t, e)
	if history.count() != 0 {
		t.Fatal("failed track recorded as a play")
	}
}
