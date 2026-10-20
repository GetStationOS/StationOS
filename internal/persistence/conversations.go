package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"stationos.local/stationos/internal/ai"
)

// Conversations keeps provider cursors and portable episode history across restarts.
type Conversations struct{ DB *sql.DB }

func (r Conversations) LoadConversation(ctx context.Context, key string) (ai.Conversation, error) {
	var raw string
	var c ai.Conversation
	err := r.DB.QueryRowContext(ctx, `SELECT payload FROM episode_conversations WHERE session_key=?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(raw), &c)
	return c, err
}
func (r Conversations) SaveConversation(ctx context.Context, key string, c ai.Conversation) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = r.DB.ExecContext(ctx, `INSERT INTO episode_conversations(session_key,payload,updated_at) VALUES(?,?,unixepoch()) ON CONFLICT(session_key) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, key, string(raw))
	return err
}
