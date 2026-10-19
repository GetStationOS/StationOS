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

func TestConfigurationErrorsClassifyWithoutExposingPrivateProviderContent(t *testing.T) {
	for _, tc := range []struct{ code, param, want string }{
		{"model_not_found", "model", "not accessible"},
		{"invalid_json_schema", "text.format.schema", "structured output schema"},
		{"unsupported_parameter", "parallel_tool_calls", "parallel tool calls"},
		{"context_length_exceeded", "input", "context limit"},
		{"private-secret", "private-secret", "Check the model"},
	} {
		m := Model{Client: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
			raw, _ := json.Marshal(map[string]any{"error": map[string]string{"code": tc.code, "param": tc.param, "message": "private-secret listener text"}})
			return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		})}}
		_, err := m.Respond(context.Background(), "gpt-6-luna", "private-secret", "", nil, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "HTTP 400") || strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), "listener text") {
			t.Fatal(err)
		}
	}
}
