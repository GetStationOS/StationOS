package web

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/inference"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/producer"
)

func speechDraft(t *testing.T, body string) string {
	t.Helper()
	found := regexp.MustCompile(`name="speech_draft" value="([^"]*)"`).FindStringSubmatch(body)
	if len(found) != 2 {
		t.Fatal("missing speech draft", body)
	}
	return html.UnescapeString(found[1])
}
func TestDJChatterboxVoiceFormAndPreservation(t *testing.T) {
	var mode atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/voices" {
			t.Error("voice form generated audio", r.Method, r.URL.Path)
		}
		if mode.Load() == 1 {
			w.WriteHeader(503)
			return
		}
		if mode.Load() == 2 {
			w.Write([]byte(`{"default_voice":"original","voices":[{"id":"original","name":"Original"}]}`))
			return
		}
		w.Write([]byte(`{"default_voice":"original","voices":[{"id":"original","name":"Original"},{"id":"warm","name":"Warm <voice>"}]}`))
	}))
	defer provider.Close()
	_, st, src, cat := setupWeb(t, "")
	repo, _ := persistence.NewDJs(st.DB, make([]byte, 32))
	store := persistence.NewProducer(repo)
	ctx := context.Background()
	conn := ai.Connection{ID: "nano", Name: "Nano", Type: "chatterbox", Address: provider.URL, ContextTokens: 8192, OutputTokens: 2048}
	if err := store.SaveConnection(ctx, conn, ai.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	voices := inference.New(store, nil)
	p, err := (djs.Service{Repo: repo, Voices: voices}).Save(ctx, djs.Profile{Name: "Voice test", Voice: "onyx", ConversationSpeed: 1.25, Vibe: "Warm direction", SpeechChange: &djs.SpeechSelection{Connection: "nano", Settings: djs.SpeechPreference{Model: "chatterbox-nano", Speed: 1}}}, djs.KeyChange{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Dependencies{Voices: voices, DJs: repo, Stations: st, Sources: src, Catalogue: cat, Playlists: persistence.Playlists{DB: st.DB}, Ready: st.DB.PingContext})
	if err != nil {
		t.Fatal(err)
	}
	path := "/producer/djs/" + p.ID
	w := request(h, "GET", path, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="speech_voice"`) || !strings.Contains(w.Body.String(), "Warm &lt;voice&gt;") || strings.Contains(w.Body.String(), `name="conversation_speed"`) || strings.Contains(w.Body.String(), `name="vibe"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	form := url.Values{"csrf": {csrf(t, h)}, "id": {p.ID}, "name": {p.Name}, "prompt": {"Unsaved prompt"}, "speech_config": {"yes"}, "speech_connection": {"nano"}, "speech_fields_connection": {"nano"}, "speech_kind": {"chatterbox"}, "speech_model": {"chatterbox-nano"}, "speech_voice": {"warm"}, "speech_draft": {"{}"}}
	w = request(h, "POST", "/producer/djs", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ := repo.Get(ctx, p.ID)
	if saved.SpeechConnection != "nano" || saved.Voice != "onyx" || saved.SpeechVoices["nano"] != "warm" || saved.ConversationSpeed != 1.25 {
		t.Fatal(saved)
	}
	for _, m := range []int32{1, 2} {
		mode.Store(m)
		w = request(h, "GET", path, nil, map[string]string{"HX-Request": "true"})
		if w.Code != 200 || !strings.Contains(w.Body.String(), `value="warm" selected`) || !strings.Contains(w.Body.String(), "(unavailable)") {
			t.Fatal(w.Body.String())
		}
		w = request(h, "POST", "/producer/djs", form, nil)
		if w.Code != 303 {
			t.Fatal("unrelated edit blocked", w.Code, w.Body.String())
		}
	}
	form.Set("speech_voice", "invented")
	w = request(h, "POST", "/producer/djs", form, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), `value="invented" selected`) {
		t.Fatal(w.Code, w.Body.String())
	}
	mode.Store(0)
	form.Set("speech_voice", "warm")
	settings, _ := store.Settings(ctx)
	settings.Routes.Speech.Connection = "openai"
	if err = store.Configure(ctx, settings, producer.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	w = request(h, "GET", path, nil, nil)
	if !strings.Contains(w.Body.String(), `name="speech_voice"`) {
		t.Fatal("global route changed DJ", w.Body.String())
	}
	// Changing provider previews only its fields, preserving unsaved text and settings.
	form.Set("speech_connection", "openai")
	w = request(h, "POST", "/producer/djs/speech", form, map[string]string{"HX-Request": "true", "HX-Target": "dj-speech-settings"})
	if w.Code != 200 || strings.Contains(w.Body.String(), "<!doctype") || !strings.Contains(w.Body.String(), `name="conversation_speed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	var pending map[string]djs.SpeechPreference
	if json.Unmarshal([]byte(speechDraft(t, w.Body.String())), &pending) != nil || pending["nano"].Voice != "warm" {
		t.Fatal(pending)
	}
	saved, _ = repo.Get(ctx, p.ID)
	if saved.SpeechConnection != "nano" {
		t.Fatal("preview wrote settings")
	}
	form.Set("speech_draft", speechDraft(t, w.Body.String()))
	form.Set("speech_fields_connection", "openai")
	form.Set("speech_kind", "openai")
	form.Set("speech_model", "tts-1")
	form.Set("voice", "marin")
	form.Set("conversation_speed", "1.4")
	form.Set("vibe", "New OpenAI direction")
	form.Del("speech_voice")
	w = request(h, "POST", "/producer/djs", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ = repo.Get(ctx, p.ID)
	if saved.SpeechConnection != "openai" || saved.SpeechModel != "tts-1" || saved.Voice != "marin" || saved.SpeechVoices["nano"] != "warm" {
		t.Fatal(saved)
	}
	// The normal-form fallback retains the complete page and unsaved editorial input.
	form.Set("speech_connection", "nano")
	w = request(h, "POST", "/producer/djs/speech", form, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<!doctype") || !strings.Contains(w.Body.String(), "Unsaved prompt") || !strings.Contains(w.Body.String(), `value="warm" selected`) {
		t.Fatal(w.Code, w.Body.String())
	}
	form.Set("speech_draft", speechDraft(t, w.Body.String()))
	form.Set("speech_fields_connection", "nano")
	form.Set("speech_kind", "chatterbox")
	form.Set("speech_model", "chatterbox-nano")
	form.Set("speech_voice", "warm")
	form.Del("voice")
	form.Del("conversation_speed")
	form.Del("vibe")
	w = request(h, "POST", "/producer/djs", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ = repo.Get(ctx, p.ID)
	if saved.SpeechConnection != "nano" || saved.SpeechSettings["openai"].Model != "tts-1" || saved.SpeechSettings["openai"].Voice != "marin" || saved.ConversationSpeed != 1.4 {
		t.Fatal("provider settings lost", saved)
	}
	if err = store.DeleteConnection(ctx, "nano"); err == nil {
		t.Fatal("deleted endpoint in use by DJ")
	}
}
