package inference

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/producer"
)

type testStore struct {
	connections map[string]ai.Connection
	keys        map[string]string
	routes      ai.Routes
}

func (s *testStore) Connections(context.Context) ([]ai.Connection, error) { return nil, nil }
func (s *testStore) Connection(_ context.Context, id string) (ai.Connection, error) {
	if id == "" {
		id = "openai"
	}
	c, ok := s.connections[id]
	if !ok {
		return c, errors.New("missing")
	}
	return c, nil
}
func (s *testStore) Credential(_ context.Context, id string) (string, error)           { return s.keys[id], nil }
func (s *testStore) SaveConnection(context.Context, ai.Connection, ai.KeyChange) error { return nil }
func (s *testStore) DeleteConnection(context.Context, string) error                    { return nil }
func (s *testStore) Routes(context.Context) (ai.Routes, error)                         { return s.routes, nil }

type cloud struct {
	calls      int
	key, model string
}

func (c *cloud) Respond(_ context.Context, model, key, _ string, _ []producer.Item, _ []producer.Tool) (producer.Response, error) {
	c.calls++
	c.key = key
	c.model = model
	return producer.Response{Text: "cloud answer"}, nil
}
func connection(kind, address string) ai.Connection {
	return ai.Connection{ID: "local", Name: "Test", Type: kind, Address: address, ContextTokens: 16384, OutputTokens: 2048}
}
func router(c ai.Connection) *Router {
	return New(&testStore{connections: map[string]ai.Connection{"local": c, "openai": {ID: "openai", Type: "openai", HasKey: true}}, keys: map[string]string{"openai": "cloud-secret"}, routes: ai.Routes{Assistant: ai.Selection{Connection: "local"}, Curation: ai.Selection{Connection: "local"}, Audience: ai.Selection{Connection: "local"}}}, &cloud{})
}
func item(v any) producer.Item { b, _ := json.Marshal(v); return b }

func TestLocalProtocolsToolResultsAndContinuation(t *testing.T) {
	for _, kind := range []string{"ollama", "compatible", "anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			n := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" || r.Header.Get("x-goog-api-key") != "" {
					t.Error("cloud credential leaked")
				}
				raw, _ := io.ReadAll(r.Body)
				n++
				if !strings.Contains(string(raw), "original request") {
					t.Error("lost human request")
				}
				if n == 2 && !strings.Contains(string(raw), "saved entity") {
					t.Error("missing tool result")
				}
				w.Header().Set("Content-Type", "application/json")
				if n == 1 {
					switch kind {
					case "ollama":
						io.WriteString(w, `{"done":true,"message":{"role":"assistant","tool_calls":[{"function":{"name":"save_station","arguments":{"name":"Radio"}}}]}}`)
					case "compatible":
						io.WriteString(w, `{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"call-1","type":"function","function":{"name":"save_station","arguments":"{\"name\":\"Radio\"}"}}]}}]}`)
					case "anthropic":
						io.WriteString(w, `{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"call-1","name":"save_station","input":{"name":"Radio"}}]}`)
					case "gemini":
						io.WriteString(w, `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"functionCall":{"name":"save_station","args":{"name":"Radio"}}}]}}]}`)
					}
				} else {
					switch kind {
					case "ollama":
						io.WriteString(w, `{"done":true,"message":{"content":"Finished"}}`)
					case "compatible":
						io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"Finished"}}]}`)
					case "anthropic":
						io.WriteString(w, `{"stop_reason":"end_turn","content":[{"type":"text","text":"Finished"}]}`)
					case "gemini":
						io.WriteString(w, `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"Finished"}]}}]}`)
					}
				}
			}))
			defer server.Close()
			r := router(connection(kind, server.URL))
			input := []producer.Item{item(map[string]string{"role": "user", "content": "original request"})}
			tools := []producer.Tool{{Name: "save_station", Parameters: json.RawMessage(`{"type":"object"}`)}}
			first, err := r.Respond(context.Background(), "selected-model", "ignored-key", "instructions", input, tools)
			if err != nil || len(first.Calls) != 1 {
				t.Fatal(first, err)
			}
			input = append(input, first.Items...)
			input = append(input, item(map[string]string{"type": "function_call_output", "call_id": first.Calls[0].ID, "output": "saved entity"}))
			last, err := r.Respond(context.Background(), "selected-model", "ignored-key", "instructions", input, tools)
			if err != nil || last.Text != "Finished" || n != 2 {
				t.Fatal(last, err, n)
			}
		})
	}
}
func TestProviderFailureNeverUsesCloudEvenWithLegacyFallbackSettings(t *testing.T) {
	for _, status := range []int{404, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			r := router(connection("ollama", server.URL))
			store := r.Store.(*testStore)
			// Historical fields are ignored; the configured local endpoint stays selected.
			if err := json.Unmarshal([]byte(`{"Assistant":{"Connection":"local","FallbackConnection":"openai","FallbackModel":"old-cloud-model"}}`), &store.routes); err != nil {
				t.Fatal(err)
			}
			out, err := r.Respond(context.Background(), "local-model", "", "prompt", nil, nil)
			if err == nil || r.OpenAI.(*cloud).calls != 0 || out.Notice != "" {
				t.Fatal("provider failure used cloud", out, err)
			}
		})
	}
}
func TestRedirectCredentialIsolationAndCancellation(t *testing.T) {
	var reached atomic.Int32
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) }))
	defer dest.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	c := NewClient()
	_, err := c.request(context.Background(), connection("ollama", origin.URL), "private-key", "GET", "/api/tags", nil)
	if err == nil || reached.Load() != 0 || strings.Contains(err.Error(), "private-key") {
		t.Fatal("unsafe redirect", err)
	}
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	defer stalled.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = c.chat(ctx, connection("ollama", stalled.URL), "", "m", "prompt", nil, nil, nil, false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestLimitsAndStructuredOutput(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"done":true,"message":{"content":"invalid JSON"}}`)
	}))
	defer s.Close()
	r := router(connection("ollama", s.URL))
	_, err := r.Generate(context.Background(), ai.TextRequest{Role: "curation", Model: "m", Instructions: "prompt", Schema: json.RawMessage(`{"type":"object"}`)})
	if err == nil {
		t.Fatal("invalid JSON accepted")
	}
	before := calls.Load()
	_, err = r.Respond(context.Background(), "m", "", strings.Repeat("essential requirement", 10000), nil, nil)
	if err == nil || calls.Load() != before {
		t.Fatal("oversized essential request dispatched")
	}
}
func TestQueuePriorityBoundAndCancellation(t *testing.T) {
	g := &Gate{}
	release, err := g.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	bg := make(chan func(), 1)
	live := make(chan func(), 1)
	go func() {
		f, e := g.acquire(context.Background(), false)
		if e == nil {
			bg <- f
		}
	}()
	waitQueue := func(n int) {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			g.mu.Lock()
			count := len(g.live) + len(g.background)
			g.mu.Unlock()
			if count == n {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("queue not ready")
	}
	waitQueue(1)
	go func() {
		f, e := g.acquire(context.Background(), true)
		if e == nil {
			live <- f
		}
	}()
	waitQueue(2)
	release()
	select {
	case f := <-live:
		f()
	case <-time.After(time.Second):
		t.Fatal("live work not prioritized")
	}
	select {
	case f := <-bg:
		f()
	case <-time.After(time.Second):
		t.Fatal("background lost")
	}
	hold, _ := g.acquire(context.Background(), false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := g.acquire(ctx, false); done <- e }()
	waitQueue(1)
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	hold()
}
func TestDiscoveryDoesNotGenerateOrDownload(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/tags" {
			t.Error("unexpected model mutation", r.URL.Path)
		}
		io.WriteString(w, `{"models":[{"name":"gemma4:12b"}]}`)
	}))
	defer s.Close()
	v, e := NewClient().Check(context.Background(), connection("ollama", s.URL), "", "gemma4:12b", false)
	if e != nil || !v.Reachable || !v.ModelAvailable || v.Tools || v.Structured {
		t.Fatal(v, e)
	}
}

