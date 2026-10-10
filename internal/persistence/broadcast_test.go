package persistence

import (
	"context"
	"errors"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/station"
	"testing"
)

func TestBroadcastAssignmentAndCascades(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	source := seed(t, db)
	st := Stations{DB: db}
	pr := Playlists{DB: db}
	br := Broadcast{DB: db}
	st.Save(ctx, station.Station{ID: "other", Name: "Other", Timezone: "UTC"})
	if err := pr.Save(ctx, playlists.Playlist{ID: "pool", StationID: "station", Name: "Pool"}); err != nil {
		t.Fatal(err)
	}
	if err := pr.Save(ctx, playlists.Playlist{ID: "foreign", StationID: "other", Name: "Foreign"}); err != nil {
		t.Fatal(err)
	}
	if err := br.Assign(ctx, "station", "foreign"); !errors.Is(err, playlists.ErrInvalid) {
		t.Fatal("cross-station assignment", err)
	}
	if err := br.Assign(ctx, "station", "pool"); err != nil {
		t.Fatal(err)
	}
	a := item(source, "one", "First")
	a.Genre = "Jazz"
	if err := (Catalogue{DB: db}).Replace(ctx, source, []content.AudioItem{a}); err != nil {
		t.Fatal(err)
	}
	if err := pr.AddRule(ctx, "pool", playlists.Rule{ID: "r", Kind: "genre", Value: "Jazz"}); err != nil {
		t.Fatal(err)
	}
	pool, err := br.Pool(ctx, "pool")
	if err != nil || len(pool) != 1 {
		t.Fatal(pool, err)
	}
	b := item(source, "two", "Second")
	b.Genre = "Jazz"
	(Catalogue{DB: db}).Replace(ctx, source, []content.AudioItem{a, b})
	pool, err = br.Pool(ctx, "pool")
	if err != nil || len(pool) != 2 {
		t.Fatal("dynamic pool", pool, err)
	}
	pr.SetIncluded(ctx, "pool", []string{b.ID}, false)
	pool, err = br.Pool(ctx, "pool")
	if err != nil || len(pool) != 1 || pool[0].ID != a.ID {
		t.Fatal("exclusion ignored", pool, err)
	}
	if err := pr.Delete(ctx, "pool"); err != nil {
		t.Fatal(err)
	}
	assigned, err := br.Assignment(ctx, "station")
	if err != nil || assigned != "" {
		t.Fatal("deleted playlist remains assigned", assigned, err)
	}
	st.Delete(ctx, "station")
	if _, err = br.Assignment(ctx, "station"); !errors.Is(err, station.ErrNotFound) {
		t.Fatal(err)
	}
}
func TestStableEditableUniqueStreamAddresses(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	repo := Stations{DB: db}
	service := station.Service{Repo: repo}
	a, err := service.Save(ctx, station.Station{Name: "Reni Radio!", Timezone: "UTC"})
	if err != nil || a.StreamSlug != "reni-radio" {
		t.Fatal(a, err)
	}
	b, err := service.Save(ctx, station.Station{Name: "Reni Radio!", Timezone: "UTC"})
	if err != nil || b.StreamSlug == a.StreamSlug {
		t.Fatal("duplicate default", b, err)
	}
	a.Name = "New name"
	a.StreamSlug = ""
	a, err = service.Save(ctx, a)
	if err != nil || a.StreamSlug != "reni-radio" {
		t.Fatal("rename changed presets", a, err)
	}
	a.StreamSlug = "jazz-24"
	a, err = service.Save(ctx, a)
	if err != nil || a.StreamSlug != "jazz-24" {
		t.Fatal(a, err)
	}
	b.StreamSlug = "jazz-24"
	if _, err = service.Save(ctx, b); !errors.Is(err, station.ErrSlugTaken) {
		t.Fatal("duplicate override", err)
	}
	a.StreamSlug = "../bad"
	if _, err = service.Save(ctx, a); err == nil {
		t.Fatal("invalid address accepted")
	}
	if err := Integrity(ctx, db); err != nil {
		t.Fatal(err)
	}
}
