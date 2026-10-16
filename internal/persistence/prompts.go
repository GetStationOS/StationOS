package persistence

import (
	"context"
	"database/sql"
	"errors"
	"stationos.local/stationos/internal/prompts"
)

type Prompts struct{ DB *sql.DB }

func (r Prompts) Prompt(ctx context.Context, id string) (string, error) {
	if _, ok := prompts.Find(id); !ok {
		return "", prompts.ErrNotFound
	}
	var text string
	err := r.DB.QueryRowContext(ctx, `SELECT body FROM ai_prompts WHERE id=?`, id).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return prompts.Default(id)
	}
	return text, err
}
func (r Prompts) SavePrompt(ctx context.Context, id, text string) error {
	if err := prompts.Validate(id, text); err != nil {
		return err
	}
	return r.update(ctx, id, text, false)
}
func (r Prompts) ResetPrompt(ctx context.Context, id string) error {
	if _, ok := prompts.Find(id); !ok {
		return prompts.ErrNotFound
	}
	return r.update(ctx, id, "", true)
}
func (r Prompts) update(ctx context.Context, id, text string, reset bool) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if reset {
		_, err = tx.ExecContext(ctx, `DELETE FROM ai_prompts WHERE id=?`, id)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_prompts(id,body) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body`, id, text)
	}
	if err != nil {
		return err
	}
	// Scheduling snapshots carry this revision into broadcast preparation keys.
	if id == "show" || id == "background" || id == "link" || id == "audience-link" || id == "selection" {
		if _, err = tx.ExecContext(ctx, `UPDATE producer_settings SET ai_revision=ai_revision+1 WHERE id=1`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
