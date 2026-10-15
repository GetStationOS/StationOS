package persistence

import (
	"context"
	"path/filepath"
	"stationos.local/stationos/internal/producer"
	"strings"
	"testing"
)

func TestChatReasoningSurvivesReopenWithoutEnteringRecoveryPrompts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chat.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	dj, err := NewDJs(db, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := NewProducer(dj)
	id := producer.ID()
	if err = store.Create(ctx, producer.Conversation{ID: id, Title: "Saved history"}); err != nil {
		t.Fatal(err)
	}
	if err = store.Begin(ctx, id, producer.ID(), "Original request"); err != nil {
		t.Fatal(err)
	}
	if err = store.Append(ctx, id, producer.Message{Role: "reasoning", Text: "Display-only reasoning"}); err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(ctx, id, "failed", []byte(`[]`), producer.Message{Role: "assistant", Text: "Paused response"}); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dj, err = NewDJs(db, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store = NewProducer(dj)
	c, err := store.Get(ctx, id)
	if err != nil || len(c.Messages) != 3 || c.Messages[0].Role != "user" || c.Messages[1].Role != "reasoning" || c.Messages[2].Text != "Paused response" {
		t.Fatal(c, err)
	}
	if err = store.Begin(ctx, id, producer.ID(), "Continue"); err != nil {
		t.Fatal(err)
	}
	c, err = store.Get(ctx, id)
	if err != nil || strings.Contains(string(c.Transcript), "Display-only reasoning") || !strings.Contains(string(c.Transcript), "Original request") || !strings.Contains(string(c.Transcript), "Paused response") {
		t.Fatal(c, err)
	}
}
