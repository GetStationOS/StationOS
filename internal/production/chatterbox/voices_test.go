package chatterbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stationos.local/stationos/internal/ai"
)

func TestVoiceCatalogue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/nano/voices" || r.Header.Get("Authorization") != "" {
			t.Error(r.Method, r.URL, r.Header)
		}
		w.Write([]byte(`{"default_voice":"default","voices":[{"id":"default","name":"Default","description":"Original"},{"id":"warm","name":"Warm"}]}`))
	}))
	defer server.Close()
	catalog, err := (Provider{HTTP: NewClient()}).Voices(context.Background(), ai.Connection{Address: server.URL + "/nano/"})
	if err != nil || catalog.DefaultVoice != "default" || len(catalog.Voices) != 2 || catalog.Voices[1].ID != "warm" {
		t.Fatal(catalog, err)
	}
}
func TestInvalidVoiceCatalogues(t *testing.T) {
	for _, body := range []string{`{}`, `not json`, `{"default_voice":"missing","voices":[{"id":"real","name":"Real"}]}`, `{"default_voice":"a","voices":[{"id":"a","name":"A"},{"id":"a","name":"Again"}]}`, `{"default_voice":"a","voices":[{"id":"a","name":""}]}`, `{"default_voice":"a","voices":[{"id":"a","name":"A"},{"id":"bad\n","name":"Bad"}]}`, strings.Repeat(" ", 256<<10) + "{}"} {
		t.Run(body[:min(len(body), 50)], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer server.Close()
			if _, err := (Provider{HTTP: NewClient()}).Voices(context.Background(), ai.Connection{Address: server.URL}); err == nil {
				t.Fatal("bad catalogue accepted")
			}
		})
	}
}
func TestVoiceCatalogueUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	provider := Provider{HTTP: NewClient()}
	conn := ai.Connection{Address: server.URL}
	if _, err := provider.Voices(context.Background(), conn); err == nil {
		t.Fatal("HTTP error accepted")
	}
	server.Close()
	if _, err := provider.Voices(context.Background(), conn); err == nil {
		t.Fatal("connection failure accepted")
	}
}
func TestChosenVoiceRequest(t *testing.T) {
	for _, voice := range []string{"", "warm"} {
		t.Run(voice, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]string
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["text"] != "Hello" {
					t.Error(body)
				}
				if value, ok := body["voice"]; (voice == "" && ok) || (voice != "" && (!ok || value != voice)) {
					t.Error("wrong voice", body)
				}
				w.Header().Set("Content-Type", "audio/wav")
				w.Write(wave())
			}))
			defer server.Close()
			if _, err := (Provider{HTTP: NewClient()}).Synthesize(context.Background(), ai.Connection{Address: server.URL}, "Hello", voice); err != nil {
				t.Fatal(err)
			}
		})
	}
}
