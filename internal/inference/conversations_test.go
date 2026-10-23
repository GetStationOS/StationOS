package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/producer"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryConversations struct {
	mu     sync.Mutex
	values map[string]ai.Conversation
}

func (s *memoryConversations) LoadConversation(_ context.Context, key string) (ai.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key], nil
}
func (s *memoryConversations) SaveConversation(_ context.Context, key string, value ai.Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[string]ai.Conversation{}
	}
	s.values[key] = value
	return nil
}

type sessionCloud struct {
	inputs  [][]producer.Item
	options []ai.GenerationOptions
	fail    error
}

func (s *sessionCloud) Respond(context.Context, string, string, string, []producer.Item, []producer.Tool) (producer.Response, error) {
	panic("must use structured session path")
}
func (s *sessionCloud) RespondStructuredWithOptions(_ context.Context, _, _, _ string, input []producer.Item, _ []producer.Tool, _ json.RawMessage, options ai.GenerationOptions) (producer.Response, error) {
	s.inputs = append(s.inputs, input)
	s.options = append(s.options, options)
	if s.fail != nil {
		err := s.fail
		s.fail = nil
		return producer.Response{}, err
	}
	return producer.Response{ResponseID: fmt.Sprintf("response-%d", len(s.inputs)), Text: "Accepted DJ choice"}, nil
}
func cloudRouter(store *memoryConversations, model *sessionCloud) *Router {
	r := router(connection("compatible", "http://unused"))
	r.OpenAI = model
	r.Conversations = store
	r.Store.(*testStore).routes.Curation = ai.Selection{Connection: "openai"}
	return r
}
func TestEpisodeConversationContinuesAcrossTasksRestartAndNewEpisode(t *testing.T) {
	ctx := context.Background()
	store := &memoryConversations{}
	model := &sessionCloud{}
	r := cloudRouter(store, model)
	q := ai.TextRequest{Session: "station:show:date", Role: "curation", Model: "model", Instructions: "DJ personality; current task", Input: `{"show":"Night music","musical_direction":"Varied jazz"}`}
	if _, err := r.Generate(ctx, q); err != nil {
		t.Fatal(err)
	}
	r = cloudRouter(store, model) // process restart, same durable conversation
	q.Input = `{"current":{"Title":"Blue Moon"},"task":"spoken link"}`
	if _, err := r.Generate(ctx, q); err != nil {
		t.Fatal(err)
	}
	if model.options[1].PreviousResponseID != "response-1" || !model.options[1].StoreConversation || len(model.inputs[1]) != 1 {
		t.Fatal(model.options, model.inputs)
	}
	q.Session = "station:show:tomorrow"
	if _, err := r.Generate(ctx, q); err != nil {
		t.Fatal(err)
	}
	if model.options[2].PreviousResponseID != "" || len(model.inputs[2]) != 1 {
		t.Fatal("episodes shared context")
	}
	saved, _ := store.LoadConversation(ctx, "station:show:date")
	if len(saved.History) != 4 {
		t.Fatal(saved)
	}
}
func TestEpisodeConversationExpiredCursorReplaysPortableHistory(t *testing.T) {
	ctx := context.Background()
	store := &memoryConversations{}
	model := &sessionCloud{}
	r := cloudRouter(store, model)
	q := ai.TextRequest{Session: "episode", Role: "curation", Model: "model", Input: "Introduction"}
	r.Generate(ctx, q)
	model.fail = ai.ErrConversationUnavailable
	q.Input = "Next track"
	if _, err := r.Generate(ctx, q); err != nil {
		t.Fatal(err)
	}
	if len(model.inputs) != 3 || len(model.inputs[2]) != 3 || model.options[2].PreviousResponseID != "" {
		t.Fatal(model.inputs, model.options)
	}
	model.fail = errors.New("uncertain transport failure")
	if _, err := r.Generate(ctx, q); err == nil || len(model.inputs) != 4 {
		t.Fatal("ambiguous failure replayed")
	}
	saved, _ := store.LoadConversation(ctx, "episode")
	if saved.PreviousResponseID != "response-3" || len(saved.History) != 4 {
		t.Fatal(saved)
	}
}
func TestEpisodeConversationOtherProvidersReceiveEarlierTurns(t *testing.T) {
	for _, kind := range []string{"ollama", "compatible", "anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				raw, _ := json.Marshal(body)
				if calls == 2 && !strings.Contains(string(raw), "previous DJ introduction") {
					t.Error("lost earlier turn", string(raw))
				}
				switch kind {
				case "ollama":
					fmt.Fprint(w, `{"done":true,"message":{"role":"assistant","content":"previous DJ introduction"}}`)
				case "anthropic":
					fmt.Fprint(w, `{"stop_reason":"end_turn","content":[{"type":"text","text":"previous DJ introduction"}]}`)
				case "gemini":
					fmt.Fprint(w, `{"candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"text":"previous DJ introduction"}]}}]}`)
				default:
					fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"previous DJ introduction"}}]}`)
				}
			}))
			defer server.Close()
			r := router(connection(kind, server.URL))
			r.Conversations = &memoryConversations{}
			q := ai.TextRequest{Session: "episode", Role: "curation", Model: "model", Input: "Start the show"}
			for i := 0; i < 2; i++ {
				if _, err := r.Generate(context.Background(), q); err != nil {
					t.Fatal(err)
				}
				q.Input = "Select a track"
			}
		})
	}
}
func TestEpisodeConversationSwitchProviderPreservesMemory(t *testing.T) {
	ctx := context.Background()
	store := &memoryConversations{}
	model := &sessionCloud{}
	r := cloudRouter(store, model)
	q := ai.TextRequest{Session: "episode", Role: "curation", Model: "first-model", Input: "Introduction"}
	r.Generate(ctx, q)
	q.Model = "other-model"
	if _, err := r.Generate(ctx, q); err != nil {
		t.Fatal(err)
	}
	if model.options[1].PreviousResponseID != "" || len(model.inputs[1]) != 3 {
		t.Fatal("provider identity did not replay history")
	}
}
func TestEpisodeConversationLockCancellationAndCleanup(t *testing.T) {
	r := &Router{}
	release, err := r.lockConversation(context.Background(), "episode")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err = r.lockConversation(ctx, "episode"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	other, err := r.lockConversation(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	if len(r.sessionLocks) != 0 {
		t.Fatal("leaked episode locks")
	}
}

func TestPortableEpisodeMemoryRetainsChosenCreditsWithoutCandidateBulk(t *testing.T) {
	input := item(map[string]string{"role": "user", "content": `{"show":"Jazz","session_tool_history":[{"Tracks":[{"ID":"one","Title":"Blue Moon","Artist":"Performer","Composer":"Rodgers","Album":"Album"},{"ID":"two","Title":"Unchosen"}]}],"episode":{"Aired":[{"ID":"old"}]}}`})
	compact := string(compactEpisodeInput(input, `{"Tracks":["one"]}`))
	if !strings.Contains(compact, "Blue Moon") || !strings.Contains(compact, "Rodgers") || strings.Contains(compact, "Unchosen") || strings.Contains(compact, "session_tool_history") {
		t.Fatal(compact)
	}
}

func TestConcurrentEpisodeTasksCannotForkConversation(t *testing.T) {
	store := &memoryConversations{}
	model := &sessionCloud{}
	r := cloudRouter(store, model)
	var jobs sync.WaitGroup
	for i := 0; i < 12; i++ {
		jobs.Add(1)
		go func() {
			defer jobs.Done()
			_, err := r.Generate(context.Background(), ai.TextRequest{Session: "episode", Role: "curation", Model: "model", Input: "Prepare next DJ task"})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	jobs.Wait()
	if len(model.options) != 12 {
		t.Fatal(len(model.options))
	}
	for i, options := range model.options {
		want := ""
		if i > 0 {
			want = fmt.Sprintf("response-%d", i)
		}
		if options.PreviousResponseID != want {
			t.Fatal("forked response chain", i, options)
		}
	}
	saved, _ := store.LoadConversation(context.Background(), "episode")
	if len(saved.History) != 24 {
		t.Fatal("lost exchanges", len(saved.History))
	}
}

func TestOversizedOpenAIChainRebuildsWithoutDroppingMusicalMemory(t *testing.T) {
	store := &memoryConversations{}
	model := &sessionCloud{}
	r := cloudRouter(store, model)
	q := ai.TextRequest{Session: "episode", Role: "curation", Model: "model", Input: "Earlier song and composer"}
	if _, err := r.Generate(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	model.fail = ai.ErrContextLimit
	q.Input = "Choose another track"
	if _, err := r.Generate(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if len(model.inputs) != 3 || len(model.inputs[2]) != 3 || model.options[2].PreviousResponseID != "" {
		t.Fatal(model.inputs)
	}
	if !strings.Contains(string(model.inputs[2][0]), "Earlier song and composer") {
		t.Fatal("lost music memory")
	}
}

func TestContinuedConversationSendsOnlyNewAiredReceipts(t *testing.T) {
	store := &memoryConversations{}
	model := &sessionCloud{}
	r := cloudRouter(store, model)
	q := ai.TextRequest{Session: "episode", Role: "curation", Model: "model", Input: `{"episode":{"Aired":[{"ID":"old","Title":"Earlier work","Composer":"Writer"}]}}`}
	if _, err := r.Generate(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	q.Input = `{"episode":{"Aired":[{"ID":"old","Title":"Earlier work","Composer":"Writer"},{"ID":"new","Title":"Automatic selection"}]}}`
	if _, err := r.Generate(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	second := string(model.inputs[1][0])
	if strings.Contains(second, "Earlier work") || !strings.Contains(second, "Automatic selection") {
		t.Fatal(second)
	}
	q.Model = "different-provider-model"
	if _, err := r.Generate(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	replay := string(model.inputs[2][len(model.inputs[2])-1])
	if !strings.Contains(replay, "Earlier work") || !strings.Contains(replay, "Automatic selection") {
		t.Fatal("replay lost authoritative history", replay)
	}
}

func TestStatelessSpeechDoesNotReadReplayOrWritePlanningHistory(t *testing.T) {
	ctx := context.Background()
	store := &memoryConversations{values: map[string]ai.Conversation{"episode": {History: []json.RawMessage{json.RawMessage(`{"role":"assistant","content":"Never aired draft"}`)}}}}
	model := &sessionCloud{}
	r := cloudRouter(store, model)
	r.Store.(*testStore).routes.Writing = ai.Selection{Connection: "openai"}
	_, err := r.Generate(ctx, ai.TextRequest{Session: "episode", Role: "writing", Instructions: "Write a link", Input: `{"recent_aired_speech":["Actually aired"]}`, Options: ai.GenerationOptions{Stateless: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(model.inputs) != 1 || len(model.inputs[0]) != 1 || !strings.Contains(string(model.inputs[0][0]), "Actually aired") || strings.Contains(string(model.inputs[0][0]), "Never aired") {
		t.Fatal(model.inputs)
	}
	if model.options[0].StoreConversation || model.options[0].PreviousResponseID != "" || len(store.values["episode"].History) != 1 {
		t.Fatal("speech modified planning history")
	}
	r.Conversations = nil
	if _, err = r.Generate(ctx, ai.TextRequest{Session: "episode", Role: "writing", Input: "link", Options: ai.GenerationOptions{Stateless: true}}); err != nil {
		t.Fatal("stateless speech requires planning storage", err)
	}
}
