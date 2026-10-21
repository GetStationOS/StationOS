package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/inference"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

func TestDJsUseIndependentSpeechProvidersConcurrently(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo, _ := persistence.NewDJs(db, make([]byte, 32))
	store := persistence.NewProducer(repo)
	var nanoCalls, cloudCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/voices" {
			w.Write([]byte(`{"default_voice":"warm","voices":[{"id":"warm","name":"Warm"}]}`))
			return
		}
		nanoCalls.Add(1)
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/tts" || body["voice"] != "warm" || body["text"] != "Nano link" || r.Header.Get("Authorization") != "" {
			t.Error(r.URL, body, r.Header)
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(testWave())
	}))
	defer server.Close()
	for _, conn := range []ai.Connection{{ID: "cloud", Name: "Cloud", Type: "openai", Address: "https://api.openai.com/v1", ContextTokens: 8192, OutputTokens: 2048}, {ID: "nano", Name: "Nano", Type: "chatterbox", Address: server.URL, ContextTokens: 8192, OutputTokens: 2048}} {
		key := ai.KeyChange{}
		if conn.ID == "cloud" {
			key.Value = "cloud-secret"
		}
		if err = store.SaveConnection(ctx, conn, key); err != nil {
			t.Fatal(err)
		}
	}
	router := inference.New(store, nil)
	service := djs.Service{Repo: repo, Voices: router}
	cloud, err := service.Save(ctx, djs.Profile{Name: "Cloud DJ", SpeechChange: &djs.SpeechSelection{Connection: "cloud", Settings: djs.SpeechPreference{Model: "tts-1", Voice: "onyx", Speed: 1.2, Vibe: "Warm"}}}, djs.KeyChange{})
	if err != nil {
		t.Fatal(err)
	}
	nano, err := service.Save(ctx, djs.Profile{Name: "Nano DJ", SpeechChange: &djs.SpeechSelection{Connection: "nano", Settings: djs.SpeechPreference{Model: "chatterbox-nano", Voice: "warm", Speed: 1}}}, djs.KeyChange{})
	if err != nil {
		t.Fatal(err)
	}
	// A stale legacy global speech route must have no influence on either DJ.
	settings, _ := store.Settings(ctx)
	settings.Routes.Speech.Connection = "removed"
	settings.SpeechModel = "unused-model"
	if err = store.Configure(ctx, settings, producer.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	base := New(persistence.ProducerKeys{Producer: store}, nil, testAudio{})
	base.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		cloudCalls.Add(1)
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if r.Header.Get("Authorization") != "Bearer cloud-secret" || body["model"] != "tts-1" || body["voice"] != "onyx" || body["speed"] != 1.2 || body["instructions"] != "Warm" {
			t.Error(r.Header, body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("mp3"))}, nil
	})}
	router.OpenAISpeech = base
	var workers sync.WaitGroup
	for _, tc := range []struct {
		dj           djs.Profile
		text, prefix string
	}{{cloud, "Cloud link", "normal:mp3"}, {nano, "Nano link", "normal:RIFF"}} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			p := base
			p.Text = &localText{reply: tc.text}
			p.Speech = router
			var meta production.SpeechProvenance
			linkCtx := production.WithSpeechProvenance(ctx, func(value production.SpeechProvenance) { meta = value })
			out, err := p.Link(linkCtx, production.LinkRequest{Show: schedule.Occurrence{DJ: tc.dj}})
			if meta.Connection != tc.dj.SpeechConnection || meta.Model != strings.Replace(tc.dj.SpeechModel, "chatterbox-nano", "nano", 1) || meta.Voice != tc.dj.ActiveSpeech().Voice {
				t.Error("speech receipt crossed DJs", meta, tc.dj.Name)
			}
			if err != nil || !strings.HasPrefix(string(out), tc.prefix) {
				t.Error("wrong DJ speech output", tc.dj.Name, string(out), err)
			}
		}()
	}
	workers.Wait()
	if cloudCalls.Load() != 1 || nanoCalls.Load() != 1 {
		t.Fatal(cloudCalls.Load(), nanoCalls.Load())
	}
}
