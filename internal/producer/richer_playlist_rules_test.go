package producer_test

import (
	"context"
	"encoding/json"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
)

func TestAssistantRichRulesAndMeasuredExploration(t *testing.T) {
	_, r, stations, pools, _, _ := fixture(t)
	ctx := context.Background()
	owner := call(t, r, "save_station", map[string]string{"name": "Jazz Radio"}).Data.(station.Station)
	pool := call(t, r, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Music"}).Data.(playlists.Playlist)
	source := sources.Config{ID: "library", Name: "Library", Kind: "upnp", Endpoint: "http://server/desc", Revision: 1}
	if err := (persistence.Sources{DB: stations.DB}).Save(ctx, source); err != nil {
		t.Fatal(err)
	}
	item := content.AudioItem{ID: content.ID(source.ID, "a"), SourceID: source.ID, SourceItemID: "a", Title: "A", Artist: "Miles Davis; John Coltrane", Composer: "Writer", Genre: "Jazz", Year: 1959, Kind: content.Track, Origin: content.Original}
	if err := (persistence.Catalogue{DB: stations.DB}).Replace(ctx, source, []content.AudioItem{item}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ kind, want string }{{"artists", "Miles Davis"}, {"composers", "Writer"}, {"years", "1959"}, {"decades", "1950"}} {
		result := call(t, r, "explore_music", map[string]any{"kind": tc.kind, "limit": 20})
		raw, _ := json.Marshal(result.Data)
		if !strings.Contains(string(raw), tc.want) {
			t.Fatal(string(raw))
		}
	}
	result := call(t, r, "search_music", map[string]any{"artist": "John Coltrane", "composer": "Writer", "year_from": 1950, "year_to": 1959})
	raw, _ := json.Marshal(result.Data)
	if !strings.Contains(string(raw), `"Year":1959`) || !strings.Contains(string(raw), `"total":1`) {
		t.Fatal(string(raw))
	}
	call(t, r, "replace_playlist_rules", map[string]any{"playlist_id": pool.ID, "rules": []any{
		map[string]string{"kind": "artist", "value": "Miles Davis"}, map[string]string{"kind": "genre", "value": "Jazz"}, map[string]string{"kind": "decade", "value": "1950"},
	}})
	rules, err := pools.Rules(ctx, pool.ID)
	if err != nil || len(rules) != 3 {
		t.Fatal(rules, err)
	}
	found, err := pools.Resolve(ctx, pool.ID, 0, 100)
	if err != nil || found.Total != 1 {
		t.Fatal(found, err)
	}
	def, ok := r.Find("add_playlist_rule")
	if !ok {
		t.Fatal("missing tool")
	}
	raw, _ = json.Marshal(def)
	for _, want := range []string{"composer", "decade", "AND"} {
		if !strings.Contains(string(raw), want) {
			t.Fatal(want, string(raw))
		}
	}
	props := def.Parameters.(map[string]any)["properties"].(map[string]any)["rule"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props["filter"]; ok {
		t.Fatal("redundant filter schema advertised")
	}
	call(t, r, "replace_playlist_rules", map[string]any{"playlist_id": pool.ID, "rules": []any{map[string]string{"kind": "composer", "value": "Writer"}, map[string]string{"kind": "decade", "value": "1950"}}})
	rules, err = pools.Rules(ctx, pool.ID)
	if err != nil || len(rules) != 2 {
		t.Fatal(rules, err)
	}
}
