package inference

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"stationos.local/stationos/internal/production"
	"testing"

	"stationos.local/stationos/internal/ai"
)

func TestChatterboxModelDiscovery(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"default_model":"nano","models":[{"id":"nano","name":"Chatterbox Nano"}]}`, true},
		{`{"default_model":"nano","models":[]}`, false},
		{`{"default_model":"nano","models":[{"id":"original","name":"Original"}]}`, false},
		{`not json`, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/models" || r.Header.Get("Authorization") != "" {
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

func TestChatterboxRoutingPreservesSelectedModel(t *testing.T) {
	for _, tc := range []struct{ connection, model, want string }{
		{"nano", "original", "original"}, {"nano", "turbo", "turbo"}, {"nano", "chatterbox-nano", "nano"}, {"", "original", "original"}, {"", "gpt-4o-mini-tts", "nano"},
	} {
		t.Run(tc.connection+tc.model, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]string
				json.NewDecoder(r.Body).Decode(&body)
				got = body["model"]
				w.Header().Set("Content-Type", "audio/wav")
				w.Write(routingWave())
			}))
			defer srv.Close()
			store := &testStore{connections: map[string]ai.Connection{"nano": {ID: "nano", Type: "chatterbox", Address: srv.URL}}, routes: ai.Routes{Speech: ai.Selection{Connection: "nano"}}}
			var meta production.SpeechProvenance
			ctx := production.WithSpeechProvenance(context.Background(), func(value production.SpeechProvenance) { meta = value })
			_, err := New(store, nil).Synthesize(ctx, production.SpeechRequest{Connection: tc.connection, Model: tc.model, Text: "Welcome", Voice: "elena"})
			if err != nil || got != tc.want || meta.Model != tc.want {
				t.Fatal(got, meta, err)
			}
		})
	}
}
func routingWave() []byte {
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
