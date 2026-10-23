package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"stationos.local/stationos/internal/changefeed"
	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/schedule"
)

type Episodes struct {
	DB      *sql.DB
	Changes *changefeed.Bus
}

func (r Episodes) Get(ctx context.Context, key string) (episodes.Episode, error) {
	var raw string
	var e episodes.Episode
	err := r.DB.QueryRowContext(ctx, `SELECT payload FROM show_episodes WHERE episode_key=?`, episodes.SessionKey(key)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return e, episodes.ErrNotFound
	}
	if err != nil {
		return e, err
	}
	err = json.Unmarshal([]byte(raw), &e)
	return e, err
}
func (r Episodes) Update(ctx context.Context, o schedule.Occurrence, fn func(*episodes.Episode) error) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Revalidate revision and civil-time identity; obsolete workers cannot save plans.
	show, err := scanShow(tx.QueryRowContext(ctx, `SELECT `+showFields+showJoin+` WHERE s.station_id=? AND s.id=?`, o.StationID, o.ShowID))
	if err != nil {
		return err
	}
	valid := false
	for _, candidate := range schedule.Occurrences([]schedule.Show{show}, o.Start, o.Start.Location()) {
		if !show.Archived && candidate.Key == o.Key {
			valid = true
		}
	}
	if !valid {
		return episodes.ErrStale
	}
	e := episodes.New(o)
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT payload FROM show_episodes WHERE episode_key=?`, episodes.SessionKey(o.Key)).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &e); err != nil {
			return err
		}
	}
	if e.ShowKey != o.Key {
		e.ShowKey = o.Key
		e.RepetitionReason = ""
		e.Start = o.Start
		e.End = o.End
		if !e.Cancelled {
			e.Status = "attention"
			e.Problem = "Show settings changed; updating the running order."
		}
		e.RetryAt = time.Time{}
	}
	if err = fn(&e); err != nil {
		return err
	}
	e.Version++
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO show_episodes(episode_key,station_id,show_id,starts_at,payload) VALUES(?,?,?,?,?) ON CONFLICT(episode_key) DO UPDATE SET starts_at=excluded.starts_at,payload=excluded.payload`, episodes.SessionKey(o.Key), o.StationID, o.ShowID, o.Start.Unix(), string(data))
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	r.Changes.Publish("episodes")
	return nil
}

func (r Episodes) ListShow(ctx context.Context, stationID, showID string) ([]episodes.Summary, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT episode_key,json_extract(payload,'$.Start'),json_extract(payload,'$.End'),json_extract(payload,'$.Status'),COALESCE(json_extract(payload,'$.PlanningFailed'),0),COALESCE(json_extract(payload,'$.Cancelled'),0),COALESCE(json_array_length(payload,'$.Aired'),0) FROM show_episodes WHERE station_id=? AND show_id=? ORDER BY starts_at DESC`, stationID, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []episodes.Summary{}
	for rows.Next() {
		var v episodes.Summary
		var start, end, status string
		var failed bool
		if err := rows.Scan(&v.Key, &start, &end, &status, &failed, &v.Cancelled, &v.AiredCount); err != nil {
			return nil, err
		}
		v.Start, err = time.Parse(time.RFC3339, start)
		if err != nil {
			return nil, err
		}
		v.End, err = time.Parse(time.RFC3339, end)
		if err != nil {
			return nil, err
		}
		v.Label = (episodes.Episode{Status: status, PlanningFailed: failed, Cancelled: v.Cancelled}).Label()
		out = append(out, v)
	}
	return out, rows.Err()
}
