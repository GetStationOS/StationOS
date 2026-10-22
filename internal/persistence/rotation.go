package persistence

import (
	"context"
	"database/sql"
	"errors"
	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/station"
	"time"
)

var _ broadcast.History = Broadcast{}

func (r Broadcast) Rotation(ctx context.Context, id string, at time.Time) (broadcast.Rotation, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return broadcast.Rotation{}, err
	}
	defer tx.Rollback()
	var minutes int
	if err = tx.QueryRowContext(ctx, `SELECT repeat_minutes FROM stations WHERE id=?`, id).Scan(&minutes); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = station.ErrNotFound
		}
		return broadcast.Rotation{}, err
	}
	result := broadcast.Rotation{Interval: time.Duration(minutes) * time.Minute, LastPlayed: map[string]time.Time{}}
	err = tx.QueryRowContext(ctx, `SELECT track_id FROM station_track_plays WHERE station_id=? ORDER BY last_started_at DESC,track_id LIMIT 1`, id).Scan(&result.LastTrackID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	// Keep one last-played timestamp per recording, including plays outside the
	// repeat window, so never-played and long-unplayed music rank differently.
	rows, err := tx.QueryContext(ctx, `SELECT track_id,last_started_at FROM station_track_plays WHERE station_id=?`, id)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var track string
		var started int64
		if err = rows.Scan(&track, &started); err != nil {
			rows.Close()
			return result, err
		}
		result.LastPlayed[track] = time.Unix(0, started)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (r Broadcast) RecordPlay(ctx context.Context, stationID, trackID string, at time.Time) error {
	if trackID == "" || at.IsZero() {
		return errors.New("invalid track play")
	}
	_, err := r.DB.ExecContext(ctx, `INSERT INTO station_track_plays(station_id,track_id,last_started_at) VALUES(?,?,?) ON CONFLICT(station_id,track_id) DO UPDATE SET last_started_at=MAX(station_track_plays.last_started_at,excluded.last_started_at)`, stationID, trackID, at.UnixNano())
	return err
}
