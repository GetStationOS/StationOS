package producer_test

import (
	"context"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/producer"
	"strings"
	"testing"
	"time"
)

type promptCapture struct{ got chan string }

func (m promptCapture) Respond(_ context.Context, _, _, instructions string, _ []producer.Item, _ []producer.Tool) (producer.Response, error) {
	m.got <- instructions
	return producer.Response{Text: "Finished"}, nil
}
func TestAssistantUsesSavedPromptWithRuntimeContext(t *testing.T) {
	store, registry, st, _, _, _ := fixture(t)
	ctx := context.Background()
	p := persistence.Prompts{DB: st.DB}
	if err := p.SavePrompt(ctx, "assistant", "CUSTOM ASSISTANT INSTRUCTIONS"); err != nil {
		t.Fatal(err)
	}
	settings, err := store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.Model = "gpt-6-luna"
	if err := store.Configure(ctx, settings, producer.KeyChange{Value: "key"}); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	s, err := producer.New(ctx, store, registry, promptCapture{got})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Prompts = p
	id, err := s.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(ctx, id, producer.ID(), "Please explain my stations"); err != nil {
		t.Fatal(err)
	}
	select {
	case prompt := <-got:
		if !strings.HasPrefix(prompt, "CUSTOM ASSISTANT INSTRUCTIONS") || !strings.Contains(prompt, "Please explain my stations") {
			t.Fatal(prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("assistant did not call model")
	}
}
