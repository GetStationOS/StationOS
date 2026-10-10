package broadcast

import (
	"context"
	"testing"
	"time"
)

func TestStatusWatchIsQuietForUnchangedAudioAndDoesNotStartStation(t *testing.T) {
	m, e, _ := fixture(t, Options{})
	changes, stop := m.WatchChanges()
	defer stop()
	<-changes
	if e.count.Load() != 0 || m.State("radio").Listeners != 0 {
		t.Fatal("watcher started radio")
	}
	sub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	<-changes
	first := opened(t, e)
	first.write.Write([]byte("one"))
	receive(t, sub)
	<-changes
	first.write.Write([]byte("two"))
	receive(t, sub)
	select {
	case <-changes:
		t.Fatal("unchanged audio caused status event")
	case <-time.After(30 * time.Millisecond):
	}
	second, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	<-changes
	second.Close()
	<-changes
	stop()
	stop()
	m.Changed()
	select {
	case <-changes:
		t.Fatal("cancelled watcher received notification")
	default:
	}
}
func TestSlowStatusWatcherCoalescesWithoutBlockingAudio(t *testing.T) {
	m, _, _ := fixture(t, Options{})
	changes, stop := m.WatchChanges()
	defer stop()
	for range 1000 {
		m.Changed()
	}
	<-changes
	select {
	case <-changes:
		t.Fatal("unbounded event queue")
	default:
	}
}

func TestStatusWatchEndsWhenManagerStops(t *testing.T) {
	m, _, _ := fixture(t, Options{})
	changes, stop := m.WatchChanges()
	defer stop()
	<-changes
	m.Close()
	select {
	case _, open := <-changes:
		if open {
			t.Fatal("watch remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("watch did not end")
	}
	ended, release := m.WatchChanges()
	defer release()
	if _, open := <-ended; open {
		t.Fatal("closed manager admitted watcher")
	}
}
