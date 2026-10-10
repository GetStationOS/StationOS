package persistence

import (
	"context"
	"database/sql"
	"errors"
	"stationos.local/stationos/internal/mixer"
	"stationos.local/stationos/internal/station"
)

type Mixer struct{ DB *sql.DB }

func (r Mixer) Get(ctx context.Context, id string) (mixer.Levels, error) {
	var v mixer.Levels
	err := r.DB.QueryRowContext(ctx, `SELECT COALESCE(m.music_volume,100),COALESCE(m.dj_volume,100) FROM stations s LEFT JOIN station_mixer m ON m.station_id=s.id WHERE s.id=?`, id).Scan(&v.Music, &v.DJ)
	if errors.Is(err, sql.ErrNoRows) {
		err = station.ErrNotFound
	}
	return v, err
}
func (r Mixer) Save(ctx context.Context, id string, v mixer.Levels) error {
	if err := v.Validate(); err != nil {
		return err
	}
	result, err := r.DB.ExecContext(ctx, `INSERT INTO station_mixer(station_id,music_volume,dj_volume) SELECT id,?,? FROM stations WHERE id=? ON CONFLICT(station_id) DO UPDATE SET music_volume=excluded.music_volume,dj_volume=excluded.dj_volume`, v.Music, v.DJ, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return station.ErrNotFound
	}
	return err
}
