package openai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOnlyReturnedReasoningSummariesAreDisplayable(t *testing.T) {
	m := Model{Client: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"reasoning","encrypted_content":"opaque-private","summary":[{"type":"summary_text","text":"Readable summary"}]},{"type":"message","content":[{"type":"output_text","text":"Answer"}]}]}`))}, nil
	})}}
	out, err := m.Respond(context.Background(), "model", "key", "", nil, nil)
	if err != nil || out.Text != "Answer" || strings.TrimSpace(out.Reasoning) != "Readable summary" {
		t.Fatal(out, err)
	}
}
