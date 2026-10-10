// Package persistence contains SQLite adapters; domain packages do not import it.
package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"stationos.local/stationos/internal/station"
	"stationos.local/stationos/migrations"
)

func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" || strings.HasPrefix(path, "file:") {
		return nil, errors.New("database must be a filesystem path or :memory:")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, err
	}
	if err = migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
func migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, checksum TEXT NOT NULL)`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT version,checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return err
	}
	applied := map[int]string{}
	for rows.Next() {
		var v int
		var sum string
		if err = rows.Scan(&v, &sum); err != nil {
			rows.Close()
			return err
		}
		applied[v] = sum
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return err
	}
	known := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		v, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil || v != known+1 {
			return errors.New("migration sequence is invalid")
		}
		known = v
		b, err := migrations.Files.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		sum := hex.EncodeToString(h[:])
		if old, ok := applied[v]; ok {
			if old != sum {
				return fmt.Errorf("migration %d checksum changed", v)
			}
			continue
		}
		if _, err = tx.ExecContext(ctx, string(b)); err != nil {
			return fmt.Errorf("migration %d: %w", v, err)
		}
		if v == 5 {
			rows, err := tx.QueryContext(ctx, `SELECT id,name FROM stations ORDER BY id`)
			if err != nil {
				return err
			}
			type pair struct{ id, name string }
			var items []pair
			for rows.Next() {
				var p pair
				if err = rows.Scan(&p.id, &p.name); err != nil {
					rows.Close()
					return err
				}
				items = append(items, p)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			used := map[string]bool{}
			for _, p := range items {
				slug := station.Slug(p.name)
				if used[slug] {
					slug = slug[:min(len(slug), 40)] + "-" + p.id
				}
				used[slug] = true
				if _, err = tx.ExecContext(ctx, `UPDATE stations SET stream_slug=? WHERE id=?`, slug, p.id); err != nil {
					return err
				}
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,checksum) VALUES(?,?)`, v, sum); err != nil {
			return err
		}
	}
	for v := range applied {
		if v > known || v < 1 {
			return errors.New("database schema is not supported by this binary")
		}
	}
	return tx.Commit()
}
func Integrity(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("database integrity check failed")
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("foreign key integrity check failed")
	}
	return rows.Err()
}
