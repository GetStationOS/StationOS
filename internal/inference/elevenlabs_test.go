package inference

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/production"
)

func TestElevenLabsDiscoveryAndSpeech(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") != "secret" || r.Header.Get("Authorization") != "" {
			t.Error("wrong authentication")
		}
		switch r.URL.Path {
		case "/v1/models":
			w.Write([]byte(`[{"model_id":"eleven_multilingual_v2","can_do_text_to_speech":true},{"model_id":"voice_conversion","can_do_text_to_speech":false}]`))
		case "/v2/voices":
			if r.URL.Query().Get("voice_type") != "saved" {
				t.Error("must discover the account saved voices")
			}
			if r.URL.Query().Get("next_page_token") == "" {
				w.Write([]byte(`{"voices":[{"voice_id":"designed","name":"Designed DJ"}],"has_more":true,"next_page_token":"page two"}`))
			} else {
				if r.URL.Query().Get("next_page_token") != "page two" {
					t.Error(r.URL)
				}
				w.Write([]byte(`{"voices":[{"voice_id":"second","name":"Second"}],"has_more":false}`))
			}
		case "/v1/text-to-speech/designed":
			posts++
			if r.Method != "POST" || r.URL.Query().Get("output_format") != "mp3_44100_128" {
				t.Error(r.Method, r.URL)
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 2 || body["text"] != "Hello" || body["model_id"] != "eleven_multilingual_v2" {
				t.Error(body)
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Write([]byte("ID3sample"))
		default:
			t.Error(r.URL)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := NewClient()
	conn := ai.Connection{ID: "eleven", Type: "elevenlabs", Address: srv.URL}
	check, err := c.Check(context.Background(), conn, "secret", "eleven_multilingual_v2", true)
	if err != nil || !check.ModelAvailable || check.Tools || len(check.Models) != 1 {
		t.Fatal(check, err)
	}
	voices, err := c.elevenVoices(context.Background(), conn, "secret")
	if err != nil || len(voices.Voices) != 2 || voices.Voices[0].ID != "designed" {
		t.Fatal(voices, err)
	}
	if posts != 0 {
		t.Fatal("discovery generated paid speech")
	}
	audio, err := c.elevenSpeech(context.Background(), conn, "secret", production.SpeechRequest{Text: "Hello", Voice: "designed", Model: "eleven_multilingual_v2", Speed: 1.4, Direction: "OpenAI only"})
	if err != nil || string(audio) != "ID3sample" || posts != 1 {
		t.Fatal(string(audio), posts, err)
	}
}
func TestElevenLabsFailuresAreBoundedAndRedacted(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		media, body string
	}{
		{"unauthorised", 401, "application/json", "secret provider details"},
		{"rate", 429, "application/json", "secret provider details"},
		{"wrong media", 200, "application/json", "secret provider details"},
		{"empty", 200, "audio/mpeg", ""},
		{"bad audio", 200, "audio/mpeg", "not mp3"},
		{"oversized", 200, "audio/mpeg", "ID3" + strings.Repeat("x", 16<<20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", tc.media)
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := NewClient().elevenSpeech(context.Background(), ai.Connection{Address: srv.URL}, "secret", production.SpeechRequest{Text: "Hello", Voice: "designed", Model: "eleven_multilingual_v2"})
			if err == nil || strings.Contains(err.Error(), "secret") || calls != 1 {
				t.Fatal(err, calls)
			}
			if tc.status == 429 && !errors.Is(err, ai.ErrRateLimit) {
				t.Fatal(err)
			}
		})
	}
}

func TestElevenLabsRouterUsesDJConnectionAndKey(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/voices" {
			w.Write([]byte(`{"voices":[{"voice_id":"designed","name":"Elena Vance"}],"has_more":false}`))
			return
		}
		calls++
		if r.Header.Get("xi-api-key") != "eleven-secret" || r.Header.Get("Authorization") != "" {
			t.Error("credential crossed providers")
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write([]byte("ID3audio"))
	}))
	defer srv.Close()
	store := &testStore{connections: map[string]ai.Connection{"openai": {ID: "openai", Type: "openai"}, "eleven": {ID: "eleven", Type: "elevenlabs", Address: srv.URL}}, keys: map[string]string{"openai": "openai-secret", "eleven": "eleven-secret"}, routes: ai.Routes{Speech: ai.Selection{Connection: "openai"}}}
	r := New(store, nil)
	var meta production.SpeechProvenance
	ctx := production.WithSpeechProvenance(context.Background(), func(value production.SpeechProvenance) { meta = value })
	audio, err := r.Synthesize(ctx, production.SpeechRequest{Connection: "eleven", Text: "Hello", Voice: "designed", Model: "eleven_multilingual_v2"})
	if err != nil || string(audio) != "ID3audio" || calls != 1 || meta.Voice != "designed" || meta.VoiceName != "Elena Vance" {
		t.Fatal(err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = r.Synthesize(ctx, production.SpeechRequest{Connection: "eleven", Text: "Hello", Voice: "designed", Model: "eleven_multilingual_v2"})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal(err, calls)
	}
}

func TestElevenLabsVoiceNameFailureDoesNotBlockSpeech(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/voices" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path != "/v1/text-to-speech/designed" {
			t.Error(r.URL.Path)
		}
		posts++
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write([]byte("ID3audio"))
	}))
	defer srv.Close()
	store := &testStore{connections: map[string]ai.Connection{"eleven": {ID: "eleven", Type: "elevenlabs", Address: srv.URL}}, keys: map[string]string{"eleven": "secret"}}
	var meta production.SpeechProvenance
	ctx := production.WithSpeechProvenance(context.Background(), func(value production.SpeechProvenance) { meta = value })
	audio, err := New(store, nil).Synthesize(ctx, production.SpeechRequest{Connection: "eleven", Text: "Hello", Voice: "designed", Model: "eleven_multilingual_v2"})
	if err != nil || string(audio) != "ID3audio" || posts != 1 || meta.Voice != "designed" || meta.VoiceName != "" {
		t.Fatal(meta, posts, err)
	}
}
