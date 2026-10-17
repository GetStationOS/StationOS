package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"stationos.local/stationos/internal/playlists"
)

type Playlists struct{ DB *sql.DB }

var _ playlists.Repository = Playlists{}

func (r Playlists) Save(ctx context.Context, p playlists.Playlist) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.ID == "" {
		return playlists.ErrInvalid
	}
	res, err := r.DB.ExecContext(ctx, `INSERT INTO playlists(id,station_id,name,description) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description WHERE playlists.station_id=excluded.station_id`, p.ID, p.StationID, p.Name, p.Description)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return playlists.ErrInvalid
	}
	return err
}
func scanPlaylist(row interface{ Scan(...any) error }) (playlists.Playlist, error) {
	var p playlists.Playlist
	err := row.Scan(&p.ID, &p.StationID, &p.Name, &p.Description)
	if errors.Is(err, sql.ErrNoRows) {
		err = playlists.ErrNotFound
	}
	return p, err
}
func (r Playlists) Get(ctx context.Context, id string) (playlists.Playlist, error) {
	return scanPlaylist(r.DB.QueryRowContext(ctx, `SELECT id,station_id,name,description FROM playlists WHERE id=?`, id))
}
func (r Playlists) List(ctx context.Context, stationID string) ([]playlists.Playlist, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id,station_id,name,description FROM playlists WHERE (?='' OR station_id=?) ORDER BY lower(name),id`, stationID, stationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []playlists.Playlist{}
	for rows.Next() {
		p, err := scanPlaylist(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r Playlists) Rules(ctx context.Context, id string) ([]playlists.Rule, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id,kind,value,source_id FROM playlist_rules WHERE playlist_id=? ORDER BY kind,lower(value),id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []playlists.Rule{}
	for rows.Next() {
		var rule playlists.Rule
		if err = rows.Scan(&rule.ID, &rule.Kind, &rule.Value, &rule.SourceID); err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}
func (r Playlists) AddRule(ctx context.Context, id string, rule playlists.Rule) error {
	if err := rule.Validate(); err != nil {
		return err
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if rule.Kind == "album" {
		var exists int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalogue_items WHERE source_id=? AND album=?`, rule.SourceID, rule.Value).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return playlists.ErrUnavailable
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO playlist_rules(id,playlist_id,kind,value,source_id) VALUES(?,?,?,?,?) ON CONFLICT(playlist_id,kind,value,source_id) DO NOTHING`, rule.ID, id, rule.Kind, rule.Value, rule.SourceID); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM playlist_rules WHERE playlist_id=?`, id).Scan(&count); err != nil {
		return err
	}
	if count > playlists.MaxRules {
		return playlists.ErrInvalid
	}
	if err = resetPlaylistBaseline(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (r Playlists) RemoveRule(ctx context.Context, id, ruleID string) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM playlist_rules WHERE playlist_id=? AND id=?`, id, ruleID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return playlists.ErrNotFound
	}
	if err = resetPlaylistBaseline(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}
func requirePlaylist(ctx context.Context, tx *sql.Tx, id string) error {
	var found string
	err := tx.QueryRowContext(ctx, `SELECT id FROM playlists WHERE id=?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return playlists.ErrNotFound
	}
	return err
}
func (r Playlists) SetIncluded(ctx context.Context, id string, ids []string, included bool) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = requirePlaylist(ctx, tx, id); err != nil {
		return err
	}
	for _, contentID := range ids {
		var snapshot string
		err = tx.QueryRowContext(ctx, `SELECT metadata FROM catalogue_items WHERE id=?`, contentID).Scan(&snapshot)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRowContext(ctx, `SELECT snapshot FROM playlist_selections WHERE playlist_id=? AND content_id=?`, id, contentID).Scan(&snapshot)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return playlists.ErrUnavailable
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO playlist_selections(playlist_id,content_id,included,snapshot) VALUES(?,?,?,?) ON CONFLICT(playlist_id,content_id) DO UPDATE SET included=excluded.included,snapshot=excluded.snapshot`, id, contentID, included, snapshot); err != nil {
			return err
		}
	}
	if err = resetPlaylistBaseline(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Rules are a union, explicit exclusions win, and manual choices remain visible
// when the rebuildable index loses an item. Title/ID sorting is for stable browsing only.
func resolvePlaylist(ctx context.Context, tx *sql.Tx, id string) (playlists.Result, error) {
	out := playlists.Result{Entries: []playlists.Entry{}}
	if err := requirePlaylist(ctx, tx, id); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `WITH candidates AS (
 SELECT i.id,i.metadata,1 AS available FROM catalogue_items i
 WHERE (EXISTS(SELECT 1 FROM playlist_selections p WHERE p.playlist_id=? AND p.content_id=i.id AND p.included=1)
 OR EXISTS(SELECT 1 FROM playlist_rules r WHERE r.playlist_id=? AND ((r.kind='genre' AND r.value=json_extract(i.metadata,'$.genre')) OR (r.kind='album' AND r.value=i.album AND r.source_id=i.source_id))))
 AND NOT EXISTS(SELECT 1 FROM playlist_selections x WHERE x.playlist_id=? AND x.content_id=i.id AND x.included=0)
 UNION ALL SELECT p.content_id,p.snapshot,0 FROM playlist_selections p LEFT JOIN catalogue_items i ON i.id=p.content_id WHERE p.playlist_id=? AND p.included=1 AND i.id IS NULL
 ) SELECT c.metadata,c.available FROM candidates c
 ORDER BY lower(json_extract(c.metadata,'$.title')),c.id LIMIT ?`, id, id, id, id, playlists.MaxItems+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var entry playlists.Entry
		if err = rows.Scan(&raw, &entry.Available); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &entry.Item); err != nil {
			return out, err
		}
		out.Entries = append(out.Entries, entry)
		out.Total++
		if !entry.Available {
			out.Unavailable++
		}
		if entry.Item.Duration == nil {
			out.UnknownDurations++
		} else {
			out.Duration += time.Duration(*entry.Item.Duration)
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if out.Total > playlists.MaxItems {
		return out, playlists.ErrTooLarge
	}
	return out, nil
}
func (r Playlists) Resolve(ctx context.Context, id string, offset, limit int) (playlists.Result, error) {
	if offset < 0 || offset > 1000000 || limit < 1 || limit > 100 {
		return playlists.Result{}, playlists.ErrInvalid
	}
	tx, err := r.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return playlists.Result{}, err
	}
	defer tx.Rollback()
	out, err := resolvePlaylist(ctx, tx, id)
	if err != nil {
		return out, err
	}
	start := min(offset, out.Total)
	end := min(start+limit, out.Total)
	out.Entries = out.Entries[start:end]
	return out, tx.Commit()
}

// Rule replacement never changes manual inclusions or exclusions.
func (r Playlists) ReplaceRules(ctx context.Context, id string, rules []playlists.Rule) error {
	_, err := r.ReplaceRulesAndRefresh(ctx, id, rules)
	return err
}
func (r Playlists) ReplaceRulesAndRefresh(ctx context.Context, id string, rules []playlists.Rule) (playlists.RefreshResult, error) {
	var report playlists.RefreshResult
	if len(rules) > playlists.MaxRules {
		return report, playlists.ErrInvalid
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	if err = requirePlaylist(ctx, tx, id); err != nil {
		return report, err
	}
	// Preserve previously chosen albums even when temporarily absent from the index.
	existing := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT source_id,value FROM playlist_rules WHERE playlist_id=? AND kind='album'`, id)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var source, value string
		if err = rows.Scan(&source, &value); err != nil {
			rows.Close()
			return report, err
		}
		existing[source+"\x00"+value] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return report, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM playlist_rules WHERE playlist_id=?`, id); err != nil {
		return report, err
	}
	for _, rule := range rules {
		if err = rule.Validate(); err != nil {
			return report, err
		}
		if rule.ID == "" {
			return report, playlists.ErrInvalid
		}
		if rule.Kind == "album" && !existing[rule.SourceID+"\x00"+rule.Value] {
			var count int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalogue_items WHERE source_id=? AND album=?`, rule.SourceID, rule.Value).Scan(&count); err != nil {
				return report, err
			}
			if count == 0 {
				return report, playlists.ErrUnavailable
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO playlist_rules(id,playlist_id,kind,value,source_id) VALUES(?,?,?,?,?) ON CONFLICT(playlist_id,kind,value,source_id) DO NOTHING`, rule.ID, id, rule.Kind, rule.Value, rule.SourceID); err != nil {
			return report, err
		}
	}
	report, err = refreshPlaylistSnapshot(ctx, tx, id)
	if err != nil {
		return report, err
	}
	return report, tx.Commit()
}

// Delete removes only the playlist and its cascading selections.
func (r Playlists) Delete(ctx context.Context, id string) error {
	res, err := r.DB.ExecContext(ctx, `DELETE FROM playlists WHERE id=?`, id)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			return playlists.ErrInUse
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return playlists.ErrNotFound
	}
	return nil
}

// Availability aggregates the same membership as Resolve inside SQLite, without
// decoding up to thousands of recordings merely to check whether a pool is empty.
func (r Playlists) Availability(ctx context.Context, id string) (playlists.Availability, error) {
	var counts playlists.Availability
	tx, err := r.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return counts, err
	}
	defer tx.Rollback()
	if err = requirePlaylist(ctx, tx, id); err != nil {
		return counts, err
	}
	err = tx.QueryRowContext(ctx, `WITH candidates AS (
 SELECT 1 AS available FROM catalogue_items i
 WHERE (EXISTS(SELECT 1 FROM playlist_selections p WHERE p.playlist_id=? AND p.content_id=i.id AND p.included=1)
 OR EXISTS(SELECT 1 FROM playlist_rules r WHERE r.playlist_id=? AND ((r.kind='genre' AND r.value=json_extract(i.metadata,'$.genre')) OR (r.kind='album' AND r.value=i.album AND r.source_id=i.source_id))))
 AND NOT EXISTS(SELECT 1 FROM playlist_selections x WHERE x.playlist_id=? AND x.content_id=i.id AND x.included=0)
 UNION ALL SELECT 0 FROM playlist_selections p LEFT JOIN catalogue_items i ON i.id=p.content_id WHERE p.playlist_id=? AND p.included=1 AND i.id IS NULL
 ) SELECT count(*),coalesce(sum(1-available),0) FROM candidates`, id, id, id, id).Scan(&counts.Total, &counts.Unavailable)
	if err != nil {
		return counts, err
	}
	return counts, tx.Commit()
}
