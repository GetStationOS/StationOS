package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stationos.local/stationos/internal/producer"
)

func TestLocalBudgetRetainsApprovedPlanAndDropsBulkyCatalogue(t *testing.T) {
	for _, kind := range []string{"ollama", "compatible"} {
		t.Run(kind, func(t *testing.T) {
			plan := "Velvet Notes: weekdays 08:00–22:00; Saturday Soirée 18:00–22:00; Sunday Sanctuary 10:00–14:00. Julian Vance presents."
			old := recordedResultPrefix + `{"data":{"items":[{"ID":"recording-1","Title":"` + strings.Repeat("large catalogue metadata ", 7000) + `"}],"total":100000}}`
			input := []producer.Item{item(map[string]string{"role": "user", "content": "Create an original jazz station"}), item(map[string]string{"role": "assistant", "content": old}), item(map[string]string{"role": "assistant", "content": plan}), item(map[string]string{"role": "user", "content": "Yes, please proceed"})}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), "Sunday Sanctuary") || !strings.Contains(string(raw), "Yes, please proceed") || !strings.Contains(string(raw), "recording-1") || strings.Contains(string(raw), strings.Repeat("large catalogue metadata ", 10)) {
					t.Error("compaction lost the plan/identity or retained catalogue detail")
				}
				var body struct {
					Messages []message
					Tools    json.RawMessage
				}
				json.Unmarshal(raw, &body)
				messages, _ := json.Marshal(body.Messages)
				if len(messages)+len(body.Tools) > (16384-2048)*3 {
					t.Error("dispatched oversized context")
				}
				if kind == "ollama" {
					io.WriteString(w, `{"done":true,"message":{"content":"Configuration complete"}}`)
				} else {
					io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"Configuration complete"}}]}`)
				}
			}))
			defer server.Close()
			r := router(connection(kind, server.URL))
			_, err := r.Respond(context.Background(), "local-model", "", "Full saved human brief and checklist", input, []producer.Tool{{Name: "save_station", Parameters: map[string]any{"type": "object"}}})
			if err != nil || calls != 1 {
				t.Fatal(calls, err)
			}
		})
	}
}
func TestCompactionPreservesSummaryAndUnansweredToolCalls(t *testing.T) {
	summary := recordedResultPrefix + `{"data":{"catalogue_summary":true,"Tracks":100000,"TopGenres":[{"name":"Jazz","tracks":62000}]}}`
	call := wireCall{ID: "pending"}
	call.Function.Name = "save_station"
	input := []message{{Role: "system", Content: "Original requirements"}, {Role: "assistant", Content: summary}, {Role: "assistant", ToolCalls: []wireCall{call}}}
	out := compactAssistantContext(input, 10)
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), "62000") || !strings.Contains(string(raw), "pending") || len(out) != 3 {
		t.Fatal("essential content was silently dropped", string(raw))
	}
}

func TestMaximumOutputSettingAdaptsWithoutDroppingRequirements(t *testing.T) {
	for _, kind := range []string{"ollama", "compatible", "anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			brief := "Full human brief: " + strings.Repeat("Keep every requested programme and presenter. ", 650)
			sent := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent++
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), brief) {
					t.Error("requirements were trimmed")
				}
				var body map[string]json.RawMessage
				json.Unmarshal(raw, &body)
				var output int
				switch kind {
				case "ollama":
					var options struct {
						Output int `json:"num_predict"`
					}
					json.Unmarshal(body["options"], &options)
					output = options.Output
				case "compatible", "anthropic":
					json.Unmarshal(body["max_tokens"], &output)
				case "gemini":
					var options struct {
						Output int `json:"maxOutputTokens"`
					}
					json.Unmarshal(body["generationConfig"], &options)
					output = options.Output
				}
				if output < 256 || output >= 8192 {
					t.Errorf("output ceiling not adapted: %d", output)
				}
				switch kind {
				case "ollama":
					io.WriteString(w, `{"done":true,"message":{"content":"Next small step"}}`)
				case "compatible":
					io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"Next small step"}}]}`)
				case "anthropic":
					io.WriteString(w, `{"stop_reason":"end_turn","content":[{"type":"text","text":"Next small step"}]}`)
				case "gemini":
					io.WriteString(w, `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"Next small step"}]}}]}`)
				}
			}))
			defer server.Close()
			conn := connection(kind, server.URL)
			conn.OutputTokens = 8192
			_, err := NewClient().chat(context.Background(), conn, "", "model", brief, nil, nil, nil, false)
			if err != nil || sent != 1 || conn.OutputTokens != 8192 {
				t.Fatal("max-output request failed or settings mutated", sent, err)
			}
		})
	}
}

func TestEssentialContextOverflowNeverDispatchesOrDropsHumanRequest(t *testing.T) {
	sent := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sent++ }))
	defer server.Close()
	conn := connection("ollama", server.URL)
	conn.OutputTokens = 8192
	_, err := NewClient().chat(context.Background(), conn, "", "model", strings.Repeat("Human requirements ", 4000), nil, nil, nil, false)
	if err == nil || sent != 0 {
		t.Fatal("oversized essential request dispatched", sent, err)
	}
}
