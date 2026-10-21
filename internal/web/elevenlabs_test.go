package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/inference"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/producer"
)

type elevenTransport struct{ base *url.URL }

func (v elevenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	u := *r.URL
	u.Scheme = v.base.Scheme
	u.Host = v.base.Host
	r.URL = &u
	return http.DefaultTransport.RoundTrip(r)
}
func TestElevenLabsDJFormSaveAndProviderSwitch(t *testing.T) {
	var failed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected generation", r.URL)
		}
		if r.Header.Get("xi-api-key") != "secret" {
			t.Error("credential missing")
		}
		if failed.Load() {
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`[{"model_id":"eleven_multilingual_v2","can_do_text_to_speech":true}]`))
			return
		}
		if r.URL.Path != "/v2/voices" {
			t.Error(r.URL)
		}
		w.Write([]byte(`{"voices":[{"voice_id":"designed","name":"Designed <DJ>"}],"has_more":false}`))
	}))
	defer srv.Close()
	_, st, src, cat := setupWeb(t, "")
	repo, _ := persistence.NewDJs(st.DB, make([]byte, 32))
	store := persistence.NewProducer(repo)
	ctx := context.Background()
	conn := ai.Connection{ID: "eleven", Name: "ElevenLabs", Type: "elevenlabs", Address: "https://api.elevenlabs.io", ContextTokens: 8192, OutputTokens: 2048}
	if err := store.SaveConnection(ctx, conn, ai.KeyChange{Value: "secret"}); err != nil {
		t.Fatal(err)
	}
	router := inference.New(store, nil)
	base, _ := url.Parse(srv.URL)
	router.Local.HTTP.Transport = elevenTransport{base}
	service := djs.Service{Repo: repo, Voices: router}
	dj, err := service.Save(ctx, djs.Profile{Name: "Eleven DJ", Voice: "onyx", ConversationSpeed: 1.25, Vibe: "Warm", SpeechChange: &djs.SpeechSelection{Connection: "eleven", Settings: djs.SpeechPreference{Model: "eleven_multilingual_v2", Voice: "designed", Speed: 1}}}, djs.KeyChange{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Dependencies{Assistant: &producer.Service{Store: store, Model: router}, Voices: router, DJs: repo, Stations: st, Sources: src, Catalogue: cat, Playlists: persistence.Playlists{DB: st.DB}, Ready: st.DB.PingContext})
	if err != nil {
		t.Fatal(err)
	}
	endpointForm := url.Values{"csrf": {csrf(t, h)}, "connection_action": {"save"}, "connection_id": {"eleven"}, "connection_name": {"ElevenLabs"}, "provider_type": {"elevenlabs"}, "address": {"https://api.elevenlabs.io"}, "context_tokens": {"8192"}, "output_tokens": {"2048"}}
	endpointResponse := request(h, "POST", "/producer/assistant/endpoints", endpointForm, nil)
	if endpointResponse.Code != 303 {
		t.Fatal(endpointResponse.Code, endpointResponse.Body.String())
	}
	if key, _ := store.Credential(ctx, "eleven"); key != "secret" {
		t.Fatal("credential was not preserved")
	}
	endpointResponse = request(h, "GET", "/producer/assistant/endpoints?connection=eleven", nil, nil)
	if endpointResponse.Code != 200 || strings.Contains(endpointResponse.Body.String(), "secret") || !strings.Contains(endpointResponse.Body.String(), "ElevenLabs API key") {
		t.Fatal(endpointResponse.Code, endpointResponse.Body.String())
	}
	path := "/producer/djs/" + dj.ID
	w := request(h, "GET", path, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Designed &lt;DJ&gt;") || !strings.Contains(w.Body.String(), `value="eleven_multilingual_v2"`) || strings.Contains(w.Body.String(), `name="vibe"`) || strings.Contains(w.Body.String(), `name="conversation_speed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	form := url.Values{"csrf": {csrf(t, h)}, "id": {dj.ID}, "name": {dj.Name}, "prompt": {"Editorial draft"}, "speech_config": {"yes"}, "speech_connection": {"openai"}, "speech_fields_connection": {"eleven"}, "speech_kind": {"elevenlabs"}, "speech_model": {"eleven_multilingual_v2"}, "speech_voice": {"designed"}, "speech_draft": {"{}"}}
	w = request(h, "POST", "/producer/djs/speech", form, map[string]string{"HX-Request": "true", "HX-Target": "dj-speech-settings"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="vibe"`) || !strings.Contains(speechDraft(t, w.Body.String()), "designed") || !strings.Contains(w.Body.String(), `name="speech_model" value="gpt-4o-mini-tts"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	fallback := request(h, "POST", "/producer/djs/speech", form, nil)
	if fallback.Code != 200 || !strings.Contains(fallback.Body.String(), "Editorial draft") || !strings.Contains(fallback.Body.String(), `name="speech_model" value="gpt-4o-mini-tts"`) {
		t.Fatal(fallback.Code, fallback.Body.String())
	}
	form.Set("speech_connection", "eleven")
	w = request(h, "POST", "/producer/djs", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ := repo.Get(ctx, dj.ID)
	if saved.ActiveSpeech().Voice != "designed" || saved.Voice != "onyx" || saved.Vibe != "Warm" {
		t.Fatal(saved)
	}
	failed.Store(true)
	w = request(h, "GET", path, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `value="designed" selected`) || !strings.Contains(w.Body.String(), "(unavailable)") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/producer/djs", form, nil)
	if w.Code != 303 {
		t.Fatal("unrelated edit blocked", w.Code, w.Body.String())
	}
	form.Set("speech_voice", "invented")
	w = request(h, "POST", "/producer/djs", form, nil)
	if w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := store.DeleteConnection(ctx, "eleven"); err == nil {
		t.Fatal("in-use endpoint removed")
	}
	conn.Type = "chatterbox"
	if err := store.SaveConnection(ctx, conn, ai.KeyChange{}); err == nil {
		t.Fatal("in-use provider changed")
	}
}
