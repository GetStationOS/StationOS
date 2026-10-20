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
	return ` FROM catalogue_items i JOIN sources s ON s.id=i.source_id WHERE (?='' OR i.id=?) AND
 (?='' OR EXISTS(SELECT 1 FROM station_sources ss WHERE ss.station_id=? AND ss.source_id=i.source_id)) AND
 (?='' OR i.source_id=?) AND (?='' OR EXISTS(SELECT 1 FROM catalogue_genres g WHERE g.content_id=i.id AND g.genre=?))
 AND (?='' OR i.album=?)
 AND (?='' OR EXISTS(SELECT 1 FROM catalogue_credits c WHERE c.content_id=i.id AND c.role='artist' AND c.name=?))
 AND (?='' OR EXISTS(SELECT 1 FROM catalogue_credits c WHERE c.content_id=i.id AND c.role='composer' AND c.name=?))
 AND (?=0 OR coalesce(json_extract(i.metadata,'$.year'),0)>=?)
 AND (?=0 OR (coalesce(json_extract(i.metadata,'$.year'),0)>0 AND json_extract(i.metadata,'$.year')<=?)) AND
 (instr(lower(i.title),lower(?))>0 OR instr(lower(i.artist),lower(?))>0 OR instr(lower(i.album),lower(?))>0 OR instr(lower(COALESCE(json_extract(i.metadata,'$.genre'),'')),lower(?))>0)`,
		[]any{q.ItemID, q.ItemID, q.StationID, q.StationID, q.SourceID, q.SourceID, q.Genre, q.Genre, q.Album, q.Album, q.Artist, q.Artist, q.Composer, q.Composer, q.YearFrom, q.YearFrom, q.YearTo, q.YearTo, q.Text, q.Text, q.Text, q.Text}
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
func (r Catalogue) Facets(ctx context.Context, stationIDs ...string) (catalogue.Facets, error) {
	stationID := ""
	if len(stationIDs) > 0 {
		stationID = stationIDs[0]
	}
	scope := `(?='' OR EXISTS(SELECT 1 FROM station_sources ss WHERE ss.station_id=? AND ss.source_id=i.source_id))`
	args := []any{stationID, stationID}
	out := catalogue.Facets{Genres: []string{}, Albums: []catalogue.Album{}}
	rows, err := r.DB.QueryContext(ctx, `SELECT DISTINCT genre FROM catalogue_genres g JOIN catalogue_items i ON i.id=g.content_id WHERE `+scope+` ORDER BY lower(genre),genre`, args...)
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
	rows, err = r.DB.QueryContext(ctx, `SELECT i.source_id,s.name,i.album,count(*) FROM catalogue_items i JOIN sources s ON s.id=i.source_id WHERE `+scope+` AND i.album<>'' GROUP BY i.source_id,i.album ORDER BY lower(i.album),s.name,i.source_id`, args...)
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
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, facet := range []struct {
		query string
		dest  *[]string
	}{
		{`SELECT DISTINCT name FROM catalogue_credits c JOIN catalogue_items i ON i.id=c.content_id WHERE ` + scope + ` AND role='artist' ORDER BY lower(name),name`, &out.Artists},
		{`SELECT DISTINCT name FROM catalogue_credits c JOIN catalogue_items i ON i.id=c.content_id WHERE ` + scope + ` AND role='composer' ORDER BY lower(name),name`, &out.Composers},
		{`SELECT DISTINCT CAST(json_extract(metadata,'$.year') AS TEXT) FROM catalogue_items i WHERE ` + scope + ` AND json_extract(metadata,'$.year') BETWEEN 1 AND 9999 ORDER BY CAST(json_extract(metadata,'$.year') AS INTEGER)`, &out.Years},
		{`SELECT DISTINCT CAST(CAST(json_extract(metadata,'$.year') AS INTEGER)/10*10 AS TEXT) FROM catalogue_items i WHERE ` + scope + ` AND json_extract(metadata,'$.year') BETWEEN 10 AND 9999 ORDER BY CAST(json_extract(metadata,'$.year') AS INTEGER)/10`, &out.Decades},
	} {
		rs, e := r.DB.QueryContext(ctx, facet.query, args...)
		if e != nil {
			return out, e
		}
		for rs.Next() {
			var value string
			if e = rs.Scan(&value); e != nil {
				rs.Close()
				return out, e
			}
			*facet.dest = append(*facet.dest, value)
		}
		e = rs.Err()
		rs.Close()
		if e != nil {
			return out, e
		}
	}
	return out, nil
}
