package inference

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadableProviderReasoning(t *testing.T) {
	for _, tc := range []struct{ kind, body string }{
		{"ollama", `{"done":true,"message":{"role":"assistant","thinking":"Readable reasoning","content":"Answer"}}`},
		{"compatible", `{"choices":[{"finish_reason":"stop","message":{"reasoning_content":"Readable reasoning","content":"Answer"}}]}`},
		{"anthropic", `{"stop_reason":"end_turn","content":[{"type":"thinking","thinking":"Readable reasoning","signature":"private-signature"},{"type":"redacted_thinking","data":"opaque"},{"type":"text","text":"Answer"}]}`},
		{"gemini", `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"thought":true,"text":"Readable reasoning","thoughtSignature":"private-signature"},{"text":"Answer"}]}}]}`},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, tc.body) }))
			defer server.Close()
			out, err := NewClient().chat(context.Background(), connection(tc.kind, server.URL), "", "model", "prompt", nil, nil, nil, false)
			if err != nil || out.Text != "Answer" || out.Reasoning != "Readable reasoning" {
				t.Fatal(out, err)
			}
		})
	}
}
