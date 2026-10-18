package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/sources"
)

type Catalogue struct{ DB *sql.DB }

var _ catalogue.Repository = Catalogue{}

func (r Catalogue) Replace(ctx context.Context, c sources.Config, items []content.AudioItem) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var rev int64
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM sources WHERE id=?`, c.ID).Scan(&rev); err != nil {
		return err
	}
	if rev != c.Revision {
		return sources.ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalogue_items WHERE source_id=?`, c.ID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO catalogue_items(id,source_id,source_item_id,title,artist,album,metadata) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, a := range items {
		if err = a.Validate(); err != nil {
			return err
		}
		if a.SourceID != c.ID || a.ID != content.ID(c.ID, a.SourceItemID) {
			return errors.New("catalogue identity mismatch")
		}
		b, err := json.Marshal(a)
		if err != nil {
			return err
		}
		if _, err = stmt.ExecContext(ctx, a.ID, a.SourceID, a.SourceItemID, a.Title, a.Artist, a.Album, string(b)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (r Catalogue) Search(ctx context.Context, q catalogue.Query) (catalogue.Result, error) {
	if err := q.Validate(); err != nil {
		return catalogue.Result{}, err
	}
	// Read count and results from one snapshot so paging cannot disagree during imports.
	tx, err := r.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return catalogue.Result{}, err
	}
	defer tx.Rollback()
	where, args := catalogueFilter(q)
	out := catalogue.Result{Items: []content.AudioItem{}}
	if err = tx.QueryRowContext(ctx, `SELECT count(*)`+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	args = append(args, q.Limit, q.Offset)
	rows, err := tx.QueryContext(ctx, `SELECT i.metadata`+where+catalogueOrder(q)+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return out, err
		}
		var a content.AudioItem
		if err = json.Unmarshal([]byte(raw), &a); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (r Catalogue) Clear(ctx context.Context, id string) error {
	res, err := r.DB.ExecContext(ctx, `DELETE FROM catalogue_items WHERE (?='' OR source_id=?)`, id, id)
	if err != nil {
		return err
	}
	_, err = res.RowsAffected()
	return err
}

func catalogueFilter(q catalogue.Query) (string, []any) {
	return ` FROM catalogue_items i JOIN sources s ON s.id=i.source_id WHERE
 (?='' OR i.source_id=?) AND (?='' OR EXISTS(SELECT 1 FROM catalogue_genres g WHERE g.content_id=i.id AND g.genre=?))
 AND (?='' OR i.album=?) AND
 (instr(lower(i.title),lower(?))>0 OR instr(lower(i.artist),lower(?))>0 OR instr(lower(i.album),lower(?))>0 OR instr(lower(COALESCE(json_extract(i.metadata,'$.genre'),'')),lower(?))>0)`,
		[]any{q.SourceID, q.SourceID, q.Genre, q.Genre, q.Album, q.Album, q.Text, q.Text, q.Text, q.Text}
}
func catalogueOrder(q catalogue.Query) string {
	column := "lower(i.title)"
	switch q.Sort {
	case "artist":
		column = "lower(i.artist)"
	case "album":
		column = "lower(i.album)"
	case "duration":
		column = "json_extract(i.metadata,'$.duration_ns')"
	}
	direction := " ASC"
	if q.Descending {
		direction = " DESC"
	}
	return " ORDER BY " + column + direction + ",lower(i.title),i.id"
}
func (r Catalogue) Facets(ctx context.Context) (catalogue.Facets, error) {
	out := catalogue.Facets{Genres: []string{}, Albums: []catalogue.Album{}}
	rows, err := r.DB.QueryContext(ctx, `SELECT DISTINCT genre FROM catalogue_genres ORDER BY lower(genre),genre`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var genre string
		if err = rows.Scan(&genre); err != nil {
			rows.Close()
			return out, err
		}
		out.Genres = append(out.Genres, genre)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = r.DB.QueryContext(ctx, `SELECT i.source_id,s.name,i.album,count(*) FROM catalogue_items i JOIN sources s ON s.id=i.source_id WHERE i.album<>'' GROUP BY i.source_id,i.album ORDER BY lower(i.album),s.name,i.source_id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var album catalogue.Album
		if err = rows.Scan(&album.SourceID, &album.SourceName, &album.Name, &album.Tracks); err != nil {
			return out, err
		}
		out.Albums = append(out.Albums, album)
	}
	return out, rows.Err()
}
