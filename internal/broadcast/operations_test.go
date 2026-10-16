package broadcast

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"stationos.local/stationos/internal/operations"
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
