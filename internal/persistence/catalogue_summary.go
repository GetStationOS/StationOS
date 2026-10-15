package persistence

import (
	"context"
	"stationos.local/stationos/internal/catalogue"
)

var _ catalogue.SummaryRepository = Catalogue{}

// Aggregate in SQLite, inside one snapshot. No recording rows or metadata blobs
// are materialised in Go or sent to the model to describe the library.
func (r Catalogue) Summary(ctx context.Context, q catalogue.SummaryQuery) (catalogue.Summary, error) {
	out := catalogue.Summary{CatalogueSummary: true, Scope: q, TopGenres: []catalogue.Coverage{}, TopArtists: []catalogue.Coverage{}}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	filter, args := catalogueFilter(catalogue.Query{SourceID: q.SourceID, Genre: q.Genre, Text: q.Text})
	genre := "COALESCE(json_extract(i.metadata,'$.genre'),'')"
	duration := "json_extract(i.metadata,'$.duration_ns')"
	err = tx.QueryRowContext(ctx, `SELECT count(*),count(DISTINCT CASE WHEN i.album<>'' THEN i.source_id||char(31)||i.album END),count(DISTINCT NULLIF(i.artist,'')),count(DISTINCT NULLIF(`+genre+`,'')),count(DISTINCT i.source_id),COALESCE(sum(CASE WHEN i.artist='' THEN 1 ELSE 0 END),0),COALESCE(sum(CASE WHEN `+genre+`='' THEN 1 ELSE 0 END),0),COALESCE(sum(CASE WHEN `+duration+` IS NULL THEN 1 ELSE 0 END),0),CAST(COALESCE(sum(`+duration+`),0)/1000000000 AS INTEGER)`+filter, args...).Scan(&out.Tracks, &out.Albums, &out.Artists, &out.Genres, &out.Sources, &out.UnknownArtists, &out.UnknownGenres, &out.UnknownDurations, &out.KnownSeconds)
	if err != nil {
		return out, err
	}
	for _, group := range []struct {
		expression  string
		destination *[]catalogue.Coverage
	}{{genre, &out.TopGenres}, {"i.artist", &out.TopArtists}} {
		bounded := append(append([]any{}, args...), q.Limit)
		rows, e := tx.QueryContext(ctx, `SELECT `+group.expression+`,count(*)`+filter+` AND `+group.expression+`<>'' GROUP BY `+group.expression+` ORDER BY count(*) DESC,lower(`+group.expression+`),`+group.expression+` LIMIT ?`, bounded...)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var item catalogue.Coverage
			if e = rows.Scan(&item.Name, &item.Tracks); e != nil {
				rows.Close()
				return out, e
			}
			*group.destination = append(*group.destination, item)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	return out, tx.Commit()
}
