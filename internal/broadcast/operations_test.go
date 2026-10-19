package broadcast

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingStore struct {
	operations.Store
	mu     sync.Mutex
	events map[string]operations.Event
}

func (s *recordingStore) Recover(context.Context, time.Time) error { return nil }
func (s *recordingStore) Save(_ context.Context, e operations.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[e.ID] = e
	return nil
}
func TestTransmissionHistorySharedListenersAndInterruptedTrack(t *testing.T) {
	store := &recordingStore{events: map[string]operations.Event{}}
	rec, err := operations.New(context.Background(), store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	m, encoder, _ := fixture(t, Options{Operations: rec})
	if m.State("s").Status != "Dormant" {
		t.Fatal("observer started broadcast")
	}
	one, err := m.Subscribe(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	two, err := m.Subscribe(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	current := opened(t, encoder)
	go func() { _, _ = current.write.Write(bytes.Repeat([]byte{1}, 4096)) }()
	for _, sub := range []*Subscription{one, two} {
		select {
		case <-sub.Audio:
		case <-time.After(time.Second):
			t.Fatal("no audio")
		}
	}
	one.Close()
	two.Close()
	m.Close()
	rec.Close()
	music := 0
	started, stopped := false, false
	for _, e := range store.events {
		if e.Kind == "music" {
			music++
			if e.AudioBytes != 4096 || e.Outcome != "interrupted" {
				t.Fatalf("wrong transmission %+v", e)
			}
		}
		started = started || e.Kind == "started"
		stopped = stopped || e.Kind == "stopped"
	}
	if music != 1 || !started || !stopped {
		t.Fatalf("shared transmission: music=%d started=%v stopped=%v", music, started, stopped)
	}
}

func TestSelectionObservationsDescribeOutcomesWithoutIncidents(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested bool
		err       error
		want      string
	}{
		{"DJ", false, nil, "DJ selected"}, {"pending", false, production.ErrPlanPending, "not ready"}, {"failed", false, errors.New("private provider response"), "could not be used"}, {"request", true, nil, "Listener request queued"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &recordingStore{events: map[string]operations.Event{}}
			recorder, err := operations.New(context.Background(), store, nil)
			if err != nil {
				t.Fatal(err)
			}
			manager := New(nil, nil, nil, Options{Operations: recorder})
			show := schedule.Occurrence{ShowID: "show", Title: "Jazz After Dark", PlaylistID: "pool", DJ: djs.Profile{ID: "dj", Name: "Miles"}}
			manager.recordSelection("radio", show, content.AudioItem{ID: "track", Title: "Cheryl", Artist: "Charlie Parker"}, tc.requested, tc.err)
			recorder.Close()
			if len(store.events) != 1 {
				t.Fatal(store.events)
			}
			for _, event := range store.events {
				if event.Kind != "selection" || event.TrackID != "track" || event.ShowID != "show" || event.IncidentCode != "" || event.Severity != "" || event.Outcome != "observed" || !event.Ended.Equal(event.Started) || !strings.Contains(event.Title+event.Reason, tc.want) || strings.Contains(event.Reason, "private provider response") {
					t.Fatal(event)
				}
			}
		})
	}
}
