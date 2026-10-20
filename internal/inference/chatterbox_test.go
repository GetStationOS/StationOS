package inference

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"stationos.local/stationos/internal/ai"
)

func TestChatterboxHealthUsesExistingDiscovery(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"status":"ok","model":"chatterbox-nano","device":"cpu"}`, true},
		{`{"status":"loading","model":"chatterbox-nano"}`, false},
		{`{"status":"ok","model":"another-model"}`, false},
		{`not json`, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/health" || r.Header.Get("Authorization") != "" {
					t.Error(r.Method, r.URL.Path, r.Header)
				}
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			result, err := NewClient().Check(context.Background(), ai.Connection{Type: "chatterbox", Address: server.URL}, "unused", "chatterbox-nano", true)
			if tc.valid {
				if err != nil || !result.Reachable || !result.ModelAvailable || result.Tools || result.Structured || len(result.Models) != 1 {
					t.Fatal(result, err)
				}
			} else if err == nil {
				t.Fatal("unhealthy service accepted")
			}
		})
	}
}
