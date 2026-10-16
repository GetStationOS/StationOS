package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"stationos.local/stationos/internal/operations"
	"time"
)

type Operations struct{ DB *sql.DB }

var _ operations.Store = Operations{}

func instant(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}
func restored(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}
func (o Operations) Save(ctx context.Context, e operations.Event) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	tx, err := o.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if e.Recover {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM transmission_incidents WHERE station_id=? AND code=? AND recovered_at=0`, e.StationID, e.IncidentCode).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO transmission_events(id,station_id,started,ended,kind,outcome,snapshot) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET ended=excluded.ended,outcome=excluded.outcome,snapshot=excluded.snapshot`, e.ID, e.StationID, instant(e.Started), instant(e.Ended), e.Kind, e.Outcome, string(raw))
	if err != nil {
		return err
	}
	if e.IncidentCode != "" {
		if e.Recover {
			_, err = tx.ExecContext(ctx, `UPDATE transmission_incidents SET recovered_at=? WHERE station_id=? AND code=? AND recovered_at=0`, instant(e.Started), e.StationID, e.IncidentCode)
		} else {
			i := operations.Incident{ID: e.ID, StationID: e.StationID, Code: e.IncidentCode, Severity: e.Severity, Message: e.Reason, Effect: e.Effect, Action: e.Action, EventID: e.ID}
			data, _ := json.Marshal(i)
			_, err = tx.ExecContext(ctx, `INSERT INTO transmission_incidents VALUES(?,?,?,?,?,0,0,1,?) ON CONFLICT(station_id,code) WHERE recovered_at=0 DO UPDATE SET latest_at=excluded.latest_at,occurrences=occurrences+1,snapshot=excluded.snapshot`, i.ID, e.StationID, e.IncidentCode, instant(e.Started), instant(e.Started), string(data))
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Only serve startup invokes recovery. CLI catalogue/integrity tasks must not
// close records belonging to a running server.
func (o Operations) Recover(ctx context.Context, at time.Time) error {
	tx, err := o.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT snapshot FROM transmission_events WHERE outcome='transmitting'`)
	if err != nil {
		return err
	}
	var events []operations.Event
	for rows.Next() {
		var raw string
		var e operations.Event
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal([]byte(raw), &e)
		}
		if err != nil {
			rows.Close()
			return err
		}
		events = append(events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range events {
		e.Outcome = "unknown"
		e.Reason = "Server restarted before completion was recorded; end time is unknown."
		raw, _ := json.Marshal(e)
		if _, err = tx.ExecContext(ctx, `UPDATE transmission_events SET outcome='unknown',snapshot=? WHERE id=?`, string(raw), e.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func historyWhere(f operations.Filter) (string, []any) {
	query := ` FROM transmission_events WHERE station_id=?`
	args := []any{f.StationID}
	if !f.From.IsZero() {
		query += ` AND started>=?`
		args = append(args, instant(f.From))
	}
	if !f.Until.IsZero() {
		query += ` AND started<?`
		args = append(args, instant(f.Until))
	}
	if f.Kind != "" {
		query += ` AND kind=?`
		args = append(args, f.Kind)
	}
	if f.Outcome != "" {
		query += ` AND outcome=?`
		args = append(args, f.Outcome)
	}
	if f.ShowID != "" {
		query += ` AND json_extract(snapshot,'$.ShowID')=?`
		args = append(args, f.ShowID)
	}
	if f.ShowName != "" {
		query += ` AND json_extract(snapshot,'$.Show')=?`
		args = append(args, f.ShowName)
	}
	if f.DJID != "" {
		query += ` AND json_extract(snapshot,'$.DJID')=?`
		args = append(args, f.DJID)
	}
	if f.Query != "" {
		query += ` AND (id=? OR instr(lower(json_extract(snapshot,'$.Title') || ' ' || json_extract(snapshot,'$.Artist') || ' ' || json_extract(snapshot,'$.Speech') || ' ' || json_extract(snapshot,'$.Reason')),lower(?))>0)`
		args = append(args, f.Query, f.Query)
	}
	if f.Before != "" {
		query += ` AND (started,sequence)<(SELECT started,sequence FROM transmission_events WHERE id=? AND station_id=?)`
		args = append(args, f.Before, f.StationID)
	}
	if f.Anchor != "" {
		query += ` AND (started,sequence)<=(SELECT started,sequence FROM transmission_events WHERE id=? AND station_id=?)`
		args = append(args, f.Anchor, f.StationID)
	}
	return query, args
}
func (o Operations) Events(ctx context.Context, f operations.Filter) ([]operations.Event, error) {
	where, args := historyWhere(f)
	query := "SELECT snapshot" + where
	query += ` ORDER BY started DESC,sequence DESC LIMIT ?`
	limit := f.Limit
	if limit <= 0 || limit > 1001 {
		limit = 51
	}
	args = append(args, limit)
	rows, err := o.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []operations.Event
	for rows.Next() {
		var raw string
		var e operations.Event
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// Count and read one bounded page from the same database snapshot.
func (o Operations) History(ctx context.Context, f operations.Filter, page, size int) (operations.HistoryPage, error) {
	result := operations.HistoryPage{}
	if page < 1 || page > 40001 || (size != 25 && size != 50 && size != 100) {
		return result, errors.New("invalid history page")
	}
	where, args := historyWhere(f)
	tx, err := o.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT count(*)"+where, args...).Scan(&result.Total); err != nil {
		return result, err
	}
	result.Number = min(page, max(1, (result.Total+size-1)/size))
	result.Size = size
	if result.Total > 0 {
		if err = tx.QueryRowContext(ctx, "SELECT id"+where+" ORDER BY started DESC,sequence DESC LIMIT 1", args...).Scan(&result.Anchor); err != nil {
			return result, err
		}
	}
	params := append(append([]any(nil), args...), size, (result.Number-1)*size)
	rows, err := tx.QueryContext(ctx, "SELECT snapshot"+where+" ORDER BY started DESC,sequence DESC LIMIT ? OFFSET ?", params...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var raw string
		var event operations.Event
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return result, err
		}
		if err = json.Unmarshal([]byte(raw), &event); err != nil {
			rows.Close()
			return result, err
		}
		result.Events = append(result.Events, event)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (o Operations) Incidents(ctx context.Context, station string, active bool) ([]operations.Incident, error) {
	query := `SELECT id,first_at,latest_at,recovered_at,acknowledged_at,occurrences,snapshot FROM transmission_incidents WHERE station_id=?`
	if active {
		query += ` AND recovered_at=0`
	}
	query += ` ORDER BY recovered_at=0 DESC,latest_at DESC LIMIT 100`
	rows, err := o.DB.QueryContext(ctx, query, station)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []operations.Incident
	for rows.Next() {
		var i operations.Incident
		var id, raw string
		var first, last, recovered, ack int64
		var count int
		if err = rows.Scan(&id, &first, &last, &recovered, &ack, &count, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &i); err != nil {
			return nil, err
		}
		i.ID = id
		i.First = restored(first)
		i.Latest = restored(last)
		i.Recovered = restored(recovered)
		i.Acknowledged = restored(ack)
		i.Count = count
		result = append(result, i)
	}
	return result, rows.Err()
}
func (o Operations) Acknowledge(ctx context.Context, station, id string, at time.Time) error {
	result, err := o.DB.ExecContext(ctx, `UPDATE transmission_incidents SET acknowledged_at=? WHERE station_id=? AND id=? AND acknowledged_at=0`, instant(at), station, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return errors.New("incident not found or already acknowledged")
	}
	return nil
}
