package broadcast

import (
	"context"
	"io"
	"log/slog"
	"stationos.local/stationos/internal/operations"
	"strings"
	"testing"
	"time"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

type failingSelection struct{}

func (failingSelection) SelectTrack(context.Context, production.SelectionRequest) (string, error) {
	return "", production.ErrUnavailable
}

type controlledSelection struct {
	called  chan production.SelectionRequest
	answers chan string
}

func (s controlledSelection) SelectTrack(ctx context.Context, q production.SelectionRequest) (string, error) {
	select {
	case s.called <- q:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case id := <-s.answers:
		return id, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestNextTrackChoiceRunsDuringMusic(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "valid", false: "unoffered"}[valid], func(t *testing.T) {
			now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
			source := timedSchedule{shows: []schedule.Show{{ID: "show", StationID: "radio", Day: 0, Start: 540, End: 600, PlaylistID: "pool", DJ: djs.Profile{ID: "dj", TextReady: true}}}}
			selector := controlledSelection{called: make(chan production.SelectionRequest, 4), answers: make(chan string, 4)}
			m, e, repo := fixture(t, Options{Schedule: source, Selector: selector, Now: func() time.Time { return now }})
			repo.pool = append(repo.pool, content.AudioItem{ID: "three"})
			sub, err := m.Subscribe(context.Background(), "radio")
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			first := opened(t, e)
			var request production.SelectionRequest
			select {
			case request = <-selector.called:
			case <-time.After(3 * time.Second):
				t.Fatal("selection not started")
			}
			first.write.Write([]byte("MUSIC"))
			if string(receive(t, sub)) != "MUSIC" {
				t.Fatal("selection blocked music")
			}
			if m.State("radio").CurationStatus != "Preparing" {
				t.Fatal(m.State("radio"))
			}
			if len(request.Candidates) != 2 || request.Current.ID != first.item.ID {
				t.Fatal(request)
			}
			choice := request.Candidates[len(request.Candidates)-1].ID
			if !valid {
				choice = "invented"
			}
			selector.answers <- choice
			deadline := time.Now().Add(3 * time.Second)
			want := "Ready"
			if !valid {
				want = "Unavailable"
			}
			for {
				first.write.Write([]byte("MUSIC"))
				receive(t, sub)
				state := m.State("radio")
				if state.CurationStatus == want {
					if state.NextProgrammeKey == "" {
						t.Fatal("queue missing owning programme", state)
					}
					if valid && state.Next.ID != choice {
						t.Fatal("live next track not updated", state)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal(state)
				}
			}
			first.write.Close()
			next := opened(t, e)
			if valid && next.item.ID != choice {
				t.Fatal("DJ choice not played", next.item, choice)
			}
			if !valid && (next.item.ID == "invented" || next.item.ID == first.item.ID) {
				t.Fatal("invalid choice bypassed fallback", next.item)
			}
		})
	}
}

func TestTrackShortlistBoundsAndTiming(t *testing.T) {
	pool := []content.AudioItem{length("current", 60), length("short", 60), length("long", 600)}
	got := trackShortlist(pool, "current", Rotation{}, 2*time.Minute)
	if len(got) != 1 || got[0].ID != "short" {
		t.Fatal(got)
	}
	got = trackShortlist(pool, "current", Rotation{}, 30*time.Second)
	if len(got) != 1 || got[0].ID != "short" {
		t.Fatal(got)
	}
	pool = nil
	for i := range 20 {
		pool = append(pool, content.AudioItem{ID: string(rune('a' + i))})
	}
	history := Rotation{LastPlayed: map[string]time.Time{"a": time.Now()}}
	got = trackShortlist(pool, "b", history, 0)
	if len(got) != 10 {
		t.Fatal(len(got))
	}
	for _, item := range got {
		if item.ID == "b" || item.ID == "a" {
			t.Fatal("current/recent track shortlisted ahead of unplayed music", got)
		}
	}
}

func TestChosenTrackMatchesSpeechAndIsRevalidated(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "spoken-choice", true: "removed-before-playback"}[remove], func(t *testing.T) {
			now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
			source := timedSchedule{shows: []schedule.Show{{ID: "show", StationID: "radio", Day: 0, Start: 540, End: 600, PlaylistID: "pool", DJ: djs.Profile{ID: "dj", TextReady: true}}}}
			selector := controlledSelection{called: make(chan production.SelectionRequest, 4), answers: make(chan string, 4)}
			links := readyLinks{called: make(chan production.LinkRequest, 4)}
			m, e, repo := fixture(t, Options{Schedule: source, Selector: selector, Links: links, Now: func() time.Time { return now }})
			repo.pool = append(repo.pool, content.AudioItem{ID: "three"})
			sub, err := m.Subscribe(context.Background(), "radio")
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			first := opened(t, e)
			var q production.SelectionRequest
			select {
			case q = <-selector.called:
			case <-time.After(3 * time.Second):
				t.Fatal("selection missing")
			}
			choice := q.Candidates[len(q.Candidates)-1].ID
			selector.answers <- choice
			select {
			case link := <-links.called:
				if link.Next.ID != choice {
					t.Fatal("speech used a different track", link)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("link missing")
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				first.write.Write([]byte("MUSIC"))
				receive(t, sub)
				if m.State("radio").PresenterStatus == "Ready" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("speech never ready")
				}
			}
			if remove {
				repo.mu.Lock()
				filtered := []content.AudioItem{}
				for _, item := range repo.pool {
					if item.ID != choice {
						filtered = append(filtered, item)
					}
				}
				repo.pool = filtered
				repo.mu.Unlock()
			}
			first.write.Close()
			next := opened(t, e)
			if remove {
				if next.item.ID == choice {
					t.Fatal("removed track played")
				}
				next.write.Write([]byte("NEXT"))
				if string(receive(t, sub)) != "NEXT" {
					t.Fatal("stale speech was played")
				}
			} else {
				if next.item.ID != choice {
					t.Fatal("choice changed", next.item)
				}
				if string(receive(t, sub)) != "HANDOVER" {
					t.Fatal("introduction missing")
				}
			}
		})
	}
}

