package producer

import (
	"context"
	"errors"
	"testing"
	"time"
)

type temporaryError struct{ delay time.Duration }

func (e temporaryError) Error() string             { return "rate limited" }
func (e temporaryError) RetryAfter() time.Duration { return e.delay }

type retryModel struct {
	calls    int
	failures int
	err      error
}

func (m *retryModel) Respond(context.Context, string, string, string, []Item, []Tool) (Response, error) {
	m.calls++
	if m.calls <= m.failures {
		return Response{}, m.err
	}
	return Response{Text: "Finished", Usage: Usage{Input: 100, Output: 20}}, nil
}
func TestRateRetryDoesNotLoseUsageOrRepeatSuccessfulResponse(t *testing.T) {
	m := &retryModel{failures: 1, err: temporaryError{time.Millisecond}}
	s := &Service{Model: m, runs: map[string]*activeRun{"test": {}}}
	response, err := s.respond(context.Background(), "test", "model", "key", "brief", nil, nil)
	if err != nil || m.calls != 2 || response.Text != "Finished" {
		t.Fatal(response, err, m.calls)
	}
	activity, _ := s.Activity("test")
	if activity.Usage.Input != 100 || activity.Usage.Output != 20 {
		t.Fatal(activity)
	}
}
func TestQuotaAndLongWaitDoNotRetry(t *testing.T) {
	for _, problem := range []error{errors.New("quota exhausted"), temporaryError{31 * time.Second}} {
		m := &retryModel{failures: 10, err: problem}
		s := &Service{Model: m}
		_, err := s.respond(context.Background(), "test", "model", "key", "brief", nil, nil)
		if err == nil || m.calls != 1 {
			t.Fatal(err, m.calls)
		}
	}
}
func TestRateRetriesAreBounded(t *testing.T) {
	m := &retryModel{failures: 10, err: temporaryError{time.Millisecond}}
	s := &Service{Model: m}
	_, err := s.respond(context.Background(), "test", "model", "key", "brief", nil, nil)
	if err == nil || m.calls != 4 {
		t.Fatal(err, m.calls)
	}
}
func TestStopCancelsRateWait(t *testing.T) {
	m := &retryModel{failures: 10, err: temporaryError{20 * time.Second}}
	s := &Service{Model: m}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, err := s.respond(ctx, "test", "model", "key", "brief", nil, nil)
	if !errors.Is(err, context.Canceled) || m.calls != 1 || time.Since(started) > time.Second {
		t.Fatal(err, m.calls)
	}
}
