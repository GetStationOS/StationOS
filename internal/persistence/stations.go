package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stations WHERE id=?)`, s.ID).Scan(&exists); err != nil {
		return err
	}
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
	_, err = tx.ExecContext(ctx, `INSERT INTO stations(id,name,description,timezone,locale,stream_slug,repeat_minutes,visibility) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,timezone=excluded.timezone,locale=excluded.locale,stream_slug=excluded.stream_slug,repeat_minutes=excluded.repeat_minutes,visibility=excluded.visibility`, s.ID, s.Name, s.Description, s.Timezone, s.Locale, s.StreamSlug, s.RepeatIntervalMinutes(), s.Publication())
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: stations.stream_slug") {
		return station.ErrSlugTaken
	}
	if err != nil {
		return err
	}
	if s.SourceIDs != nil {
		for _, id := range s.SourceIDs {
			var valid bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sources WHERE id=?)`, id).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return station.ErrInvalidSource
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM station_sources WHERE station_id=?`, s.ID); err != nil {
			return err
		}
		for _, id := range s.SourceIDs {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO station_sources(station_id,source_id) VALUES(?,?)`, s.ID, id); err != nil {
				return err
			}
		}
	} else if !exists {
		if _, err := tx.ExecContext(ctx, `INSERT INTO station_sources(station_id,source_id) SELECT ?,id FROM sources`, s.ID); err != nil {
			return err
		}
	}
	if s.Logo != nil {
		if s.Logo.Clear {
			_, err = tx.ExecContext(ctx, `DELETE FROM station_logos WHERE station_id=?`, s.ID)
		} else {
			hash := sha256.Sum256(s.Logo.Data)
			_, err = tx.ExecContext(ctx, `INSERT INTO station_logos(station_id,data,version) VALUES(?,?,?) ON CONFLICT(station_id) DO UPDATE SET data=excluded.data,version=excluded.version`, s.ID, s.Logo.Data, hex.EncodeToString(hash[:]))
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r Stations) ByStreamSlug(ctx context.Context, slug string) (station.Station, error) {
	var s station.Station
	var sourceJSON string
	err := r.DB.QueryRowContext(ctx, `SELECT id,name,description,timezone,locale,stream_slug,repeat_minutes,COALESCE((SELECT version FROM station_logos WHERE station_id=stations.id),''),visibility,(SELECT json_group_array(source_id) FROM station_sources WHERE station_id=stations.id) FROM stations WHERE stream_slug=? AND stream_slug<>''`, slug).Scan(&s.ID, &s.Name, &s.Description, &s.Timezone, &s.Locale, &s.StreamSlug, &s.RepeatMinutes, &s.LogoVersion, &s.Visibility, &sourceJSON)
	if errors.Is(err, sql.ErrNoRows) {
		err = station.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(sourceJSON), &s.SourceIDs)
	}
	return s, err
}
func (r Stations) Get(ctx context.Context, id string) (station.Station, error) {
	var s station.Station
	var sourceJSON string
	err := r.DB.QueryRowContext(ctx, `SELECT id,name,description,timezone,locale,stream_slug,repeat_minutes,COALESCE((SELECT version FROM station_logos WHERE station_id=stations.id),''),visibility,(SELECT json_group_array(source_id) FROM station_sources WHERE station_id=stations.id) FROM stations WHERE id=?`, id).Scan(&s.ID, &s.Name, &s.Description, &s.Timezone, &s.Locale, &s.StreamSlug, &s.RepeatMinutes, &s.LogoVersion, &s.Visibility, &sourceJSON)
	if errors.Is(err, sql.ErrNoRows) {
		err = station.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(sourceJSON), &s.SourceIDs)
	}
	return s, err
}
func (r Stations) List(ctx context.Context) ([]station.Station, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id,name,description,timezone,locale,stream_slug,repeat_minutes,COALESCE((SELECT version FROM station_logos WHERE station_id=stations.id),''),visibility,(SELECT json_group_array(source_id) FROM station_sources WHERE station_id=stations.id) FROM stations ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []station.Station{}
	for rows.Next() {
		var s station.Station
		var sourceJSON string
		if err = rows.Scan(&s.ID, &s.Name, &s.Description, &s.Timezone, &s.Locale, &s.StreamSlug, &s.RepeatMinutes, &s.LogoVersion, &s.Visibility, &sourceJSON); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(sourceJSON), &s.SourceIDs); err != nil {
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

func (r Stations) Logo(ctx context.Context, id string) ([]byte, error) {
	var data []byte
	err := r.DB.QueryRowContext(ctx, `SELECT data FROM station_logos WHERE station_id=?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		err = station.ErrNotFound
	}
	return data, err
}
