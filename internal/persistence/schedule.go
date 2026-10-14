package persistence

import (
	"context"
	"database/sql"
	"errors"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
	"time"
)

type Schedule struct{ DB *sql.DB }

const showFields = `s.id,s.station_id,s.name,s.weekday,s.start_minute,s.end_minute,s.dj_id,s.playlist_id,s.revision,d.name,p.name,(SELECT curation_model FROM producer_settings WHERE id=1),d.voice,d.prompt,d.vibe,d.conversation_speed,d.background,d.portrait_url,` + speechReadySQL + `,` + textReadySQL + `,(SELECT ai_revision FROM producer_settings WHERE id=1),s.accept_requests,s.accept_shoutouts,s.description`
const showJoin = ` FROM station_shows s JOIN djs d ON d.id=s.dj_id JOIN playlists p ON p.id=s.playlist_id `

func scanShow(row interface{ Scan(...any) error }) (schedule.Show, error) {
	var v schedule.Show
	err := row.Scan(&v.ID, &v.StationID, &v.Name, &v.Day, &v.Start, &v.End, &v.DJID, &v.PlaylistID, &v.Revision, &v.DJName, &v.PlaylistName, &v.DJ.Model, &v.DJ.Voice, &v.DJ.Prompt, &v.DJ.Vibe, &v.DJ.ConversationSpeed, &v.DJ.Background, &v.DJ.PortraitURL, &v.DJ.HasKey, &v.DJ.TextReady, &v.DJ.AIRevision, &v.AcceptRequests, &v.AcceptShoutouts, &v.Description)
	v.DJ.ID = v.DJID
	v.DJ.Name = v.DJName
	if errors.Is(err, sql.ErrNoRows) {
		err = schedule.ErrNotFound
	}
	return v, err
}
func (r Schedule) List(ctx context.Context, id string) ([]schedule.Show, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT `+showFields+showJoin+` WHERE s.station_id=? ORDER BY s.weekday,s.start_minute,s.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []schedule.Show{}
	for rows.Next() {
		v, err := scanShow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r Schedule) Get(ctx context.Context, stationID, id string) (schedule.Show, error) {
	return scanShow(r.DB.QueryRowContext(ctx, `SELECT `+showFields+showJoin+` WHERE s.station_id=? AND s.id=?`, stationID, id))
}
func (r Schedule) Save(ctx context.Context, v schedule.Show) error {
	return r.SaveMany(ctx, []schedule.Show{v})
}

// SaveMany validates and saves every show within the same transaction.
func (r Schedule) SaveMany(ctx context.Context, shows []schedule.Show) error {
	if len(shows) == 0 || len(shows) > 7 {
		return schedule.ErrInvalid
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range shows {
		if err := saveShow(ctx, tx, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func saveShow(ctx context.Context, tx *sql.Tx, v schedule.Show) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if v.ID == "" {
		return schedule.ErrInvalid
	}
	var err error
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM stations WHERE id=?`, v.StationID).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return station.ErrNotFound
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM playlists WHERE id=? AND station_id=?`, v.PlaylistID, v.StationID).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return schedule.ErrInvalid
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM djs WHERE id=?`, v.DJID).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return schedule.ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,station_id,weekday,start_minute,end_minute FROM station_shows WHERE station_id=?`, v.StationID)
	if err != nil {
		return err
	}
	existing := []schedule.Show{}
	for rows.Next() {
		var x schedule.Show
		if err = rows.Scan(&x.ID, &x.StationID, &x.Day, &x.Start, &x.End); err != nil {
			rows.Close()
			return err
		}
		existing = append(existing, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	found := false
	for _, x := range existing {
		if x.ID == v.ID {
			found = true
			continue
		}
		if schedule.Overlaps(v, x) {
			return schedule.ErrOverlap
		}
	}
	if !found && len(existing) >= schedule.MaxShows {
		return schedule.ErrInvalid
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO station_shows(id,station_id,name,weekday,start_minute,end_minute,dj_id,playlist_id,accept_requests,accept_shoutouts,description) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,weekday=excluded.weekday,start_minute=excluded.start_minute,end_minute=excluded.end_minute,dj_id=excluded.dj_id,playlist_id=excluded.playlist_id,accept_requests=excluded.accept_requests,accept_shoutouts=excluded.accept_shoutouts,description=excluded.description,revision=station_shows.revision+1 WHERE station_shows.station_id=excluded.station_id`, v.ID, v.StationID, v.Name, v.Day, v.Start, v.End, v.DJID, v.PlaylistID, v.AcceptRequests, v.AcceptShoutouts, v.Description)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return schedule.ErrNotFound
	}
	return nil
}
func (r Schedule) Delete(ctx context.Context, stationID, id string) error {
	result, err := r.DB.ExecContext(ctx, `DELETE FROM station_shows WHERE station_id=? AND id=?`, stationID, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return schedule.ErrNotFound
	}
	return err
}
func (r Schedule) Resolve(ctx context.Context, id string, at time.Time) (schedule.Timeline, error) {
	st, err := (Stations{DB: r.DB}).Get(ctx, id)
	if err != nil {
		return schedule.Timeline{}, err
	}
	loc, err := time.LoadLocation(st.Timezone)
	if err != nil {
		return schedule.Timeline{}, err
	}
	shows, err := r.List(ctx, id)
	if err != nil {
		return schedule.Timeline{}, err
	}
	playlist, err := (Broadcast{DB: r.DB}).Assignment(ctx, id)
	if err != nil {
		return schedule.Timeline{}, err
	}
	name := ""
	if playlist != "" {
		p, err := (Playlists{DB: r.DB}).Get(ctx, playlist)
		if err != nil {
			return schedule.Timeline{}, err
		}
		name = p.Name
	}
	return schedule.Resolve(shows, id, playlist, name, at, loc), nil
}