func TestAllPrivateProtocolsStructuredJSONAndCredentials(t *testing.T) {
	for _, kind := range []string{"ollama", "compatible", "anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				header := "Authorization"
				value := "Bearer connection-secret"
				if kind == "anthropic" {
					header = "x-api-key"
					value = "connection-secret"
				}
				if kind == "gemini" {
					header = "x-goog-api-key"
					value = "connection-secret"
				}
				if r.Header.Get(header) != value {
					t.Error("wrong credential header")
				}
				if kind != "compatible" && kind != "ollama" && r.Header.Get("Authorization") != "" {
					t.Error("unexpected bearer key")
				}
				raw, _ := io.ReadAll(r.Body)
				body := string(raw)
				switch kind {
				case "ollama":
					if !strings.Contains(body, `"format":{"type":"object"}`) {
						t.Error("missing native schema")
					}
					io.WriteString(w, `{"done":true,"message":{"content":"{\"ok\":true}"}}`)
				case "compatible":
					if !strings.Contains(body, "response_format") {
						t.Error("missing schema")
					}
					io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"ok\":true}"}}]}`)
				case "anthropic":
					if !strings.Contains(body, "input_schema") || !strings.Contains(body, "tool_choice") {
						t.Error("missing forced schema tool")
					}
					io.WriteString(w, `{"stop_reason":"tool_use","content":[{"type":"tool_use","name":"structured_result","id":"schema","input":{"ok":true}}]}`)
				case "gemini":
					if !strings.Contains(body, "responseJsonSchema") {
						t.Error("missing schema")
					}
					io.WriteString(w, `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"{\"ok\":true}"}]}}]}`)
				}
			}))
			defer server.Close()
			r := router(connection(kind, server.URL))
			r.Store.(*testStore).keys["local"] = "connection-secret"
			text, err := r.Generate(context.Background(), ai.TextRequest{Role: "curation", Model: "chosen-model", Input: "return JSON", Schema: json.RawMessage(`{"type":"object"}`)})
			if err != nil || text != `{"ok":true}` {
				t.Fatal(text, err)
			}
		})
	}
}
func TestContextPinningAndGeminiSignature(t *testing.T) {
	store := &testStore{routes: ai.Routes{Assistant: ai.Selection{Connection: "other"}}}
	r := New(store, &cloud{})
	ctx := ai.PinRoutes(context.Background(), ai.Routes{Assistant: ai.Selection{Connection: "original"}})
	v, err := r.routes(ctx)
	if err != nil || v.Assistant.Connection != "original" {
		t.Fatal("route changed during admitted task")
	}
	input := []producer.Item{item(map[string]string{"type": "function_call", "call_id": "call", "name": "test", "arguments": "{}", "gemini_signature": "opaque-signature"})}
	messages, err := history(input, false)
	if err != nil || messages[0].ToolCalls[0].Signature != "opaque-signature" {
		t.Fatal("lost live signature", err)
	}
}
