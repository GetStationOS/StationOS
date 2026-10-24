package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/schedule"
)

// Called inside the show-save transaction, before publication. Completed episodes
// are immutable; an active aired episode keeps its dated start and session key.
func reconcileEpisodeEnds(ctx context.Context, tx *sql.Tx, show schedule.Show, now time.Time) error {
	var zone string
	if err := tx.QueryRowContext(ctx, `SELECT timezone FROM stations WHERE id=?`, show.StationID).Scan(&zone); err != nil {
		return err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT episode_key,payload FROM show_episodes WHERE station_id=? AND show_id=? AND starts_at<=? AND unixepoch(json_extract(payload,'$.End'))>? AND json_array_length(payload,'$.Aired')>0`, show.StationID, show.ID, now.Unix(), now.Unix())
	if err != nil {
		return err
	}
	type change struct {
		key     string
		episode episodes.Episode
	}
	changes := []change{}
	for rows.Next() {
		var key, raw string
		if err = rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return err
		}
		var e episodes.Episode
		if err = json.Unmarshal([]byte(raw), &e); err != nil {
			rows.Close()
			return err
		}
		if e.Cancelled || len(e.Aired) == 0 || !now.Before(e.End) {
			continue
		}
		// Keep today's start, even when the parent start or weekday also changed.
		start := e.Start.In(loc)
		endMinutes := show.End
		if endMinutes <= start.Hour()*60+start.Minute() {
			endMinutes += 1440
		}
		end := schedule.WallTime(start, endMinutes, true)
		if end.Equal(e.End) {
			continue
		}
		e.ReconcileEnd(end, now)
		changes = append(changes, change{key, e})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range changes {
		raw, err := json.Marshal(c.episode)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE show_episodes SET payload=? WHERE episode_key=?`, string(raw), c.key); err != nil {
			return err
		}
	}
	return nil
}
