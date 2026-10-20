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
	settings, _ := store.Settings(ctx)
	settings.Routes.Speech.Connection = "nano"
	if err := store.Configure(ctx, settings, producer.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	p, err := (djs.Service{Repo: repo}).Save(ctx, djs.Profile{Name: "Voice test", Voice: "onyx"}, djs.KeyChange{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Dependencies{Voices: inference.New(store, nil), DJs: repo, Stations: st, Sources: src, Catalogue: cat, Playlists: persistence.Playlists{DB: st.DB}, Ready: st.DB.PingContext})
	if err != nil {
		t.Fatal(err)
	}
	path := "/producer/djs/" + p.ID
	w := request(h, "GET", path, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="speech_voice"`) || !strings.Contains(w.Body.String(), "Warm &lt;voice&gt;") || !strings.Contains(w.Body.String(), `name="voice" value="onyx"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	form := url.Values{"csrf": {csrf(t, h)}, "id": {p.ID}, "name": {p.Name}, "voice": {"onyx"}, "speech_connection": {"nano"}, "speech_voice": {"warm"}, "conversation_speed": {"1.25"}, "vibe": {"Warm OpenAI direction"}}
	w = request(h, "POST", "/producer/djs", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ := repo.Get(ctx, p.ID)
	if saved.Voice != "onyx" || saved.SpeechVoices["nano"] != "warm" || saved.ConversationSpeed != 1.25 {
		t.Fatal(saved)
	}
	for _, m := range []int32{1, 2} {
		mode.Store(m)
		w = request(h, "GET", path, nil, map[string]string{"HX-Request": "true"})
		if w.Code != 200 || !strings.Contains(w.Body.String(), `value="warm" selected`) || !strings.Contains(w.Body.String(), "(unavailable)") {
			t.Fatal("missing saved voice lost", w.Body.String())
		}
		form.Set("name", "Edited while unavailable")
		w = request(h, "POST", "/producer/djs", form, nil)
		if w.Code != 303 {
			t.Fatal("unrelated edit blocked", w.Code, w.Body.String())
		}
	}
	form.Set("speech_voice", "invented")
	w = request(h, "POST", "/producer/djs", form, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), `value="invented" selected`) {
		t.Fatal("invalid selection or input lost", w.Code, w.Body.String())
	}
	saved, _ = repo.Get(ctx, p.ID)
	if saved.SpeechVoices["nano"] != "warm" {
		t.Fatal("invalid selection overwritten", saved)
	}
	mode.Store(0)
	settings.Routes.Speech.Connection = "openai"
	if err = store.Configure(ctx, settings, producer.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	// A stale Chatterbox form must not overwrite voice settings after a route change.
	w = request(h, "POST", "/producer/djs", form, nil)
	if w.Code != 422 {
		t.Fatal("stale form accepted", w.Code)
	}
	w = request(h, "GET", path, nil, nil)
	if strings.Contains(w.Body.String(), `name="speech_voice"`) || !strings.Contains(w.Body.String(), `value="onyx" selected`) {
		t.Fatal("OpenAI choice lost", w.Body.String())
	}
	form.Del("speech_voice")
	form.Del("speech_connection")
	form.Set("voice", "marin")
	w = request(h, "POST", "/producer/djs", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ = repo.Get(ctx, p.ID)
	if saved.Voice != "marin" || saved.SpeechVoices["nano"] != "warm" {
		t.Fatal("provider choices crossed", saved)
	}
	settings.Routes.Speech.Connection = "nano"
	if err = store.Configure(ctx, settings, producer.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	w = request(h, "GET", path, nil, nil)
	if !strings.Contains(w.Body.String(), `value="warm" selected`) {
		t.Fatal("Chatterbox choice not restored", w.Body.String())
	}
}
