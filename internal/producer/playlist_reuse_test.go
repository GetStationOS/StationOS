package producer_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/station"
)

func TestAssistantPlaylistDuplicatesAreScopedToTheirStation(t *testing.T) {
	_, registry, _, repo, _, _ := fixture(t)
	ctx := context.Background()
	owner := call(t, registry, "save_station", map[string]string{"name": "The Listening Room"}).Data.(station.Station)
	pool := call(t, registry, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Piano Room", "description": "Solo piano"}).Data.(playlists.Playlist)
	call(t, registry, "add_playlist_rule", map[string]any{"playlist_id": pool.ID, "rule": map[string]string{"kind": "genre", "value": "Piano"}})
	for _, name := range []string{"Piano Room", "  PIANO   ROOM ", "Piano Room - The Listening Room", "Piano Room — The Listening Room", "Piano Room (The Listening Room)"} {
		raw, _ := json.Marshal(map[string]string{"station_id": owner.ID, "name": name, "description": "Unrequested replacement"})
		result := registry.Call(ctx, "save_playlist", raw)
		if result.Error == "" || !strings.Contains(result.Error, pool.ID) {
			t.Fatalf("duplicate %q: %+v", name, result)
		}
	}
	stored, _ := repo.Get(ctx, pool.ID)
	rules, _ := repo.Rules(ctx, pool.ID)
	all, _ := repo.List(ctx, owner.ID)
	if stored.Description != pool.Description || len(all) != 1 || len(rules) != 1 || rules[0].Value != "Piano" {
		t.Fatal("duplicate attempts changed eligibility", stored, all, rules)
	}
	distinct := call(t, registry, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Contemporary Piano"}).Data.(playlists.Playlist)
	raw, _ := json.Marshal(map[string]string{"id": distinct.ID, "name": "Piano Room"})
	if registry.Call(ctx, "save_playlist", raw).Error == "" {
		t.Fatal("rename duplicated playlist")
	}
	call(t, registry, "save_playlist", map[string]string{"id": pool.ID, "description": "An updated description"})
	other := call(t, registry, "save_station", map[string]string{"name": "Piano Radio"}).Data.(station.Station)
	otherPool := call(t, registry, "save_playlist", map[string]string{"station_id": other.ID, "name": "Piano Room"}).Data.(playlists.Playlist)
	if otherPool.ID == pool.ID || otherPool.StationID != other.ID {
		t.Fatal("cross-station pool reused", otherPool)
	}
	if result := registry.Call(ctx, "assign_default_playlist", []byte(`{"station_id":"`+other.ID+`","playlist_id":"`+pool.ID+`"}`)); result.Error == "" {
		t.Fatal("assigned another station's playlist")
	}
}

func TestExistingPlaylistSupportsMultipleShowsAndDefaultWithoutCopying(t *testing.T) {
	_, registry, stations, repo, shows, _ := fixture(t)
	ctx := context.Background()
	owner := call(t, registry, "save_station", map[string]string{"name": "Jazz Radio"}).Data.(station.Station)
	pool := call(t, registry, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Jazz Avenue"}).Data.(playlists.Playlist)
	call(t, registry, "add_playlist_rule", map[string]any{"playlist_id": pool.ID, "rule": map[string]string{"kind": "genre", "value": "Jazz"}})
	details := call(t, registry, "get_playlist", map[string]string{"id": pool.ID}).Data.(map[string]any)
	rules := details["rules"].([]playlists.Rule)
	if len(rules) != 1 || rules[0].Value != "Jazz" {
		t.Fatal("cannot inspect eligibility", details)
	}
	dj := call(t, registry, "save_dj", map[string]string{"name": "Rowan Ellis"}).Data.(djs.Profile)
	call(t, registry, "create_shows", map[string]any{"show": map[string]any{"station_id": owner.ID, "name": "Evening Jazz", "dj_id": dj.ID, "playlist_id": pool.ID, "start": 1200, "end": 1320}, "days": []int{4, 6}})
	call(t, registry, "assign_default_playlist", map[string]string{"station_id": owner.ID, "playlist_id": pool.ID})
	all, _ := repo.List(ctx, owner.ID)
	scheduled, _ := shows.List(ctx, owner.ID)
	assigned, err := (persistence.Broadcast{DB: stations.DB}).Assignment(ctx, owner.ID)
	if err != nil || assigned != pool.ID || len(all) != 1 || len(scheduled) != 2 {
		t.Fatal(all, scheduled, assigned, err)
	}
	for _, show := range scheduled {
		if show.PlaylistID != pool.ID {
			t.Fatal(show)
		}
	}
}

func TestConcurrentAssistantPlaylistCreationRejectsDuplicateNames(t *testing.T) {
	_, registry, _, repo, _, _ := fixture(t)
	owner := call(t, registry, "save_station", map[string]string{"name": "Jazz Radio"}).Data.(station.Station)
	raw, _ := json.Marshal(map[string]string{"station_id": owner.ID, "name": "Night Jazz"})
	var wg sync.WaitGroup
	results := make(chan producer.Result, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- registry.Call(context.Background(), "save_playlist", raw) }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for result := range results {
		if result.Error == "" {
			successes++
		}
	}
	all, _ := repo.List(context.Background(), owner.ID)
	if successes != 1 || len(all) != 1 {
		t.Fatal(successes, all)
	}
}
