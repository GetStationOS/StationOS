package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"stationos.local/stationos/internal/playlists"
	"strings"
)

func writePlaylistBaseline(ctx context.Context, tx *sql.Tx, id string, result playlists.Result) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM playlist_refresh_items WHERE playlist_id=?`, id); err != nil {
		return err
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO playlist_refresh_items VALUES(?,?,?,?)`)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, entry := range result.Entries {
		if _, err := statement.ExecContext(ctx, id, entry.Item.ID, entry.Item.SourceID, entry.Item.Album); err != nil {
			return err
		}
	}
	return nil
}
func resetPlaylistBaseline(ctx context.Context, tx *sql.Tx, id string) error {
	result, err := resolvePlaylist(ctx, tx, id)
	if err != nil {
		return err
	}
	return writePlaylistBaseline(ctx, tx, id, result)
}

// Refresh re-evaluates the saved category selections against one catalogue snapshot.
// Existing rule matching stays live; the baseline counts only newly found members.
func (r Playlists) Refresh(ctx context.Context, id string) (playlists.RefreshResult, error) {
	var report playlists.RefreshResult
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	report, err = refreshPlaylistSnapshot(ctx, tx, id)
	if err != nil {
		return report, err
	}
	return report, tx.Commit()
}

func refreshPlaylistSnapshot(ctx context.Context, tx *sql.Tx, id string) (playlists.RefreshResult, error) {
	var report playlists.RefreshResult
	current, err := resolvePlaylist(ctx, tx, id)
	if err != nil {
		return report, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT content_id,source_id,album FROM playlist_refresh_items WHERE playlist_id=?`, id)
	if err != nil {
		return report, err
	}
	tracks, albums := map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var track, source, album string
		if err = rows.Scan(&track, &source, &album); err != nil {
			rows.Close()
			return report, err
		}
		tracks[track] = true
		if album != "" {
			albums[source+"\x00"+album] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return report, err
	}
	for _, entry := range current.Entries {
		if !entry.Available || tracks[entry.Item.ID] {
			continue
		}
		report.NewTracks++
		key := entry.Item.SourceID + "\x00" + entry.Item.Album
		if entry.Item.Album != "" && !albums[key] {
			report.NewAlbums++
			albums[key] = true
		}
	}
	if err = writePlaylistBaseline(ctx, tx, id, current); err != nil {
		return report, err
	}
	return report, nil
}

// Review adds excluded snapshots only to the editing view. Resolve, Availability
// and broadcast pools never include these entries, totals or their durations.
func (r Playlists) Review(ctx context.Context, id string, offset, limit int) (playlists.ReviewResult, error) {
	return r.SearchReview(ctx, id, "", offset, limit)
}
func (r Playlists) SearchReview(ctx context.Context, id, query string, offset, limit int) (playlists.ReviewResult, error) {
	var review playlists.ReviewResult
	if offset < 0 || offset > 1000000 || limit < 1 || limit > 100 {
		return review, playlists.ErrInvalid
	}
	tx, err := r.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return review, err
	}
	defer tx.Rollback()
	review.Result, err = resolvePlaylist(ctx, tx, id)
	if err != nil {
		return review, err
	}
	rows, err := tx.QueryContext(ctx, playlistSources+`SELECT coalesce(i.metadata,s.snapshot),i.id IS NOT NULL FROM playlist_selections s LEFT JOIN catalogue_items i ON i.id=s.content_id AND `+playlistSourceMatch+` WHERE s.playlist_id=? AND s.included=0 LIMIT ?`, id, id, playlists.MaxItems+1)
	if err != nil {
		return review, err
	}
	for rows.Next() {
		var raw string
		entry := playlists.Entry{Excluded: true}
		if err = rows.Scan(&raw, &entry.Available); err != nil {
			rows.Close()
			return review, err
		}
		if err = json.Unmarshal([]byte(raw), &entry.Item); err != nil {
			rows.Close()
			return review, err
		}
		review.Entries = append(review.Entries, entry)
		review.Excluded++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return review, err
	}
	if review.Excluded > playlists.MaxItems {
		return review, playlists.ErrTooLarge
	}
	sort.Slice(review.Entries, func(i, j int) bool {
		a, b := review.Entries[i].Item, review.Entries[j].Item
		left, right := strings.ToLower(a.Title), strings.ToLower(b.Title)
		if left == right {
			return a.ID < b.ID
		}
		return left < right
	})
	if terms := strings.Fields(strings.ToLower(query)); len(terms) > 0 {
		matches := make([]playlists.Entry, 0, len(review.Entries))
		for _, entry := range review.Entries {
			item := entry.Item
			text := strings.ToLower(strings.Join([]string{item.Title, item.Artist, item.Album, item.Composer}, " "))
			match := true
			for _, term := range terms {
				if !strings.Contains(text, term) {
					match = false
					break
				}
			}
			if match {
				matches = append(matches, entry)
			}
		}
		review.Entries = matches
	}
	review.Rows = len(review.Entries)
	start := min(offset, review.Rows)
	end := min(start+limit, review.Rows)
	review.Entries = review.Entries[start:end]
	return review, tx.Commit()
}
