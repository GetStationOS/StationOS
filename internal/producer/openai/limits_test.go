package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestQuotaFailuresAreSpecificAndNeverRetryable(t *testing.T) {
	for _, tc := range []struct{ code, want string }{
		{"credit_balance_exhausted", "credits are exhausted"},
		{"organization_spend_limit_exceeded", "organization spending limit"},
		{"project_spend_limit_exceeded", "project spending limit"},
		{"organization_usage_limit_exceeded", "organization usage limit"},
		{"insufficient_quota", "quota is unavailable"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			m := Model{Client: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"1"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error":{"code":%q,"type":"insufficient_quota","message":"private secret-key"}}`, tc.code)))}, nil
			})}}
			_, err := m.Respond(context.Background(), "model", "secret-key", "", nil, nil)
			var temporary interface {
				error
				RetryAfter() time.Duration
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) || errors.As(err, &temporary) || strings.Contains(err.Error(), "secret-key") {
				t.Fatal(err)
			}
		})
	}
}
func TestTemporaryRateLimitAndOversizedRequest(t *testing.T) {
	for _, tc := range []struct {
		body      string
		temporary bool
	}{
		{`{"error":{"code":"rate_limit_exceeded"}}`, true},
		{`{"error":{"code":"slow_down","type":"rate_limit_error"}}`, true},
		{`{"error":{"code":"rate_limit_exceeded","message":"Request too large for this model"}}`, false},
		{`{"error":{"code":"unknown","message":"secret-key"}}`, false},
		{`not JSON secret-key`, false},
	} {
		err := limitError(&http.Response{Header: http.Header{"Retry-After": {"17"}}, Body: io.NopCloser(strings.NewReader(tc.body))})
		var temporary interface {
			error
			RetryAfter() time.Duration
		}
		if errors.As(err, &temporary) != tc.temporary {
			t.Fatal(err)
		}
		if tc.temporary && temporary.RetryAfter() != 17*time.Second {
			t.Fatal(temporary.RetryAfter())
		}
		if strings.Contains(err.Error(), "secret-key") {
			t.Fatal(err)
		}
	}
}
func TestRetryAfterDateAndInvalidValues(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	if got := retryDelay(now.Add(20*time.Second).Format(http.TimeFormat), now); got != 20*time.Second {
		t.Fatal(got)
	}
	if retryDelay("999999999", now) <= 30*time.Second {
		t.Fatal("long provider delay must defer rather than retry early")
	}
	for _, v := range []string{"", "-1", "NaN", "+Inf", "bad"} {
		if retryDelay(v, now) != 0 {
			t.Fatal(v)
		}
	}
}
func TestProviderReportsMeasuredTokenUsage(t *testing.T) {
	m := Model{Client: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"completed","usage":{"input_tokens":1200,"output_tokens":37},"output":[]}`))}, nil
	})}}
	response, err := m.Respond(context.Background(), "model", "key", "", nil, nil)
	if err != nil || response.Usage.Input != 1200 || response.Usage.Output != 37 {
		t.Fatal(response, err)
	}
}
