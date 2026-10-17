package producer

import (
	"testing"
	"time"
)

func TestActivityObserverTracksWorkAndUsage(t *testing.T) {
	s := &Service{runs: map[string]*activeRun{"chat": {activity: Activity{Stage: "Starting"}}}}
	changes, stop := s.WatchChanges()
	defer stop()
	s.activity("chat", "Waiting for the AI response", false)
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("missing activity notification")
	}
	s.recordUsage("chat", Usage{Input: 20, Output: 10})
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("missing usage notification")
	}
	activity, ok := s.Activity("chat")
	if !ok || activity.Usage.Input != 20 || activity.Stage != "Waiting for the AI response" {
		t.Fatal(activity)
	}
}
