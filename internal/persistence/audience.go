package persistence

import (
	"context"
	"database/sql"
	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/changefeed"
	"stationos.local/stationos/internal/episodes"
	"strings"
	"time"
)

type Audience struct {
	DB      *sql.DB
	Changes *changefeed.Bus
}

var _ audience.Repository = Audience{}

const audienceFields = `id,station_id,show_id,occurrence_key,show_name,sender_key,kind,status,listener_name,message,track_id,track_title,track_artist,reason,created_at,show_start,expires_at,used_at`

func scanAudience(row interface{ Scan(...any) error }) (audience.Message, error) {
	var m audience.Message
	var created, start, end, used int64
	err := row.Scan(&m.ID, &m.StationID, &m.ShowID, &m.OccurrenceKey, &m.ShowName, &m.SenderKey, &m.Kind, &m.Status, &m.Name, &m.Text, &m.TrackID, &m.TrackTitle, &m.TrackArtist, &m.Reason, &created, &start, &end, &used)
	m.Created = time.Unix(created, 0)
	m.ShowStart = time.Unix(start, 0)
	m.Expires = time.Unix(end, 0)
	if used != 0 {
		m.UsedAt = time.Unix(used, 0)
	}
	return m, err
}
func expireAudience(ctx context.Context, tx *sql.Tx, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE audience_messages SET status='expired',reason='Show ended or changed' WHERE status IN ('pending','review','approved') AND (expires_at<=? OR NOT EXISTS(SELECT 1 FROM station_shows s WHERE s.id=show_id AND s.archived=0 AND (kind='request' AND s.accept_requests=1 OR kind='shoutout' AND s.accept_shoutouts=1) AND occurrence_key LIKE s.id||':%:'||s.revision))`, now.Unix())
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM audience_messages WHERE expires_at<?`, now.Add(-30*24*time.Hour).Unix())
	return err
}
func (r Audience) Submit(ctx context.Context, m audience.Message) (changeErr error) {
	defer func() {
		if changeErr == nil {
			r.Changes.Publish("audience")
		}
	}()
	if err := m.Validate(); err != nil {
		return err
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = expireAudience(ctx, tx, m.Created); err != nil {
		return err
	}
	var count int
	// Recheck public/show policy atomically at commit, including occurrence revision.
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM station_shows s JOIN stations t ON t.id=s.station_id WHERE s.id=? AND s.archived=0 AND s.station_id=? AND t.visibility<>'private' AND ? LIKE s.id||':%:'||s.revision AND (s.accept_requests=1 AND ?='request' OR s.accept_shoutouts=1 AND ?='shoutout') AND NOT EXISTS (SELECT 1 FROM show_episodes WHERE episode_key=? AND json_extract(payload,'$.Cancelled')=1)`, m.ShowID, m.StationID, m.OccurrenceKey, m.Kind, m.Kind, episodes.SessionKey(m.OccurrenceKey)).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return audience.ErrClosed
	}
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM audience_messages WHERE sender_key=? AND station_id=? AND occurrence_key=? AND kind=? AND track_id=? AND listener_name=? AND message=?`, m.SenderKey, m.StationID, m.OccurrenceKey, m.Kind, m.TrackID, m.Name, m.Text).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return audience.ErrDuplicate
	}
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM audience_messages WHERE sender_key=? AND created_at>?`, m.SenderKey, m.Created.Add(-10*time.Minute).Unix()).Scan(&count)
	if err != nil {
		return err
	}
	if count >= 3 {
		return audience.ErrLimited
	}
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM audience_messages WHERE station_id=? AND status IN ('pending','review','approved')`, m.StationID).Scan(&count)
	if err != nil {
		return err
	}
	if count >= audience.MaxMessages {
		return audience.ErrLimited
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audience_messages(`+audienceFields+`)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)`, m.ID, m.StationID, m.ShowID, m.OccurrenceKey, m.ShowName, m.SenderKey, m.Kind, m.Status, m.Name, m.Text, m.TrackID, m.TrackTitle, m.TrackArtist, m.Reason, m.Created.Unix(), m.ShowStart.Unix(), m.Expires.Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r Audience) List(ctx context.Context, station, status string, offset, limit int, now time.Time) ([]audience.Message, int, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	if err = expireAudience(ctx, tx, now); err != nil {
		return nil, 0, err
	}
	filter := `station_id=?`
	args := []any{station}
	if status == "active" {
		filter += ` AND status IN ('pending','review','approved')`
	} else if status != "" {
		statuses := strings.Split(status, ",")
		filter += ` AND status IN (` + strings.TrimRight(strings.Repeat("?,", len(statuses)), ",") + `)`
		for _, value := range statuses {
			args = append(args, value)
		}
	}
	var total int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM audience_messages WHERE `+filter, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+audienceFields+` FROM audience_messages WHERE `+filter+` ORDER BY created_at DESC,id LIMIT ? OFFSET ?`, append(args, min(100, max(1, limit)), max(0, offset))...)
	if err != nil {
		return nil, 0, err
	}
	items := []audience.Message{}
	for rows.Next() {
		m, e := scanAudience(rows)
		if e != nil {
			rows.Close()
			return nil, 0, e
		}
		items = append(items, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	return items, total, tx.Commit()
}
func (r Audience) ForShow(ctx context.Context, station, key string, status audience.Status, at time.Time, limit int) ([]audience.Message, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = expireAudience(ctx, tx, at); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+audienceFields+` FROM audience_messages WHERE station_id=? AND occurrence_key=? AND status=? AND show_start<=? AND expires_at>? ORDER BY created_at,id LIMIT ?`, station, key, status, at.Unix(), at.Unix(), min(100, max(1, limit)))
	if err != nil {
		return nil, err
	}
	out := []audience.Message{}
	for rows.Next() {
		m, e := scanAudience(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
func (r Audience) Decide(ctx context.Context, station, id string, status audience.Status, reason string, now time.Time) (changeErr error) {
	defer func() {
		if changeErr == nil {
			r.Changes.Publish("audience")
		}
	}()
	if status != audience.Approved && status != audience.Rejected {
		return audience.ErrInvalid
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = expireAudience(ctx, tx, now); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE audience_messages SET status=?,reason=? WHERE station_id=? AND id=? AND status IN ('pending','review','approved') AND expires_at>?`, status, reason, station, id, now.Unix())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return audience.ErrConflict
	}
	return tx.Commit()
}
func (r Audience) Usable(ctx context.Context, station, key string, ids []string, at time.Time) (bool, error) {
	if len(ids) == 0 {
		return true, nil
	}
	if len(ids) > audience.MaxBatch {
		return false, audience.ErrInvalid
	}
	// Include current policy/revision and publication when checking prepared speech.
	for _, id := range ids {
		var count int
		err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM audience_messages a JOIN station_shows s ON s.id=a.show_id JOIN stations t ON t.id=a.station_id WHERE a.id=? AND a.station_id=? AND a.occurrence_key=? AND s.archived=0 AND a.status='approved' AND a.expires_at>? AND t.visibility<>'private' AND a.occurrence_key LIKE s.id||':%:'||s.revision AND (a.kind='request' AND s.accept_requests=1 OR a.kind='shoutout' AND s.accept_shoutouts=1)`, id, station, key, at.Unix()).Scan(&count)
		if err != nil {
			return false, err
		}
		if count != 1 {
			return false, nil
		}
	}
	return true, nil
}
func (r Audience) MarkUsed(ctx context.Context, station string, ids []string, at time.Time) (changeErr error) {
	defer func() {
		if changeErr == nil {
			r.Changes.Publish("audience")
		}
	}()
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > audience.MaxMessages {
		return audience.ErrInvalid
	}
	// Only actual delivered audio calls this method, never preview or preparation.
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := []any{at.Unix(), station}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := r.DB.ExecContext(ctx, `UPDATE audience_messages SET status='used',used_at=?,reason='Broadcast' WHERE station_id=? AND status='approved' AND id IN (`+placeholders+`)`, args...)
	return err
}

// Review only decides still-pending items. An operator decision during an AI
// request wins, and changing a show while reviewing cannot revive its messages.
func (r Audience) Review(ctx context.Context, station string, decisions []audience.Decision, at time.Time) (changeErr error) {
	defer func() {
		if changeErr == nil {
			r.Changes.Publish("audience")
		}
	}()
	if len(decisions) > audience.MaxBatch {
		return audience.ErrInvalid
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = expireAudience(ctx, tx, at); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, d := range decisions {
		if d.ID == "" || seen[d.ID] {
			return audience.ErrInvalid
		}
		seen[d.ID] = true

		status, reason := audience.Rejected, "AI review: dismissed"
		if d.Uncertain {
			status, reason = audience.ReviewNeeded, "AI review: needs producer review"
		} else if d.Approve {
			status, reason = audience.Approved, "AI review: approved"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE audience_messages SET status=?,reason=? WHERE station_id=? AND id=? AND status='pending' AND expires_at>?`, status, reason, station, d.ID, at.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NextExpiry identifies the next clock transition affecting the station queue.
func (r Audience) NextExpiry(ctx context.Context, station string, now time.Time) (time.Time, error) {
	var at sql.NullInt64
	err := r.DB.QueryRowContext(ctx, `SELECT min(expires_at) FROM audience_messages WHERE station_id=? AND status IN ('pending','review','approved') AND expires_at>?`, station, now.Unix()).Scan(&at)
	if err != nil || !at.Valid {
		return time.Time{}, err
	}
	return time.Unix(at.Int64, 0), nil
}
