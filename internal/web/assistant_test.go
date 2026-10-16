package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/station"
)

type assistantModel struct {
	mu    sync.Mutex
	calls int
	gate  <-chan struct{}
}

func (m *assistantModel) Respond(ctx context.Context, model, key, instructions string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	if m.gate != nil {
		select {
		case <-m.gate:
		case <-ctx.Done():
			return producer.Response{}, ctx.Err()
		}
	}
	m.mu.Lock()
	m.calls++
	n := m.calls
	m.mu.Unlock()
	if n == 1 {
		item := json.RawMessage(`{"type":"function_call","call_id":"station","name":"save_station","arguments":"{\"name\":\"Assistant Radio\"}"}`)
		return producer.Response{Usage: producer.Usage{Input: 100, Output: 20}, Items: []producer.Item{item}, Calls: []producer.Call{{ID: "station", Name: "save_station", Arguments: []byte(`{"name":"Assistant Radio"}`)}}}, nil
	}
	return producer.Response{Text: "Created Assistant Radio.", Usage: producer.Usage{Input: 200, Output: 30}}, nil
}
func setupAssistant(t *testing.T, password string, model producer.Model) (http.Handler, producer.Store, persistence.Stations) {
	t.Helper()
	ctx := context.Background()
	db, e := persistence.Open(ctx, ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	dj, e := persistence.NewDJs(db, make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	store := persistence.NewProducer(dj)
	st := persistence.Stations{DB: db}
	src := persistence.Sources{DB: db}
	cat := persistence.Catalogue{DB: db}
	pl := persistence.Playlists{DB: db}
	shows := persistence.Schedule{DB: db}
	b := persistence.Broadcast{DB: db}
	svc, e := producer.New(ctx, store, producer.NewRegistry(producer.Dependencies{Stations: st, Sources: src, Catalogue: cat, Playlists: pl, DJs: dj, Schedule: shows, Broadcast: b}), model)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(svc.Close)
	svc.Prompts = persistence.Prompts{DB: db}
	h, e := New(Dependencies{Prompts: persistence.Prompts{DB: db}, Assistant: svc, Stations: st, Sources: src, Catalogue: cat, Playlists: pl, DJs: dj, Schedule: shows, BroadcastConfig: b, Ready: db.PingContext, Password: password, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if e != nil {
		t.Fatal(e)
	}
	return h, store, st
}
func TestAssistantProducerJourneyAndDuplicateFirstMessage(t *testing.T) {
	gate := make(chan struct{})
	h, store, stations := setupAssistant(t, "", &assistantModel{gate: gate})
	ctx := context.Background()
	token := csrf(t, h)
	home := request(h, "GET", "/producer/assistant", nil, nil)
	if home.Code != 200 || !strings.Contains(home.Body.String(), "Manage endpoints") {
		t.Fatal(home.Code, home.Body.String())
	}
	secret := "test-private-key"
	w := request(h, "POST", "/producer/assistant/settings", url.Values{"csrf": {token}, "model": {"test-model"}, "api_key": {secret}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	settings := request(h, "GET", "/producer/assistant/endpoints?connection=openai", nil, nil)
	if strings.Contains(settings.Body.String(), secret) || !strings.Contains(settings.Body.String(), "A key is saved") {
		t.Fatal("credential in settings", settings.Body.String())
	}
	req := producer.ID()
	form := url.Values{"csrf": {token}, "message": {"Create a station"}, "request_id": {req}}
	w = request(h, "POST", "/producer/assistant/messages", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	location := w.Header().Get("Location")
	duplicate := request(h, "POST", "/producer/assistant/messages", form, nil)
	if duplicate.Code != 303 || duplicate.Header().Get("Location") != location {
		t.Fatal("duplicate conversation", duplicate.Code, duplicate.Header())
	}
	list, e := store.List(ctx, "")
	if e != nil || len(list) != 1 {
		t.Fatal(list, e)
	}
	id := list[0].ID
	poll := request(h, "GET", "/producer/assistant/"+id+"/messages", nil, map[string]string{"HX-Request": "true"})
	if poll.Code != 200 || strings.Contains(poll.Body.String(), "textarea") || !strings.Contains(poll.Body.String(), "every 2s") {
		t.Fatal("poll reset composer", poll.Body.String())
	}
	if !strings.Contains(poll.Body.String(), "Running for") || !strings.Contains(poll.Body.String(), "Current activity:") {
		t.Fatal("active work has no timing", poll.Body.String())
	}
	close(gate)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, _ := store.Get(ctx, id)
		if c.Status != "running" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	c, _ := store.Get(ctx, id)
	if c.Status != "idle" {
		t.Fatal(c)
	}
	items, _ := stations.List(ctx)
	if len(items) != 1 {
		t.Fatal(items)
	}
	completed := request(h, "GET", location, nil, map[string]string{"HX-Request": "true"})
	if completed.Code != 200 || strings.Contains(completed.Body.String(), "<!doctype") || !strings.Contains(completed.Body.String(), "Created Assistant Radio.") {
		t.Fatal(completed.Body.String())
	}
	if !strings.Contains(completed.Body.String(), "<strong>Finished</strong>") || strings.Contains(completed.Body.String(), "Stop assistant") {
		t.Fatal("completion is ambiguous", completed.Body.String())
	}
	if !strings.Contains(completed.Body.String(), "AI usage this run: 300 input tokens (including cached input), 50 output tokens") {
		t.Fatal("measured usage missing from saved history", completed.Body.String())
	}
	for _, m := range c.Messages {
		if strings.Contains(m.Text, secret) {
			t.Fatal("secret in conversation")
		}
	}
	w = request(h, "POST", "/producer/assistant/messages", url.Values{"csrf": {token}, "conversation": {id}, "message": {"Keep this draft <script>"}, "request_id": {"bad"}}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Keep this draft &lt;script&gt;") {
		t.Fatal("failed input lost", w.Code, w.Body.String())
	}
}
func TestAssistantUsesExistingProducerAuthenticationAndCSRF(t *testing.T) {
	h, _, _ := setupAssistant(t, "password", &assistantModel{})
	headers := map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:password"))}
	for _, path := range []string{"/producer/assistant", "/producer/assistant/settings", "/producer/assistant/id/messages"} {
		if w := request(h, "GET", path, nil, nil); w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
	if w := request(h, "POST", "/producer/assistant/messages", url.Values{"message": {"Create station"}}, headers); w.Code != 403 {
		t.Fatal("CSRF", w.Code)
	}
	if w := request(h, "POST", "/producer/assistant/settings", url.Values{"model": {"model"}}, map[string]string{"Authorization": headers["Authorization"], "Origin": "https://elsewhere.example"}); w.Code != 403 {
		t.Fatal("origin", w.Code)
	}
	for _, path := range []string{"/mcp", "/producer/mcp", "/assistant"} {
		if w := request(h, "GET", path, nil, headers); w.Code != 404 {
			t.Fatal("external endpoint exposed", path, w.Code)
		}
	}
	if w := request(h, "GET", "/producer/assistant", nil, headers); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestUnconfiguredAssistantRetainsMessageAndStationContext(t *testing.T) {
	h, _, st := setupAssistant(t, "", &assistantModel{})
	ctx := context.Background()
	if e := st.Save(ctx, station.Station{ID: "context", Name: "Context Radio", Timezone: "UTC"}); e != nil {
		t.Fatal(e)
	}
	token := csrf(t, h)
	w := request(h, "POST", "/producer/assistant/messages", url.Values{"csrf": {token}, "station": {"context"}, "message": {"Plan my jazz shows"}, "request_id": {producer.ID()}}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Plan my jazz shows") || !strings.Contains(w.Body.String(), "Context Radio") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAssistantSettingsAcceptWrappedKeyWithoutEchoingIt(t *testing.T) {
	h, store, _ := setupAssistant(t, "", &assistantModel{})
	token := csrf(t, h)
	w := request(h, "POST", "/producer/assistant/settings", url.Values{"csrf": {token}, "model": {"test-model"}, "api_key": {" sk-proj-example-\nwrapped-key "}}, nil)
	if w.Code != 303 {
		t.Fatal("wrapped key rejected", w.Code)
	}
	key, e := store.APIKey(context.Background())
	if e != nil || key != "sk-proj-example-wrapped-key" {
		t.Fatal("wrong credential saved", e)
	}
	settings := request(h, "GET", "/producer/assistant/endpoints?connection=openai", nil, nil)
	if strings.Contains(settings.Body.String(), key) {
		t.Fatal("credential echoed in settings")
	}
	w = request(h, "POST", "/producer/assistant/settings", url.Values{"csrf": {token}, "model": {"test-model"}, "api_key": {"sk-first-key sk-second-key"}}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Paste one API key") {
		t.Fatal("multiple credentials accepted", w.Code)
	}
}
