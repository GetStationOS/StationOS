package inference

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"stationos.local/stationos/internal/producer"
)

func TestTruncatedProviderRepliesDiscardEveryToolAndPreserveUsage(t *testing.T) {
	for kind, body := range map[string]string{
		"ollama":     `{"done":true,"done_reason":"length","prompt_eval_count":100,"eval_count":2048,"message":{"content":"private unfinished output","tool_calls":[{"function":{"name":"save_station","arguments":{"name":"Must not execute"}}}]}}`,
		"compatible": `{"usage":{"prompt_tokens":100,"completion_tokens":2048},"choices":[{"finish_reason":"length","message":{"content":"private unfinished output","tool_calls":[{"id":"partial","function":{"name":"save_station","arguments":"{\"name\":\"Must not execute\"}"}}]}}]}`,
		"anthropic":  `{"stop_reason":"max_tokens","usage":{"input_tokens":100,"output_tokens":2048},"content":[{"type":"tool_use","id":"partial","name":"save_station","input":{"name":"Must not execute"}}]}`,
		"gemini":     `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":2048},"candidates":[{"finishReason":"MAX_TOKENS","content":{"parts":[{"functionCall":{"name":"save_station","args":{"name":"Must not execute"}}}]}}]}`,
	} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
			defer server.Close()
			out, err := NewClient().chat(context.Background(), connection(kind, server.URL), "", "model", "prompt", nil, nil, nil, false)
			if !errors.Is(err, producer.ErrOutputLimit) || len(out.Calls) != 0 || len(out.Items) != 0 || out.Text != "" || out.Usage.Input != 100 || out.Usage.Output != 2048 {
				t.Fatal("unsafe incomplete response", out, err)
			}
		})
	}
}
