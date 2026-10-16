package operations

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type testStore struct {
	Store
	mu      sync.Mutex
	events  []Event
	fail    bool
	block   chan struct{}
	entered chan struct{}
}

func (s *testStore) Recover(context.Context, time.Time) error { return nil }
func (s *testStore) Save(ctx context.Context, e Event) error {
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		default:
		}
	}
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("disk unavailable")
	}
	s.events = append(s.events, e)
	return nil
}
func TestRecorderFlushesAndLatchesFailure(t *testing.T) {
	store := &testStore{}
	r, err := New(context.Background(), store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r.Record(Event{StationID: "s"})
	r.Close()
	r.Close()
	if len(store.events) != 1 || store.events[0].ID == "" || !r.Healthy() {
		t.Fatal("flush failed")
	}
	store = &testStore{fail: true}
	r, _ = New(context.Background(), store, nil)
	r.Record(Event{})
	r.Close()
	if r.Healthy() {
		t.Fatal("write failure hidden")
	}
}
func TestRecorderQueueNeverBlocksAudio(t *testing.T) {
	store := &testStore{block: make(chan struct{}), entered: make(chan struct{}, 1)}
	r, _ := New(context.Background(), store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.Record(Event{})
	<-store.entered
	start := time.Now()
	for n := 0; n < 600; n++ {
		r.Record(Event{})
	}
	if time.Since(start) > time.Second {
		t.Fatal("audio blocked")
	}
	if r.Healthy() {
		t.Fatal("overflow hidden")
	}
	close(store.block)
	r.Close()
}
