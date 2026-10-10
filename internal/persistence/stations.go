package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"stationos.local/stationos/internal/station"
)

type Stations struct{ DB *sql.DB }

var _ station.Repository = Stations{}

func (r Stations) Save(ctx context.Context, s station.Station) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if s.ID == "" {
		return errors.New("missing station ID")
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.StreamSlug == "" {
		err := tx.QueryRowContext(ctx, `SELECT stream_slug FROM stations WHERE id=?`, s.ID).Scan(&s.StreamSlug)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if s.StreamSlug == "" {
			s.StreamSlug = station.Slug(s.Name)
			base := s.StreamSlug[:min(len(s.StreamSlug), 40)] + "-" + s.ID
			for n := 1; ; n++ {
				var count int
				if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM stations WHERE stream_slug=? AND id<>?`, s.StreamSlug, s.ID).Scan(&count); err != nil {
					return err
				}
				if count == 0 {
					break
				}
				s.StreamSlug = base
				if n > 1 {
					s.StreamSlug = fmt.Sprintf("%s-%d", base, n)
				}
			}
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO stations(id,name,description,timezone,locale,stream_slug) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,timezone=excluded.timezone,locale=excluded.locale,stream_slug=excluded.stream_slug`, s.ID, s.Name, s.Description, s.Timezone, s.Locale, s.StreamSlug)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: stations.stream_slug") {
		return station.ErrSlugTaken
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r Stations) ByStreamSlug(ctx context.Context, slug string) (station.Station, error) {
	var s station.Station
	err := r.DB.QueryRowContext(ctx, `SELECT id,name,description,timezone,locale,stream_slug FROM stations WHERE stream_slug=? AND stream_slug<>''`, slug).Scan(&s.ID, &s.Name, &s.Description, &s.Timezone, &s.Locale, &s.StreamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		err = station.ErrNotFound
	}
	return s, err
}
func (r Stations) Get(ctx context.Context, id string) (station.Station, error) {
	var s station.Station
	err := r.DB.QueryRowContext(ctx, `SELECT id,name,description,timezone,locale,stream_slug FROM stations WHERE id=?`, id).Scan(&s.ID, &s.Name, &s.Description, &s.Timezone, &s.Locale, &s.StreamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		err = station.ErrNotFound
	}
	return s, err
}
func (r Stations) List(ctx context.Context) ([]station.Station, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id,name,description,timezone,locale,stream_slug FROM stations ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []station.Station{}
	for rows.Next() {
		var s station.Station
		if err = rows.Scan(&s.ID, &s.Name, &s.Description, &s.Timezone, &s.Locale, &s.StreamSlug); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Delete commits removal of a station and its playlists together.
func (r Stations) Delete(ctx context.Context, id string) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM station_shows WHERE station_id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM playlists WHERE station_id=?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM stations WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return station.ErrNotFound
	}
	return tx.Commit()
}
