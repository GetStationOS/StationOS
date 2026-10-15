package inference

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stationos.local/stationos/internal/ai"
)

func TestDiscoveryPreservesHTTPStatusWithoutProviderBody(t *testing.T) {
	for _, status := range []int{401, 403, 429, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer sk-test-secret" {
				t.Error("discovery request missing path or credential")
			}
			w.WriteHeader(status)
			w.Write([]byte("private body sk-test-secret"))
		}))
		_, err := NewClient().Check(context.Background(), ai.Connection{Type: "compatible", Address: server.URL}, "sk-test-secret", "", false)
		server.Close()
		var failure *HTTPError
		if !errors.As(err, &failure) || failure.StatusCode != status {
			t.Fatalf("missing status %d: %v", status, err)
		}
		if strings.Contains(err.Error(), "private body") || strings.Contains(err.Error(), "sk-test-secret") {
			t.Fatal("provider body leaked")
		}
	}
}