type pendingEpisodeSelection struct{ called chan struct{} }

func (p pendingEpisodeSelection) SelectTrack(context.Context, production.SelectionRequest) (string, error) {
	select {
	case p.called <- struct{}{}:
	default:
	}
	return "", production.ErrPlanPending
}
func TestPendingEpisodePlanKeepsMusicWithoutFalseCurationFailures(t *testing.T) {
	store := &recordingStore{events: map[string]operations.Event{}}
	rec, err := operations.New(context.Background(), store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	source := timedSchedule{shows: []schedule.Show{{ID: "show", StationID: "radio", Day: 0, Start: 540, End: 600, PlaylistID: "pool", DJ: djs.Profile{ID: "dj", TextReady: true}}}}
	selector := pendingEpisodeSelection{called: make(chan struct{}, 1)}
	manager, encoder, _ := fixture(t, Options{Schedule: source, Selector: selector, Operations: rec, Now: func() time.Time { return now }})
	sub, err := manager.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first := opened(t, encoder)
	select {
	case <-selector.called:
	case <-time.After(time.Second):
		t.Fatal("episode selection not attempted")
	}
	for i := 0; i < 4; i++ {
		first.write.Write([]byte("MUSIC"))
		receive(t, sub)
	}
	if state := manager.State("radio"); state.CurationStatus != "Preparing" || state.Current.ID == "" {
		t.Fatal("pending plan stopped music or became unavailable", state)
	}
	sub.Close()
	manager.Close()
	rec.Close()
	store.mu.Lock()
	defer store.mu.Unlock()
	selectionRecorded := false
	for _, event := range store.events {
		if event.Kind == "selection" && strings.Contains(event.Reason, "not ready") {
			selectionRecorded = true
		}
		if strings.Contains(event.Reason, "DJ curation failed") {
			t.Fatal("normal pending plan created curation failure", event)
		}
	}
	if !selectionRecorded {
		t.Fatal("applied fallback missing from timeline")
	}
}
