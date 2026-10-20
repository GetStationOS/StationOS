package chatterbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"stationos.local/stationos/internal/ai"
)

func wave() []byte {
	b := make([]byte, 46)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 38)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 24000)
	binary.LittleEndian.PutUint32(b[28:], 48000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 2)
	return b
}
func TestSynthesize(t *testing.T) {
	for _, suffix := range []string{"", "/", "/nano/"} {
		t.Run(suffix, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := "/tts"
				if suffix == "/nano/" {
					path = "/nano/tts"
				}
				if r.Method != "POST" || r.URL.Path != path || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "audio/wav" || r.Header.Get("Authorization") != "" {
					t.Error("incorrect request", r.Method, r.URL.Path, r.Header)
				}
				var body map[string]string
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["text"] != "Hello, DJ 世界" {
					t.Error("incorrect text JSON", body)
				}
				w.Header().Set("Content-Type", "audio/wav")
				w.Header().Set("X-RTF", "0.40")
				w.Write(wave())
			}))
			defer server.Close()
			audio, err := (Provider{HTTP: NewClient()}).Synthesize(context.Background(), ai.Connection{Address: server.URL + suffix}, "Hello, DJ 世界")
			if err != nil || !bytes.Equal(audio, wave()) {
				t.Fatal(audio, err)
			}
		})
	}
}
func TestInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name, media string
		status      int
		body        []byte
	}{
		{"server", "audio/wav", 500, wave()}, {"no content", "audio/wav", 204, nil}, {"empty", "audio/wav", 200, nil}, {"wrong type", "application/json", 200, []byte(`{}`)}, {"malformed", "audio/wav", 200, []byte("RIFFnot-wave")}, {"truncated", "audio/wav", 200, wave()[:44]}, {"oversize", "audio/wav", 200, make([]byte, MaxAudioBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.media)
				w.WriteHeader(tc.status)
				w.Write(tc.body)
			}))
			defer server.Close()
			if _, err := (Provider{HTTP: NewClient()}).Synthesize(context.Background(), ai.Connection{Address: server.URL}, "Hello"); err == nil {
				t.Fatal("bad audio accepted")
			}
		})
	}
}
func TestTransportFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	address := server.URL
	server.Close()
	p := Provider{HTTP: NewClient()}
	if _, err := p.Synthesize(context.Background(), ai.Connection{Address: address}, "Hello"); err == nil || !strings.Contains(err.Error(), "connection failed") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Synthesize(ctx, ai.Connection{Address: address}, "Hello"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()
	if _, err := p.Synthesize(ctx, ai.Connection{Address: server.URL}, "Hello"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err := p.Synthesize(context.Background(), ai.Connection{Address: "://bad"}, "Hello"); err == nil {
		t.Fatal("invalid URL accepted")
	}
	if _, err := p.Synthesize(context.Background(), ai.Connection{Address: address}, " "); err == nil {
		t.Fatal("empty text accepted")
	}
}
func TestRejectRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", 307) }))
	defer server.Close()
	if _, err := (Provider{HTTP: NewClient()}).Synthesize(context.Background(), ai.Connection{Address: server.URL}, "Hello"); err == nil {
		t.Fatal("redirect followed")
	}
}
