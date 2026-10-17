package changefeed

import (
	"sync"
	"testing"
)

func TestCoalescingTopicIsolationAndRelease(t *testing.T) {
	bus := &Bus{}
	changes, stop := bus.Watch("operations")
	other, release := bus.Watch("assistant")
	defer release()
	var writers sync.WaitGroup
	for i := 0; i < 8; i++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for j := 0; j < 1000; j++ {
				bus.Publish("operations")
			}
		}()
	}
	writers.Wait()
	select {
	case <-changes:
	default:
		t.Fatal("lost change")
	}
	select {
	case <-changes:
		t.Fatal("signals did not coalesce")
	default:
	}
	select {
	case <-other:
		t.Fatal("cross-topic notification")
	default:
	}
	stop()
	stop()
	bus.Publish("operations")
	select {
	case <-changes:
		t.Fatal("released observer retained")
	default:
	}
}
