package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"stationos.local/stationos/internal/producer"
)

func TestChatTurnsContainActionsReasoningAndLatestStatus(t *testing.T) {
	h, store, _ := setupAssistant(t, "", &assistantModel{})
	ctx := context.Background()
	id := producer.ID()
	if err := store.Create(ctx, producer.Conversation{ID: id, Title: "Chronology"}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []producer.Message{
		{Role: "user", Text: "First question"},
		{Role: "reasoning", Text: "First reasoning <script>"},
		{Role: "action", Text: "First action", URL: "/producer"},
		{Role: "assistant", Text: "First answer"},
		{Role: "user", Text: "Second question"},
		{Role: "reasoning", Text: "Second reasoning"},
		{Role: "assistant", Text: "Second answer"},
	} {
		if err := store.Append(ctx, id, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{assistantURL(id), "/producer/assistant/" + id + "/messages"} {
		w := request(h, "GET", path, nil, map[string]string{"HX-Request": "true"})
		body := w.Body.String()
		if w.Code != 200 {
			t.Fatal(w.Code, body)
		}
		if strings.Count(body, `class="assistant-message assistant-user"`) != 2 || strings.Count(body, `class="assistant-message assistant-assistant"`) != 2 {
			t.Fatal("turn grouping", body)
		}
		previous := -1
		for _, value := range []string{"First question", "First reasoning &lt;script&gt;", "First action", "First answer", "Second question", "Second reasoning", "Second answer", "<strong>Finished</strong>"} {
			at := strings.Index(body, value)
			if at <= previous {
				t.Fatalf("%s out of order: %s", value, body)
			}
			previous = at
		}
		first := body[strings.Index(body, `data-assistant-turn="1"`):strings.Index(body, `data-assistant-turn="2"`)]
		if !strings.Contains(first, "First reasoning") || !strings.Contains(first, "First action") || strings.Contains(first, "Assistant status") {
			t.Fatal("content escaped its turn", first)
		}
		last := body[strings.Index(body, `data-assistant-turn="3"`):]
		if !strings.Contains(last, "Assistant status") || !strings.Contains(last, "Provider reasoning</summary>") {
			t.Fatal("latest response missing state", last)
		}
	}
}

type chatReasoningModel struct{ calls int }

func (m *chatReasoningModel) Respond(ctx context.Context, model, key, prompt string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	m.calls++
	if m.calls == 1 {
		return producer.Response{Reasoning: "Visible provider explanation", Text: "Checking the saved stations.", Calls: []producer.Call{{ID: "read-stations", Name: "list_stations", Arguments: []byte(`{}`)}}, Items: []producer.Item{[]byte(`{"type":"function_call","call_id":"read-stations","name":"list_stations","arguments":"{}"}`)}}, nil
	}
	return producer.Response{Text: "No changes were requested."}, nil
}
func TestChatHTMXSubmissionPersistsReasoningCommentaryAndHistory(t *testing.T) {
	h, store, _ := setupAssistant(t, "", &chatReasoningModel{})
	ctx := context.Background()
	if err := store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"}); err != nil {
		t.Fatal(err)
	}
	token := csrf(t, h)
	req := producer.ID()
	form := url.Values{"csrf": {token}, "request_id": {req}, "message": {"Review my stations"}}
	w := request(h, http.MethodPost, "/producer/assistant/messages", form, map[string]string{"HX-Request": "true"})
	if w.Code != 204 || !strings.Contains(w.Header().Get("HX-Location"), req) {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := store.Get(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if c.Status != "running" {
			if c.Status != "idle" {
				t.Fatal(c)
			}
			found := map[string]bool{}
			for _, message := range c.Messages {
				found[message.Role+":"+message.Text] = true
			}
			for _, value := range []string{"user:Review my stations", "reasoning:Visible provider explanation", "assistant:Checking the saved stations.", "assistant:No changes were requested."} {
				if !found[value] {
					t.Fatal("not persisted", value, c.Messages)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	for _, headers := range []map[string]string{nil, {"HX-Request": "true"}} {
		page := request(h, "GET", assistantURL(req), nil, headers)
		if page.Code != 200 || !strings.Contains(page.Body.String(), "Visible provider explanation") || !strings.Contains(page.Body.String(), "No changes were requested.") {
			t.Fatal(page.Body.String())
		}
	}
	duplicate := request(h, "POST", "/producer/assistant/messages", form, map[string]string{"HX-Request": "true"})
	if duplicate.Header().Get("HX-Location") == "" {
		t.Fatal("duplicate lost canonical navigation")
	}
	c, _ := store.Get(ctx, req)
	count := 0
	for _, m := range c.Messages {
		if m.Role == "user" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("duplicate user submission", c.Messages)
	}
	bad := request(h, "POST", "/producer/assistant/messages", url.Values{"csrf": {token}, "conversation": {req}, "request_id": {"invalid"}, "message": {"Retain this draft"}}, map[string]string{"HX-Request": "true"})
	if bad.Code != 200 || !strings.Contains(bad.Body.String(), "Retain this draft") || !strings.Contains(bad.Body.String(), `role="alert"`) {
		t.Fatal(bad.Body.String())
	}
}

func TestFirstChatValidationRetainsDraftWithoutCreatingConversation(t *testing.T) {
	h, store, _ := setupAssistant(t, "", &assistantModel{})
	response := request(h, "POST", "/producer/assistant/messages", url.Values{"csrf": {csrf(t, h)}, "request_id": {"expired"}, "message": {"Keep my first draft"}}, map[string]string{"HX-Request": "true"})
	conversations, err := store.List(context.Background(), "")
	if err != nil || len(conversations) != 0 || response.Code != 200 || !strings.Contains(response.Body.String(), "Keep my first draft") || !strings.Contains(response.Body.String(), "Your draft is retained") {
		t.Fatal(conversations, err, response.Code, response.Body.String())
	}
}

func TestChatPartsKeepSavedIdentityAcrossLiveRefreshes(t *testing.T) {
	h, store, _ := setupAssistant(t, "", &assistantModel{})
	ctx := context.Background()
	id := producer.ID()
	if err := store.Create(ctx, producer.Conversation{ID: id, Title: "Live content order"}); err != nil {
		t.Fatal(err)
	}
	messages := []producer.Message{
		{Role: "user", Text: "Find Christmas recordings"},
		{Role: "reasoning", Text: "Check the catalogue"},
		{Role: "assistant", Text: "I found an album"},
		{Role: "action", Text: "Saved the album"},
		{Role: "assistant", Text: "Now checking carols"},
		{Role: "action", Text: "Saved the carols"},
	}
	var previousIDs []int64
	for _, message := range messages {
		if err := store.Append(ctx, id, message); err != nil {
			t.Fatal(err)
		}
		conversation, err := store.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		response := request(h, "GET", "/producer/assistant/"+id+"/messages", nil, map[string]string{"HX-Request": "true"})
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		body := response.Body.String()
		position := -1
		for i, saved := range conversation.Messages {
			if saved.ID <= 0 || (i > 0 && saved.ID <= conversation.Messages[i-1].ID) {
				t.Fatal("message IDs are not chronological", conversation.Messages)
			}
			if i < len(previousIDs) && saved.ID != previousIDs[i] {
				t.Fatal("saved message identity changed")
			}
			if saved.Role == "user" {
				continue
			}
			key := fmt.Sprintf(`data-assistant-part="%d"`, saved.ID)
			at := strings.Index(body, key)
			if at <= position {
				t.Fatalf("response record %d missing or out of order: %s", saved.ID, body)
			}
			position = at
			if saved.Role == "reasoning" && !strings.Contains(body, fmt.Sprintf(`data-assistant-detail="%d"`, saved.ID)) {
				t.Fatal("reasoning identity is not stable", body)
			}
		}
		previousIDs = nil
		for _, saved := range conversation.Messages {
			previousIDs = append(previousIDs, saved.ID)
		}
	}
}
