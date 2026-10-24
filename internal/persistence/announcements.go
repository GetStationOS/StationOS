package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"stationos.local/stationos/internal/announcements"
	"stationos.local/stationos/internal/changefeed"
	"strconv"
	"strings"
	"time"
)

type Announcements struct {
	DB      *sql.DB
	Changes *changefeed.Bus
}

var _ announcements.Repository = Announcements{}

func (r Announcements) List(ctx context.Context, station string) ([]announcements.Message, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT payload,version FROM station_announcements WHERE station_id=? ORDER BY id`, station)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []announcements.Message{}
	for rows.Next() {
		var raw string
		var v int
		if err = rows.Scan(&raw, &v); err != nil {
			return nil, err
		}
		var m announcements.Message
		if err = json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, err
		}
		m.Version = v
		out = append(out, m)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		if err = r.stats(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (r Announcements) stats(ctx context.Context, m *announcements.Message) error {
	var at sql.NullInt64
	if err := r.DB.QueryRowContext(ctx, `SELECT count(*),max(started_at) FROM announcement_receipts WHERE station_id=? AND announcement_id=? AND outcome IN ('transmitting','completed','interrupted')`, m.StationID, m.ID).Scan(&m.Plays, &at); err != nil {
		return err
	}
	if at.Valid {
		m.LastAired = time.Unix(0, at.Int64)
		if err := r.DB.QueryRowContext(ctx, `SELECT outcome FROM announcement_receipts WHERE station_id=? AND announcement_id=? ORDER BY started_at DESC LIMIT 1`, m.StationID, m.ID).Scan(&m.LastOutcome); err != nil {
			return err
		}
	}
	return nil
}
func (r Announcements) Get(ctx context.Context, station, id string) (announcements.Message, error) {
	var raw string
	var m announcements.Message
	var version int
	err := r.DB.QueryRowContext(ctx, `SELECT payload,version FROM station_announcements WHERE station_id=? AND id=?`, station, id).Scan(&raw, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return m, announcements.ErrNotFound
	}
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal([]byte(raw), &m); err != nil {
		return m, err
	}
	m.Version = version
	err = r.stats(ctx, &m)
	return m, err
}
func (r Announcements) Save(ctx context.Context, m announcements.Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	// Show and dated-instance targeting must belong to the current station.
	shows, err := (Schedule{DB: r.DB}).List(ctx, m.StationID)
	if err != nil {
		return err
	}
	for _, id := range m.ShowIDs {
		found := false
		for _, s := range shows {
			if s.ID == id && !s.Archived {
				found = true
				if m.EpisodeKey != "" {
					valid := false
					for _, day := range s.Weekdays() {
						slot := s.SlotIDs[day]
						if slot == "" {
							slot = strconv.Itoa(day)
						}
						if strings.HasPrefix(m.EpisodeKey, s.ID+":"+slot+":") {
							valid = true
						}
					}
					if !valid {
						return fmt.Errorf("%w: choose an episode of the selected show", announcements.ErrInvalid)
					}
				}
			}
		}
		if !found {
			return fmt.Errorf("%w: choose shows belonging to this station", announcements.ErrInvalid)
		}
	}
	if m.EpisodeKey != "" {
		parts := strings.Split(m.EpisodeKey, ":")
		if len(parts) != 3 {
			return fmt.Errorf("%w: invalid episode identity", announcements.ErrInvalid)
		}
		week, err := time.Parse("2006-01-02", parts[2])
		if err != nil || week.Weekday() != time.Monday {
			return fmt.Errorf("%w: invalid episode date", announcements.ErrInvalid)
		}
	}
	m.LastAired = time.Time{}
	m.Plays = 0
	m.LastOutcome = ""
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if m.Version == 0 {
		var result sql.Result
		result, err = r.DB.ExecContext(ctx, `INSERT INTO station_announcements(id,station_id,dj_id,version,payload) SELECT ?,?,?,1,? WHERE (SELECT count(*) FROM station_announcements WHERE station_id=?)<?`, m.ID, m.StationID, nullID(m.DJID), string(raw), m.StationID, announcements.MaxItems)
		if err == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				return fmt.Errorf("%w: this station has reached its announcement limit", announcements.ErrInvalid)
			}
		}
	} else {
		var result sql.Result
		result, err = r.DB.ExecContext(ctx, `UPDATE station_announcements SET dj_id=?,payload=?,version=version+1 WHERE station_id=? AND id=? AND version=?`, nullID(m.DJID), string(raw), m.StationID, m.ID, m.Version)
		if err == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				return announcements.ErrConflict
			}
		}
	}
	if err == nil {
		r.Changes.Publish("announcements")
	}
	return err
}
func nullID(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func (r Announcements) Delete(ctx context.Context, station, id string, version int) error {
	res, err := r.DB.ExecContext(ctx, `DELETE FROM station_announcements WHERE station_id=? AND id=? AND version=?`, station, id, version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return announcements.ErrConflict
	}
	r.Changes.Publish("announcements")
	return nil
}
func (r Announcements) Settings(ctx context.Context, station string) (announcements.Settings, error) {
	s := announcements.Settings{StationID: station, GapMinutes: 10}
	var dj sql.NullString
	err := r.DB.QueryRowContext(ctx, `SELECT dj_id,gap_minutes,version FROM station_announcement_settings WHERE station_id=?`, station).Scan(&dj, &s.GapMinutes, &s.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	s.DJID = dj.String
	return s, err
}
func (r Announcements) SaveSettings(ctx context.Context, s announcements.Settings) error {
	if s.GapMinutes < 1 || s.GapMinutes > 1440 {
		return fmt.Errorf("%w: minimum gap must be 1–1,440 minutes", announcements.ErrInvalid)
	}
	res, err := r.DB.ExecContext(ctx, `INSERT INTO station_announcement_settings(station_id,dj_id,gap_minutes,version) VALUES(?,?,?,1) ON CONFLICT(station_id) DO UPDATE SET dj_id=excluded.dj_id,gap_minutes=excluded.gap_minutes,version=version+1 WHERE version=?`, s.StationID, nullID(s.DJID), s.GapMinutes, s.Version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return announcements.ErrConflict
	}
	r.Changes.Publish("announcements")
	return nil
}
func (r Announcements) LastAired(ctx context.Context, station string) (time.Time, error) {
	var at sql.NullInt64
	err := r.DB.QueryRowContext(ctx, `SELECT max(started_at) FROM announcement_receipts WHERE station_id=? AND outcome IN ('transmitting','completed','interrupted')`, station).Scan(&at)
	if !at.Valid {
		return time.Time{}, err
	}
	return time.Unix(0, at.Int64), err
}
func (r Announcements) Record(ctx context.Context, a announcements.Receipt) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO announcement_receipts(id,station_id,announcement_id,started_at,outcome,payload) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET outcome=excluded.outcome,payload=excluded.payload`, a.ID, a.StationID, a.AnnouncementID, a.Started.UnixNano(), a.Outcome, string(raw))
	if err != nil {
		return err
	}
	if a.Outcome == "transmitting" {
		_, err = tx.ExecContext(ctx, `UPDATE station_announcements SET payload=json_set(payload,'$.Next',json('false')) WHERE id=? AND station_id=? AND version=?`, a.AnnouncementID, a.StationID, a.Version)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(); err == nil {
		r.Changes.Publish("announcements")
	}
	return err
}
func (r Announcements) History(ctx context.Context, station, id string) ([]announcements.Receipt, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT payload FROM announcement_receipts WHERE station_id=? AND announcement_id=? ORDER BY started_at DESC LIMIT 100`, station, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []announcements.Receipt{}
	for rows.Next() {
		var raw string
		var a announcements.Receipt
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Recover runs only when starting the broadcast application, never on a database read.
func (r Announcements) Recover(ctx context.Context) error {
	_, err := r.DB.ExecContext(ctx, `UPDATE announcement_receipts SET outcome='interrupted',payload=json_set(payload,'$.Outcome','interrupted') WHERE outcome='transmitting'`)
	if err == nil {
		r.Changes.Publish("announcements")
	}
	return err
}
