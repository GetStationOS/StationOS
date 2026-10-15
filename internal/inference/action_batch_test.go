package inference

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"stationos.local/stationos/internal/producer"
)

func TestProviderActionBatchLimitMatchesRunnerAndDiscardsOversizedReplies(t *testing.T) {
	for _, kind := range []string{"ollama", "compatible", "anthropic", "gemini"} {
		for _, size := range []int{9, producer.MaxToolCalls, producer.MaxToolCalls + 1} {
			t.Run(kind+"-"+strconv.Itoa(size), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls := []any{}
					blocks := []any{}
					parts := []any{}
					for i := 0; i < size; i++ {
						var args any = map[string]string{"name": "A programme pool"}
						if kind == "compatible" {
							args = `{"name":"A programme pool"}`
						}
						calls = append(calls, map[string]any{"function": map[string]any{"name": "save_playlist", "arguments": args}})
						blocks = append(blocks, map[string]any{"type": "tool_use", "id": producer.ID(), "name": "save_playlist", "input": map[string]string{"name": "A programme pool"}})
						parts = append(parts, map[string]any{"functionCall": map[string]any{"name": "save_playlist", "args": map[string]string{"name": "A programme pool"}}})
					}
					var body any
					switch kind {
					case "ollama":
						body = map[string]any{"done": true, "message": map[string]any{"tool_calls": calls}, "eval_count": 100}
					case "compatible":
						body = map[string]any{"choices": []any{map[string]any{"finish_reason": "tool_calls", "message": map[string]any{"tool_calls": calls}}}, "usage": map[string]int{"completion_tokens": 100}}
					case "anthropic":
						body = map[string]any{"stop_reason": "tool_use", "content": blocks, "usage": map[string]int{"output_tokens": 100}}
					case "gemini":
						body = map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": parts}}}, "usageMetadata": map[string]int{"candidatesTokenCount": 100}}
					}
					json.NewEncoder(w).Encode(body)
				}))
				defer server.Close()
				out, err := NewClient().chat(context.Background(), connection(kind, server.URL), "", "model", "prompt", nil, nil, nil, false)
				if size <= producer.MaxToolCalls {
					if err != nil || len(out.Calls) != size {
						t.Fatal("valid provider batch rejected", len(out.Calls), err)
					}
				} else if !errors.Is(err, producer.ErrToolBatchLimit) || len(out.Calls) != 0 || len(out.Items) != 0 || out.Text != "" || out.Usage.Output != 100 {
					t.Fatal("oversized reply not safely discarded", out, err)
				}
			})
		}
	}
}
