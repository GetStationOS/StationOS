package web

import (
	"context"
	"net/url"
	"stationos.local/stationos/internal/producer"
	"strings"
	"testing"
	"time"
)

func TestAssistantStopAndContinuePreserveTheOriginalRequest(t *testing.T) {
	gate := make(chan struct{})
	h, store, _ := setupAssistant(t, "", &assistantModel{gate: gate})
	ctx := context.Background()
	_ = store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"})
	token := csrf(t, h)
	id := producer.ID()
	w := request(h, "POST", "/producer/assistant/messages", url.Values{"csrf": {token}, "message": {"Create Assistant Radio"}, "request_id": {id}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	if w = request(h, "POST", "/producer/assistant/"+id+"/stop", nil, nil); w.Code != 403 {
		t.Fatal("stop bypassed CSRF", w.Code)
	}
	if w = request(h, "POST", "/producer/assistant/"+id+"/stop", url.Values{"csrf": {token}}, nil); w.Code != 303 {
		t.Fatal(w.Code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, _ := store.Get(ctx, id)
		if c.Status == "failed" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	c, e := store.Get(ctx, id)
	if e != nil || c.Status != "failed" || len(c.Requests) != 1 || c.Requests[0] != "Create Assistant Radio" {
		t.Fatal("stop lost original brief", c, e)
	}
	poll := request(h, "GET", "/producer/assistant/"+id+"/messages", nil, nil)
	if !strings.Contains(poll.Body.String(), "Continue saved request") || !strings.Contains(poll.Body.String(), "<strong>Paused</strong>") {
		t.Fatal("continue button missing")
	}
	if w = request(h, "POST", "/producer/assistant/"+id+"/continue", nil, nil); w.Code != 403 {
		t.Fatal("continue bypassed CSRF")
	}
	close(gate)
	if w = request(h, "POST", "/producer/assistant/"+id+"/continue", url.Values{"csrf": {token}, "request_id": {producer.ID()}}, nil); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, _ = store.Get(ctx, id)
		if c.Status != "running" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if c.Status != "idle" || len(c.Requests) != 2 || c.Requests[0] != "Create Assistant Radio" {
		t.Fatal("continuation lost task", c)
	}
}
func TestAssistantSettingsRoleChoicesPreserveTheSharedKey(t *testing.T) {
	h, store, _ := setupAssistant(t, "", &assistantModel{})
	ctx := context.Background()
	_ = store.SaveSettings(ctx, "old-model", producer.KeyChange{Value: "test-key"})
	w := request(h, "POST", "/producer/assistant/settings", url.Values{"csrf": {csrf(t, h)}, "model": {"producer-choice"}, "curation_model": {"curation-choice"}, "speech_model": {"speech-choice"}, "audience_model": {"review-choice"}, "image_model": {"art-choice"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	settings, e := store.Settings(ctx)
	if e != nil || settings.Model != "producer-choice" || settings.CurationModel != "curation-choice" || settings.SpeechModel != "speech-choice" || settings.AudienceModel != "review-choice" || settings.ImageModel != "art-choice" {
		t.Fatal(settings, e)
	}
	key, e := store.APIKey(ctx)
	if e != nil || key != "test-key" {
		t.Fatal("model edit changed key", e)
	}
	w = request(h, "GET", "/producer/djs/new", nil, nil)
	if strings.Contains(w.Body.String(), `name="api_key"`) || !strings.Contains(w.Body.String(), "curation-choice") {
		t.Fatal("DJ form missed shared settings", w.Body.String())
	}
}

func TestAssistantStaleWorkerCanBeStoppedAndResumed(t *testing.T) {
	h, store, _ := setupAssistant(t, "", &assistantModel{})
	ctx := context.Background()
	id := producer.ID()
	_ = store.Create(ctx, producer.Conversation{ID: id, Title: "Interrupted request"})
	_ = store.Begin(ctx, id, producer.ID(), "Finish my station")
	_ = store.Checkpoint(ctx, id, []byte(`[]`), producer.Task{Brief: "Finish my station", Remaining: []string{"Schedule a show"}})
	poll := request(h, "GET", "/producer/assistant/"+id+"/messages", nil, nil)
	if !strings.Contains(poll.Body.String(), "Stopped unexpectedly") {
		t.Fatal(poll.Body.String())
	}
	w := request(h, "POST", "/producer/assistant/"+id+"/stop", url.Values{"csrf": {csrf(t, h)}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	c, err := store.Get(ctx, id)
	if err != nil || c.Status != "failed" || c.Task.Brief != "Finish my station" || len(c.Requests) != 1 {
		t.Fatal(c, err)
	}
	poll = request(h, "GET", "/producer/assistant/"+id+"/messages", nil, nil)
	if !strings.Contains(poll.Body.String(), "Continue saved request") {
		t.Fatal(poll.Body.String())
	}
}
func TestAssistantBlockersAreNotReportedAsFinished(t *testing.T) {
	h, store, _ := setupAssistant(t, "", &assistantModel{})
	ctx := context.Background()
	id := producer.ID()
	_ = store.Create(ctx, producer.Conversation{ID: id, Title: "Choose a source"})
	_ = store.Begin(ctx, id, producer.ID(), "Import my music")
	_ = store.Checkpoint(ctx, id, []byte(`[]`), producer.Task{Brief: "Import my music", Blockers: []string{"Which source should I import?"}})
	_ = store.Finish(ctx, id, "idle", []byte(`[]`), producer.Message{Role: "assistant", Text: "Which source should I import?"})
	poll := request(h, "GET", "/producer/assistant/"+id+"/messages", nil, nil)
	if !strings.Contains(poll.Body.String(), "<strong>Needs your input</strong>") || strings.Contains(poll.Body.String(), "<strong>Finished</strong>") {
		t.Fatal(poll.Body.String())
	}
}
