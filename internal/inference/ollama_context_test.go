package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"stationos.local/stationos/internal/producer"
)

func TestOllamaCompletedReplyAboveReservedInputBudgetIsAccepted(t *testing.T) {
	for _, input := range []int{8191, 8192, 8953, 12000} {
		t.Run(fmt.Sprint(input), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				for _, key := range []string{"truncate", "shift"} {
					if value, ok := body[key]; !ok || value != false {
						t.Errorf("unsafe %s policy: %v", key, body[key])
					}
				}
				fmt.Fprintf(w, `{"done":true,"done_reason":"stop","prompt_eval_count":%d,"eval_count":457,"message":{"tool_calls":[{"function":{"name":"select_work_step","arguments":{"area":"schedule","station_name":"The Spectrum"}}}]}}`, input)
			}))
			defer server.Close()
			conn := connection("ollama", server.URL)
			conn.ContextTokens = 16384
			conn.OutputTokens = 8192
			result, err := NewClient().chat(context.Background(), conn, "", "gemma4:12b", "Saved complete human request", nil, nil, nil, false)
			if err != nil || len(result.Calls) != 1 || result.Usage.Input != input || result.Usage.Output != 457 {
				t.Fatal("valid completed response rejected", result, err)
			}
		})
	}
}

func TestOllamaFullWindowReplyDiscardsCallsAndRetainsUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"done":true,"done_reason":"stop","prompt_eval_count":16384,"eval_count":457,"message":{"content":"Must not accept","tool_calls":[{"function":{"name":"save_station","arguments":{"name":"Must not execute"}}}]}}`)
	}))
	defer server.Close()
	conn := connection("ollama", server.URL)
	conn.ContextTokens = 16384
	conn.OutputTokens = 8192
	result, err := NewClient().chat(context.Background(), conn, "", "m", "prompt", nil, nil, nil, false)
	if !errors.Is(err, producer.ErrInputContextLimit) || len(result.Calls) != 0 || len(result.Items) != 0 || result.Text != "" || result.Usage.Input != 16384 || result.Usage.Output != 457 {
		t.Fatal("unsafe full-window reply exposed", result, err)
	}
}

func TestOllamaExplicitInputRejectionIsTypedForRecovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		io.WriteString(w, `{"error":"input exceeds context"}`)
	}))
	defer server.Close()
	_, err := NewClient().chat(context.Background(), connection("ollama", server.URL), "", "m", "prompt", nil, nil, nil, false)
	if !errors.Is(err, producer.ErrInputContextLimit) {
		t.Fatal(err)
	}
}

func TestOllamaCompletedEmptyResponseRetainsUsageForBoundedRecovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"done":true,"done_reason":"stop","prompt_eval_count":8273,"eval_count":214,"message":{"content":"","thinking":"Private model text"}}`)
	}))
	defer server.Close()
	result, err := NewClient().chat(context.Background(), connection("ollama", server.URL), "", "gemma4:12b", "prompt", nil, nil, nil, false)
	if !errors.Is(err, producer.ErrEmptyResponse) || result.Usage.Input != 8273 || result.Usage.Output != 214 || len(result.Calls) != 0 || len(result.Items) != 0 || result.Text != "" || result.Reasoning != "" {
		t.Fatal("empty completion exposed output or lost usage", result, err)
	}
}
