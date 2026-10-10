package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

func TestMaintenanceCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "stationos.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := run([]string{"-db", path, "-task", "integrity"}, logger); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st := persistence.Stations{DB: db}
	_, err = (station.Service{Repo: st}).Save(ctx, station.Station{Name: "Keep", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	src := persistence.Sources{DB: db}
	c, err := (sources.Service{Repo: src}).Save(ctx, sources.Config{Name: "Music", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	a := content.AudioItem{ID: content.ID(c.ID, "track"), SourceID: c.ID, SourceItemID: "track", Kind: content.Track, Title: "Track", Origin: content.Original}
	if err = (persistence.Catalogue{DB: db}).Replace(ctx, c, []content.AudioItem{a}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = run([]string{"-db", path, "-task", "clear-catalogue"}, logger); err != nil {
		t.Fatal(err)
	}
	db, err = persistence.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stations, err := (persistence.Stations{DB: db}).List(ctx)
	if err != nil || len(stations) != 1 {
		t.Fatal(stations, err)
	}
	items, err := (persistence.Catalogue{DB: db}).Search(ctx, catalogue.Query{Limit: 1})
	if err != nil || items.Total != 0 {
		t.Fatal(items, err)
	}
}
func TestInvalidCommandsAndPublicListen(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Setenv("STATIONOS_ADMIN_PASSWORD", "")
	for _, args := range [][]string{{"-task", "unknown"}, {"-task", "sync"}, {"-task", "sync", "-source", "missing"}, {"-listen", "0.0.0.0:8080"}, {"-listen", "bad"}, {"-unknown"}} {
		path := filepath.Join(t.TempDir(), "test.db")
		if err := run(append([]string{"-db", path}, args...), logger); err == nil {
			t.Fatal("invalid command accepted", args)
		}
	}
}
