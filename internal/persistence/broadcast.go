package persistence

import (
	"context"
	"database/sql"
	"errors"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/station"
)

// Broadcast stores configuration only; listeners and playback are process state.
type Broadcast struct{ DB *sql.DB }

func (r Broadcast) Assignment(ctx context.Context, id string) (string, error) {
	var value sql.NullString
	err := r.DB.QueryRowContext(ctx, `SELECT b.playlist_id FROM stations s LEFT JOIN station_broadcast b ON b.station_id=s.id WHERE s.id=?`, id).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", station.ErrNotFound
	}
	return value.String, err
}

func (r Broadcast) Assign(ctx context.Context, stationID, playlistID string) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM stations WHERE id=?`, stationID).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return station.ErrNotFound
	}
	var value any
	if playlistID != "" {
		var owner string
		if err = tx.QueryRowContext(ctx, `SELECT station_id FROM playlists WHERE id=?`, playlistID).Scan(&owner); errors.Is(err, sql.ErrNoRows) {
			return playlists.ErrInvalid
		} else if err != nil {
			return err
		}
		if owner != stationID {
			return playlists.ErrInvalid
		}
		value = playlistID
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO station_broadcast(station_id,playlist_id) VALUES(?,?) ON CONFLICT(station_id) DO UPDATE SET playlist_id=excluded.playlist_id`, stationID, value); err != nil {
		return err
	}
	return tx.Commit()
}

func (r Broadcast) Pool(ctx context.Context, playlistID string) ([]content.AudioItem, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	entries, err := resolvePlaylist(ctx, tx, playlistID)
	if err != nil {
		return nil, err
	}
	out := []content.AudioItem{}
	for _, entry := range entries.Entries {
		if entry.Available {
			out = append(out, entry.Item)
		}
	}
	return out, tx.Commit()
}
