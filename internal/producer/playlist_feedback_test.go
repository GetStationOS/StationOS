package producer_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

func TestPlaylistWritesReportEmptyPoolAndVerifiedRepair(t *testing.T) {
	_, registry, stations, pools, _, _ := fixture(t)
	ctx := context.Background()
	source, err := (sources.Service{Repo: persistence.Sources{DB: stations.DB}}).Save(ctx, sources.Config{Name: "Fixture music", Kind: "upnp", Endpoint: "http://fixture.invalid/description"})
	if err != nil {
		t.Fatal(err)
	}
	item := content.AudioItem{ID: content.ID(source.ID, "one"), SourceID: source.ID, SourceItemID: "one", Title: "Jazz recording", Genre: "Jazz", Kind: content.Track, Origin: content.Original}
	if err = (persistence.Catalogue{DB: stations.DB}).Replace(ctx, source, []content.AudioItem{item}); err != nil {
		t.Fatal(err)
	}
	owner := call(t, registry, "save_station", map[string]string{"name": "Fixture station"}).Data.(station.Station)
	pool := call(t, registry, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Jazz pool"}).Data.(playlists.Playlist)
	check := func(result producer.Result, want int, warn bool) {
		t.Helper()
		raw, _ := json.Marshal(result.Data)
		var data struct {
			Available  *int   `json:"available"`
			PlaylistID string `json:"playlist_id"`
			Warning    string
			NextStep   string `json:"next_step"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		if result.Error != "" || result.Summary == "" || result.URL == "" || data.PlaylistID != pool.ID || data.Available == nil || *data.Available != want {
			t.Fatal("missing committed outcome or measured count", result, string(raw))
		}
		if (data.Warning != "") != warn {
			t.Fatal("wrong eligibility warning", string(raw))
		}
		if warn && !strings.Contains(data.NextStep, "exact catalogue values") {
			t.Fatal("missing repair hint", string(raw))
		}
	}
	// Unknown values remain empty and return immediate measured feedback.
	check(call(t, registry, "add_playlist_rule", map[string]any{"playlist_id": pool.ID, "rule": map[string]string{"kind": "genre", "value": "Blues"}}), 0, true)
	rules, err := pools.Rules(ctx, pool.ID)
	if err != nil || len(rules) != 1 {
		t.Fatal("warning must retain the committed rule", rules, err)
	}
	// Reproduce Gemma's casing error; only the unique catalogue spelling is used.
	normalized := call(t, registry, "replace_playlist_rules", map[string]any{"playlist_id": pool.ID, "rules": []any{map[string]string{"kind": "genre", "value": "jazz"}}})
	check(normalized, 1, false)
	raw, _ := json.Marshal(normalized.Data)
	if !strings.Contains(string(raw), `"requested":"jazz"`) || !strings.Contains(string(raw), `"value":"Jazz"`) {
		t.Fatal("canonical spelling was not reported", string(raw))
	}
	check(call(t, registry, "replace_playlist_rules", map[string]any{"playlist_id": pool.ID, "rules": []any{map[string]string{"kind": "genre", "value": "Jazz"}}}), 1, false)
	rules, err = pools.Rules(ctx, pool.ID)
	if err != nil || len(rules) != 1 {
		t.Fatal(rules, err)
	}
	check(call(t, registry, "remove_playlist_rule", map[string]string{"playlist_id": pool.ID, "rule_id": rules[0].ID}), 0, true)
	check(call(t, registry, "select_tracks", map[string]any{"playlist_id": pool.ID, "track_ids": []string{item.ID}, "included": true}), 1, false)
	check(call(t, registry, "select_tracks", map[string]any{"playlist_id": pool.ID, "track_ids": []string{item.ID}, "included": false}), 0, true)
}

type unavailablePlaylistCounts struct{ persistence.Playlists }

func (unavailablePlaylistCounts) Availability(context.Context, string) (playlists.Availability, error) {
	return playlists.Availability{}, errors.New("read failed")
}

func TestEligibilityReadFailureDoesNotMisreportCommittedWrite(t *testing.T) {
	_, registry, stations, pools, _, _ := fixture(t)
	ctx := context.Background()
	owner := call(t, registry, "save_station", map[string]string{"name": "Fixture station"}).Data.(station.Station)
	pool := call(t, registry, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Future music"}).Data.(playlists.Playlist)
	registry = producer.NewRegistry(producer.Dependencies{Stations: stations, Playlists: unavailablePlaylistCounts{pools}})
	result := call(t, registry, "add_playlist_rule", map[string]any{"playlist_id": pool.ID, "rule": map[string]string{"kind": "genre", "value": "Jazz"}})
	raw, _ := json.Marshal(result.Data)
	if strings.Contains(string(raw), `"available":0`) || !strings.Contains(string(raw), "do not repeat the successful write") {
		t.Fatal("unknown count was reported as zero or unsafe retry advice", string(raw))
	}
	rules, err := pools.Rules(ctx, pool.ID)
	if err != nil || len(rules) != 1 {
		t.Fatal("successful write was lost", rules, err)
	}
}

func TestEmptyPoolCreationAndDefaultAssignmentGiveMeasuredFeedback(t *testing.T) {
	_, registry, stations, _, _, _ := fixture(t)
	owner := call(t, registry, "save_station", map[string]string{"name": "Future station"}).Data.(station.Station)
	result := call(t, registry, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Jazz"})
	pool := result.Data.(playlists.Playlist)
	if !strings.Contains(result.Summary, "0 eligible recordings") || !strings.Contains(result.Summary, "name does not select music") {
		t.Fatal("named pool was presented as populated", result)
	}
	assigned := call(t, registry, "assign_default_playlist", map[string]string{"station_id": owner.ID, "playlist_id": pool.ID})
	raw, _ := json.Marshal(assigned.Data)
	if !strings.Contains(string(raw), `"available":0`) || !strings.Contains(string(raw), "cannot supply music") {
		t.Fatal("empty assignment hid missing music", string(raw))
	}
	actual, err := (persistence.Broadcast{DB: stations.DB}).Assignment(context.Background(), owner.ID)
	if err != nil || actual != pool.ID {
		t.Fatal("warning changed the successful assignment", actual, err)
	}
}
