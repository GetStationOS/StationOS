package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"stationos.local/stationos/internal/producer"
)

type transport func(*http.Request) (*http.Response, error)

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) { return t(r) }
func TestResponsesContractAndToolCalls(t *testing.T) {
	m := Model{Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		if body["store"] != false || body["parallel_tool_calls"] != false || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal(body)
		}
		functions := body["tools"].([]any)
		if functions[0].(map[string]any)["type"] != "function" {
			t.Fatal(functions)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"reasoning","id":"reason"},{"type":"function_call","call_id":"one","name":"list_stations","arguments":"{}"}]}`))}, nil
	})}}
	v, e := m.Respond(context.Background(), "test-model", "secret", "Instructions", []producer.Item{json.RawMessage(`{"role":"user","content":"List stations"}`)}, []producer.Tool{{Name: "list_stations", Parameters: map[string]any{"type": "object"}}})
	if e != nil || len(v.Calls) != 1 || len(v.Items) != 2 || v.Calls[0].ID != "one" {
		t.Fatal(v, e)
	}
}
func TestProviderFailureDoesNotExposeBodyOrCredential(t *testing.T) {
	m := Model{Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("secret-key private body"))}, nil
	})}}
	_, e := m.Respond(context.Background(), "model", "secret-key", "", nil, nil)
	if e == nil || strings.Contains(e.Error(), "secret-key") {
		t.Fatal(e)
	}
}
