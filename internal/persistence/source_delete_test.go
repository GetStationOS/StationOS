package persistence

import (
	"context"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
	"testing"
)

func TestSourceDeletionRetainsPlaylistSelections(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	src := Sources{DB: db}
	c := sources.Config{ID: "source", Name: "Music", Kind: "upnp", Endpoint: "http://server/description.xml", Revision: 1}
	if err := src.Save(ctx, c); err != nil {
		t.Fatal(err)
	}
	a := content.AudioItem{ID: content.ID(c.ID, "track"), SourceID: c.ID, SourceItemID: "track", Title: "Track", Album: "Album", Kind: content.Track, Origin: content.Original}
	if err := (Catalogue{DB: db}).Replace(ctx, c, []content.AudioItem{a}); err != nil {
		t.Fatal(err)
	}
	if err := (Stations{DB: db}).Save(ctx, station.Station{ID: "station", Name: "Radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	pools := Playlists{DB: db}
	if err := pools.Save(ctx, playlists.Playlist{ID: "pool", StationID: "station", Name: "Music"}); err != nil {
		t.Fatal(err)
	}
	if err := pools.AddRule(ctx, "pool", playlists.Rule{ID: "album", Kind: "album", SourceID: c.ID, Value: "Album"}); err != nil {
		t.Fatal(err)
	}
	if err := pools.SetIncluded(ctx, "pool", []string{a.ID}, true); err != nil {
		t.Fatal(err)
	}
	if err := src.Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	rules, err := pools.Rules(ctx, "pool")
	if err != nil || len(rules) != 1 || rules[0].SourceID != c.ID {
		t.Fatal("saved rule removed", rules, err)
	}
	result, err := pools.Resolve(ctx, "pool", 0, 100)
	if err != nil || len(result.Entries) != 1 || result.Entries[0].Available || result.Entries[0].Item.Title != "Track" {
		t.Fatal("saved track snapshot not retained as unavailable", result, err)
	}
	if _, err := (Stations{DB: db}).Get(ctx, "station"); err != nil {
		t.Fatal("station removed", err)
	}
}
