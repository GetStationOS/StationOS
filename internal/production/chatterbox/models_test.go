package chatterbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"stationos.local/stationos/internal/ai"
	"testing"
)

func TestServerOwnedModelsAndExactSynthesisSelection(t *testing.T) {
	for _, model := range []string{"nano", "original", "turbo", "chatterbox-nano"} {
		t.Run(model, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					w.Write([]byte(`{"default_model":"nano","models":[{"id":"nano","name":"Chatterbox Nano"},{"id":"original","name":"Chatterbox Original"},{"id":"turbo","name":"Chatterbox Turbo"}]}`))
					return
				}
				if r.URL.Path != "/tts" || r.Method != "POST" {
					t.Error(r.Method, r.URL)
				}
				var body map[string]string
				json.NewDecoder(r.Body).Decode(&body)
				want := model
				if model == Model {
					want = "nano"
				}
				if body["model"] != want || body["voice"] != "elena" {
					t.Error(body)
				}
				w.Header().Set("Content-Type", "audio/wav")
				w.Write(wave())
			}))
			defer srv.Close()
			p := Provider{HTTP: NewClient()}
			conn := ai.Connection{Address: srv.URL}
			choices, err := p.Models(context.Background(), conn)
			if err != nil || len(choices) != 3 || choices[2].ID != "turbo" {
				t.Fatal(choices, err)
			}
			_, err = p.Synthesize(context.Background(), conn, "Welcome", "elena", model)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestLegacyModelDiscoveryRequiresReadyHealth(t *testing.T) {
	for _, ready := range []bool{false, true} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/models" {
				w.WriteHeader(404)
				return
			}
			if ready {
				w.Write([]byte(`{"status":"ok","model":"chatterbox-nano"}`))
			} else {
				w.Write([]byte(`{"status":"loading","model":"chatterbox-nano"}`))
			}
		}))
		choices, err := (Provider{HTTP: NewClient()}).Models(context.Background(), ai.Connection{Address: srv.URL})
		srv.Close()
		if ready {
			if err != nil || len(choices) != 1 || choices[0].ID != "nano" {
				t.Fatal(choices, err)
			}
		} else if err == nil {
			t.Fatal("unready legacy model accepted")
		}
	}
}
