package persistence

import (
	"context"
	"database/sql"
	"errors"

	"stationos.local/stationos/internal/sources"
)

type Sources struct{ DB *sql.DB }

var _ sources.Repository = Sources{}

func (r Sources) Save(ctx context.Context, c sources.Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.ID == "" || c.Revision < 1 {
		return errors.New("missing source ID or revision")
	}
	res, err := r.DB.ExecContext(ctx, `INSERT INTO sources(id,name,kind,endpoint,revision) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,kind=excluded.kind,endpoint=excluded.endpoint,revision=excluded.revision WHERE sources.revision=excluded.revision-1`, c.ID, c.Name, c.Kind, c.Endpoint, c.Revision)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sources.ErrConflict
	}
	return nil
}
func (r Sources) Get(ctx context.Context, id string) (sources.Config, error) {
	var c sources.Config
	err := r.DB.QueryRowContext(ctx, `SELECT id,name,kind,endpoint,revision FROM sources WHERE id=?`, id).Scan(&c.ID, &c.Name, &c.Kind, &c.Endpoint, &c.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = sources.ErrNotFound
	}
	return c, err
}
func (r Sources) List(ctx context.Context) ([]sources.Config, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id,name,kind,endpoint,revision FROM sources ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sources.Config{}
	for rows.Next() {
		var c sources.Config
		if err = rows.Scan(&c.ID, &c.Name, &c.Kind, &c.Endpoint, &c.Revision); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
