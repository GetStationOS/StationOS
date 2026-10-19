package persistence

import (
	"context"
	"errors"
	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
	"testing"
)

func TestStationSourcesConstrainCataloguePlaylistsAndBroadcast(t *testing.T) {
	ctx := context.Background()
	db := database(t)
	sr := Sources{DB: db}
	cat := Catalogue{DB: db}
	sts := Stations{DB: db}
	pools := Playlists{DB: db}
	var tracks []content.AudioItem
	for _, id := range []string{"minim", "gerbera"} {
		c := sources.Config{ID: id, Name: id, Kind: "upnp", Endpoint: "http://server/desc", Revision: 1}
		if err := sr.Save(ctx, c); err != nil {
			t.Fatal(err)
		}
		a := item(c, "same-song", "Same song")
		a.Genre = "Jazz"
		if err := cat.Replace(ctx, c, []content.AudioItem{a}); err != nil {
			t.Fatal(err)
		}
		tracks = append(tracks, a)
	}
	st := station.Station{ID: "radio", Name: "Radio", Timezone: "UTC", SourceIDs: []string{"minim", "gerbera"}}
	if err := sts.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := pools.Save(ctx, playlists.Playlist{ID: "pool", StationID: st.ID, Name: "Jazz"}); err != nil {
		t.Fatal(err)
	}
	if err := pools.AddRule(ctx, "pool", playlists.Rule{ID: "jazz", Kind: "genre", Value: "Jazz"}); err != nil {
		t.Fatal(err)
	}
	if err := pools.SetIncluded(ctx, "pool", []string{tracks[0].ID, tracks[1].ID}, true); err != nil {
		t.Fatal(err)
	}
	assertPool := func(wantAvailable, wantMissing int) {
		t.Helper()
		result, err := pools.Resolve(ctx, "pool", 0, 100)
		if err != nil || result.Total != wantAvailable+wantMissing || result.Unavailable != wantMissing {
			t.Fatal("resolve", result, err)
		}
		counts, err := pools.Availability(ctx, "pool")
		if err != nil || counts.Total != result.Total || counts.Unavailable != result.Unavailable {
			t.Fatal("availability", counts, err)
		}
		pool, err := (Broadcast{DB: db}).Pool(ctx, "pool")
		if err != nil || len(pool) != wantAvailable {
			t.Fatal("playout pool", pool, err)
		}
	}
	other := station.Station{ID: "other-radio", Name: "Other radio", Timezone: "UTC", SourceIDs: []string{"minim"}}
	if err := sts.Save(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := pools.Save(ctx, playlists.Playlist{ID: "other-pool", StationID: other.ID, Name: "Other jazz"}); err != nil {
		t.Fatal(err)
	}
	if err := pools.AddRule(ctx, "other-pool", playlists.Rule{ID: "other-jazz", Kind: "genre", Value: "Jazz"}); err != nil {
		t.Fatal(err)
	}
	assertPool(2, 0)
	st.SourceIDs = []string{"gerbera"}
	if err := sts.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	assertPool(1, 1)
	page, err := cat.Search(ctx, catalogue.Query{StationID: st.ID, Limit: 100})
	if err != nil || page.Total != 1 || page.Items[0].SourceID != "gerbera" {
		t.Fatal("station catalogue", page, err)
	}
	page, err = cat.Search(ctx, catalogue.Query{StationID: st.ID, SourceID: "minim", Limit: 100})
	if err != nil || page.Total != 0 {
		t.Fatal("source filter bypass", page, err)
	}
	facets, err := cat.Facets(ctx, st.ID)
	if err != nil || len(facets.Albums) != 1 || facets.Albums[0].SourceID != "gerbera" {
		t.Fatal("station facets", facets, err)
	}
	if err := pools.SetIncluded(ctx, "pool", []string{tracks[0].ID}, true); !errors.Is(err, playlists.ErrUnavailable) {
		t.Fatal("disabled source could be included", err)
	}
	if err := pools.AddRule(ctx, "pool", playlists.Rule{ID: "disabled", Kind: "album", Value: "Album", SourceID: "minim"}); !errors.Is(err, playlists.ErrUnavailable) {
		t.Fatal("disabled album accepted", err)
	}
	st.SourceIDs = []string{}
	if err := sts.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	assertPool(0, 2)
	otherPool, err := (Broadcast{DB: db}).Pool(ctx, "other-pool")
	if err != nil || len(otherPool) != 1 || otherPool[0].SourceID != "minim" {
		t.Fatal("source choices leaked across stations", otherPool, err)
	}

	// Ordinary updates which omit source choices must retain explicit empty selection.
	st.SourceIDs = nil
	st.Name = "Updated"
	if err := sts.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	assertPool(0, 2)
	got, err := sts.Get(ctx, st.ID)
	if err != nil || got.SourceIDs == nil || len(got.SourceIDs) != 0 {
		t.Fatal("empty selection not round-tripped", got, err)
	}
	// Invalid choices roll back station fields and all selection changes together.
	st.SourceIDs = []string{"gerbera", "missing"}
	st.Name = "Must roll back"
	if err := sts.Save(ctx, st); !errors.Is(err, station.ErrInvalidSource) {
		t.Fatal(err)
	}
	got, err = sts.Get(ctx, st.ID)
	if err != nil || got.Name != "Updated" || len(got.SourceIDs) != 0 {
		t.Fatal("invalid save was not atomic", got, err)
	}
	st.SourceIDs = []string{"minim", "gerbera"}
	if err := sts.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	assertPool(2, 0)
	c := sources.Config{ID: "later", Name: "Later", Kind: "upnp", Endpoint: "http://later/desc", Revision: 1}
	if err := sr.Save(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err = sts.Get(ctx, st.ID)
	if err != nil || len(got.SourceIDs) != 2 {
		t.Fatal("new source silently enabled", got, err)
	}
	if err := sr.Delete(ctx, "minim"); err != nil {
		t.Fatal(err)
	}
	got, err = sts.Get(ctx, st.ID)
	if err != nil || len(got.SourceIDs) != 1 || got.SourceIDs[0] != "gerbera" {
		t.Fatal("source deletion left stale choices", got, err)
	}
}
