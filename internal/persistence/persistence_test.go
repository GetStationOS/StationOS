package persistence

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

func database(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func seed(t *testing.T, db *sql.DB) sources.Config {
	t.Helper()
	ctx := context.Background()
	if err := (Stations{db}).Save(ctx, station.Station{ID: "station", Name: "Independent", Description: "Keep me", Timezone: "UTC", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	c := sources.Config{ID: "source", Name: "Music", Kind: "upnp", Endpoint: "http://server:9790/desc", Revision: 1}
	if err := (Sources{db}).Save(ctx, c); err != nil {
		t.Fatal(err)
	}
	return c
}
func item(c sources.Config, id, title string) content.AudioItem {
	return content.AudioItem{ID: content.ID(c.ID, id), SourceID: c.ID, SourceItemID: id, Title: title, Artist: "Artist", Album: "Album", Kind: content.Track, Origin: content.Original, Resources: []content.Resource{{URL: "http://server:9790/audio", MIME: "audio/mpeg"}}}
}
func TestMigrationsAndForeignKeys(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	if err := migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := Integrity(ctx, db); err != nil {
		t.Fatal(err)
	}
	var on int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Fatal(on, err)
	}
	if _, err := db.Exec(`INSERT INTO playlists(id,station_id,name,description) VALUES('bad','missing','Bad','')`); err == nil {
		t.Fatal("playlist station foreign key not enforced")
	}
	if _, err := db.Exec(`UPDATE schema_migrations SET checksum='changed'`); err != nil {
		t.Fatal(err)
	}
	if migrate(ctx, db) == nil {
		t.Fatal("modified migration accepted")
	}
}
func TestNewerSchemaRejected(t *testing.T) {
	db := database(t)
	if _, err := db.Exec(`INSERT INTO schema_migrations VALUES(99,'future')`); err != nil {
		t.Fatal(err)
	}
	if migrate(context.Background(), db) == nil {
		t.Fatal("future schema accepted")
	}
}
func TestReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	seed(t, db)
	db.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	v, err := (Stations{db}).Get(ctx, "station")
	if err != nil || v.Description != "Keep me" {
		t.Fatal(v, err)
	}
}
func TestRepositoriesAndCatalogueRebuild(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	c := seed(t, db)
	cat := Catalogue{db}
	a := item(c, "1", "Zebra")
	b := item(c, "2", "Apple")
	if err := cat.Replace(ctx, c, []content.AudioItem{a, b}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := cat.Replace(ctx, c, []content.AudioItem{b, a}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := cat.Search(ctx, catalogue.Query{Limit: 1})
	if err != nil || got.Total != 2 || got.Items[0].Title != "Apple" {
		t.Fatal(got, err)
	}
	got, err = cat.Search(ctx, catalogue.Query{Text: "' OR 1=1 --", Limit: 50})
	if err != nil || got.Total != 0 {
		t.Fatal(got, err)
	}
	got, err = cat.Search(ctx, catalogue.Query{Text: "ARTIST", Limit: 50})
	if err != nil || got.Total != 2 {
		t.Fatal(got, err)
	}
	got, err = cat.Search(ctx, catalogue.Query{SourceID: "other", Limit: 50})
	if err != nil || got.Total != 0 {
		t.Fatal(got, err)
	}
	if err = cat.Replace(ctx, c, []content.AudioItem{b, b}); err == nil {
		t.Fatal("duplicate snapshot accepted")
	}
	got, _ = cat.Search(ctx, catalogue.Query{Limit: 50})
	if got.Total != 2 {
		t.Fatal("failed transaction lost catalogue")
	}
	if err = cat.Replace(ctx, c, []content.AudioItem{b}); err != nil {
		t.Fatal(err)
	}
	got, _ = cat.Search(ctx, catalogue.Query{Limit: 50})
	if got.Total != 1 || got.Items[0].ID != b.ID {
		t.Fatal("orphan cleanup or stable ID failed")
	}
	if err = cat.Clear(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	v, err := (Stations{db}).Get(ctx, "station")
	if err != nil || v.Description != "Keep me" {
		t.Fatal("configuration lost", err)
	}
	saved, err := (Sources{db}).Get(ctx, c.ID)
	if err != nil || saved.Endpoint != c.Endpoint {
		t.Fatal(saved, err)
	}
	if err = cat.Replace(ctx, c, nil); err != nil {
		t.Fatal(err)
	}
	if err = Integrity(ctx, db); err != nil {
		t.Fatal(err)
	}
}
func TestSourceRevisionAndRepositoryErrors(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	c := seed(t, db)
	next := c
	next.Revision++
	next.Endpoint = "http://changed:9790/desc"
	if err := (Sources{db}).Save(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err := (Sources{db}).Save(ctx, c); !errors.Is(err, sources.ErrConflict) {
		t.Fatal(err)
	}
	if err := (Catalogue{db}).Replace(ctx, c, nil); !errors.Is(err, sources.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := (Stations{db}).Get(ctx, "missing"); !errors.Is(err, station.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := (Sources{db}).Get(ctx, "missing"); !errors.Is(err, sources.ErrNotFound) {
		t.Fatal(err)
	}
	stations, err := (Stations{db}).List(ctx)
	if err != nil || len(stations) != 1 {
		t.Fatal(stations, err)
	}
	ss, err := (Sources{db}).List(ctx)
	if err != nil || len(ss) != 1 {
		t.Fatal(ss, err)
	}
	if _, err := (Catalogue{db}).Search(ctx, catalogue.Query{}); err == nil {
		t.Fatal("invalid query accepted")
	}
	db.Close()
	if _, err := (Stations{db}).List(ctx); err == nil {
		t.Fatal("closed database accepted")
	}
	if err := Integrity(ctx, db); err == nil {
		t.Fatal("closed database passed integrity")
	}
}
func TestIdentityRollback(t *testing.T) {
	db := database(t)
	c := seed(t, db)
	cat := Catalogue{db}
	a := item(c, "1", "Track")
	if err := cat.Replace(context.Background(), c, []content.AudioItem{a}); err != nil {
		t.Fatal(err)
	}
	a.ID = "wrong"
	if cat.Replace(context.Background(), c, []content.AudioItem{a}) == nil {
		t.Fatal("bad identity accepted")
	}
	got, _ := cat.Search(context.Background(), catalogue.Query{Limit: 1})
	if got.Total != 1 {
		t.Fatal("rollback failed")
	}
}

func TestRebuildPreservesOtherSources(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	first := seed(t, db)
	second := first
	second.ID = "second"
	second.Name = "Second library"
	if err := (Sources{db}).Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	cat := Catalogue{db}
	if err := cat.Replace(ctx, first, []content.AudioItem{item(first, "one", "First")}); err != nil {
		t.Fatal(err)
	}
	if err := cat.Replace(ctx, second, []content.AudioItem{item(second, "two", "Second")}); err != nil {
		t.Fatal(err)
	}
	if err := cat.Clear(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	result, err := cat.Search(ctx, catalogue.Query{Limit: 50})
	if err != nil || result.Total != 1 || result.Items[0].SourceID != second.ID {
		t.Fatal(result, err)
	}
	sources, err := (Sources{db}).List(ctx)
	if err != nil || len(sources) != 2 {
		t.Fatal(sources, err)
	}
}

func TestRecordingCreditsSurviveCatalogueStorage(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	c := seed(t, db)
	a := item(c, "recording", "Concerto")
	a.Artist = "Soloist; Orchestra"
	a.Composer = "Composer"
	a.Conductor = "Conductor"
	a.AlbumArtist = "Album artist"
	a.Creator = "Source creator"
	cat := Catalogue{db}
	if err := cat.Replace(ctx, c, []content.AudioItem{a}); err != nil {
		t.Fatal(err)
	}
	result, err := cat.Search(ctx, catalogue.Query{Limit: 10})
	if err != nil || len(result.Items) != 1 {
		t.Fatal(result, err)
	}
	got := result.Items[0]
	if got.Artist != a.Artist || got.Composer != a.Composer || got.Conductor != a.Conductor || got.AlbumArtist != a.AlbumArtist || got.Creator != a.Creator {
		t.Fatal("recording credits lost", got)
	}
}
