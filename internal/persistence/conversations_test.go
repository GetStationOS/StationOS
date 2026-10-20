package persistence

import (
	"context"
	"encoding/json"
	"path/filepath"
	"stationos.local/stationos/internal/ai"
	"testing"
)

func TestEpisodeConversationSurvivesDatabaseReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "station.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	repo := Conversations{DB: db}
	value := ai.Conversation{ProviderIdentity: "fingerprint", PreviousResponseID: "response-id", History: []json.RawMessage{json.RawMessage(`{"role":"assistant","content":"Show intro"}`)}}
	if err = repo.SaveConversation(ctx, "station:episode:date", value); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo.DB = db
	got, err := repo.LoadConversation(ctx, "station:episode:date")
	if err != nil || got.PreviousResponseID != value.PreviousResponseID || len(got.History) != 1 {
		t.Fatal(got, err)
	}
	other, err := repo.LoadConversation(ctx, "station:episode:tomorrow")
	if err != nil || len(other.History) != 0 {
		t.Fatal(other, err)
	}
}
