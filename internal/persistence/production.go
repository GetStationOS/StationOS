package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"stationos.local/stationos/internal/production"
	"time"
)

type PresenterAssets struct{ DB *sql.DB }

func (r PresenterAssets) Get(ctx context.Context, key string) (production.Prepared, error) {
	var p production.Prepared
	var raw string
	err := r.DB.QueryRowContext(ctx, `SELECT plan,intro,outro FROM presenter_assets WHERE cache_key=?`, key).Scan(&raw, &p.Intro, &p.Outro)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &p.Plan)
	}
	return p, err
}
func (r PresenterAssets) Put(ctx context.Context, key, stationID string, p production.Prepared) error {
	if len(p.Intro) > 1<<20 || len(p.Outro) > 1<<20 {
		return errors.New("presenter audio exceeds limit")
	}
	if p.Intro == nil {
		p.Intro = []byte{}
	}
	if p.Outro == nil {
		p.Outro = []byte{}
	}
	raw, err := json.Marshal(p.Plan)
	if err != nil {
		return err
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO presenter_assets(cache_key,station_id,plan,intro,outro,created_at) VALUES(?,?,?,?,?,?)`, key, stationID, string(raw), p.Intro, p.Outro, time.Now().Unix()); err != nil {
		return err
	}
	// Retain at most 64 prepared shows globally, bounded to 128 MiB audio payload.
	if _, err = tx.ExecContext(ctx, `DELETE FROM presenter_assets WHERE cache_key IN (SELECT cache_key FROM presenter_assets ORDER BY created_at DESC,cache_key DESC LIMIT -1 OFFSET 64)`); err != nil {
		return err
	}
	return tx.Commit()
}
