package producer_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

func TestAgentCreatesDailyJazzFunkShowWithSavedFeedback(t *testing.T) {
	_, r, stations, pools, shows, _ := fixture(t)
	ctx := context.Background()
	source, err := (sources.Service{Repo: persistence.Sources{DB: stations.DB}}).Save(ctx, sources.Config{Name: "Music", Kind: "upnp", Endpoint: "http://fixture.invalid/music"})
	if err != nil {
		t.Fatal(err)
	}
	items := []content.AudioItem{}
	for _, g := range []string{"Jazz", "Funk", "Pop"} {
		items = append(items, content.AudioItem{ID: content.ID(source.ID, g), SourceID: source.ID, SourceItemID: g, Title: g, Genre: g, Kind: content.Track, Origin: content.Original, Resources: []content.Resource{{URL: "http://fixture.invalid/" + g, MIME: "audio/mpeg"}}})
	}
	if err = (persistence.Catalogue{DB: stations.DB}).Replace(ctx, source, items); err != nil {
		t.Fatal(err)
	}
	owner := call(t, r, "save_station", map[string]string{"name": "Evening Radio"}).Data.(station.Station)
	selectTestSources(t, stations.DB)
	miles := call(t, r, "save_dj", map[string]string{"name": "Miles"}).Data.(djs.Profile)
	result := call(t, r, "create_playlist", map[string]any{"station_id": owner.ID, "name": "Jazz and Funk", "genres": []string{"jazz", "Funk"}})
	data := result.Data.(map[string]any)
	id := data["playlist_id"].(string)
	resolved, err := pools.Resolve(ctx, id, 0, 10)
	if err != nil || resolved.Total != 2 {
		t.Fatal(resolved, err)
	}
	rules, err := pools.Rules(ctx, id)
	if err != nil || len(rules) != 2 || data["available"] != 2 {
		t.Fatal(rules, data, err)
	}
	args := map[string]any{"station_id": owner.ID, "name": "Jazz Nights", "dj_id": miles.ID, "playlist_id": id, "days": []int{0, 1, 2, 3, 4, 5, 6}, "start_time": "20:00", "duration_minutes": 240, "description": "Jazz meets funk with Miles."}
	saved := call(t, r, "create_show", args).Data.(map[string]any)
	if saved["end_time"] != "24:00" || saved["duration_minutes"] != 240 || saved["dj_id"] != miles.ID || saved["playlist_id"] != id {
		t.Fatal(saved)
	}
	list, err := shows.List(ctx, owner.ID)
	if err != nil || len(list) != 1 || len(list[0].Weekdays()) != 7 || list[0].Start != 1200 || list[0].End != 0 {
		t.Fatal(list, err)
	}
	// A conflict across any requested day rejects the whole recurring programme.
	args["name"] = "Conflicting"
	args["days"] = []int{0, 5}
	raw, _ := json.Marshal(args)
	if v := r.Call(ctx, "create_show", raw); v.Error == "" {
		t.Fatal("overlap accepted")
	}
	list, _ = shows.List(ctx, owner.ID)
	if len(list) != 1 {
		t.Fatal("partial show saved", list)
	}
	// Bad initial selections cannot leave an empty pool behind.
	before, _ := pools.List(ctx, owner.ID)
	bad, _ := json.Marshal(map[string]any{"station_id": owner.ID, "name": "Bad pool", "genres": []string{""}})
	if v := r.Call(ctx, "create_playlist", bad); v.Error == "" {
		t.Fatal("empty genre accepted")
	}
	after, _ := pools.List(ctx, owner.ID)
	if len(after) != len(before) {
		t.Fatal("failed creation left pool")
	}
}

func TestNewPlaylistRulesRollbackOnUnavailableAlbum(t *testing.T) {
	_, r, stations, pools, _, _ := fixture(t)
	ctx := context.Background()
	owner := call(t, r, "save_station", map[string]string{"name": "Rollback Radio"}).Data.(station.Station)
	source, err := (sources.Service{Repo: persistence.Sources{DB: stations.DB}}).Save(ctx, sources.Config{Name: "Empty library", Kind: "upnp", Endpoint: "http://fixture.invalid/music"})
	if err != nil {
		t.Fatal(err)
	}
	selectTestSources(t, stations.DB)
	_, err = (playlists.Service{Repo: pools, Stations: stations}).CreateWithRules(ctx, playlists.Playlist{StationID: owner.ID, Name: "Must roll back"}, []playlists.Rule{{Kind: "genre", Value: "Jazz"}, {Kind: "album", SourceID: source.ID, Value: "Missing"}})
	if err == nil {
		t.Fatal("unavailable album accepted")
	}
	list, err := pools.List(ctx, owner.ID)
	if err != nil || len(list) != 0 {
		t.Fatal("partial pool committed", list, err)
	}
}

type directShowModel struct {
	inspect bool
	t       *testing.T
	args    map[string]any
	n       int
}

func (m *directShowModel) Respond(_ context.Context, _, _, _ string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"create_show", "create_playlist", "summarize_music", "list_playlists"} {
		if !names[name] {
			m.t.Fatal("missing task capability", name)
		}
	}
	if names["create_shows"] || names["select_work_step"] {
		m.t.Fatal("redundant nested creation advertised")
	}
	n := m.n
	m.n++
	if m.inspect {
		if n == 0 {
			return responseCall(100, "inspect_station", map[string]string{"station_name": "Direct Radio"}), nil
		}
		if n == 1 {
			raw, _ := json.Marshal(input)
			if !strings.Contains(string(raw), "Direct Radio") {
				m.t.Fatal("inspection omitted saved station")
			}
		}
		n--
	}
	switch n {
	case 0:
		return responseCall(n, "save_task_progress", producer.Task{Brief: "Create a daily Jazz show from 20:00 until midnight", Remaining: []string{"Create the show"}}), nil
	case 1, 2:
		return responseCall(n, "create_show", m.args), nil
	case 3:
		return responseCall(n, "save_task_progress", producer.Task{Brief: "Create a daily Jazz show from 20:00 until midnight", Completed: []string{"Verified saved daily days, Miles, playlist, 20:00 and 240-minute duration"}}), nil
	default:
		return producer.Response{Text: "The daily show is saved."}, nil
	}
}
func TestDirectShowRunnerFinishesWithoutWorkStepOrExtraReview(t *testing.T) {
	for _, inspect := range []bool{false, true} {
		ctx := context.Background()
		store, r, _, _, shows, _ := fixture(t)
		owner := call(t, r, "save_station", map[string]string{"name": "Direct Radio"}).Data.(station.Station)
		miles := call(t, r, "save_dj", map[string]string{"name": "Miles"}).Data.(djs.Profile)
		pool := call(t, r, "create_playlist", map[string]any{"station_id": owner.ID, "name": "Jazz and Funk", "genres": []string{"Jazz", "Funk"}}).Data.(map[string]any)
		if err := store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"}); err != nil {
			t.Fatal(err)
		}
		model := &directShowModel{t: t, inspect: inspect, args: map[string]any{"station_id": owner.ID, "name": "Jazz Nights", "dj_id": miles.ID, "playlist_id": pool["playlist_id"], "days": []int{0, 1, 2, 3, 4, 5, 6}, "start_time": "20:00", "duration_minutes": 240}}
		svc, err := producer.New(ctx, store, r, model)
		if err != nil {
			t.Fatal(err)
		}
		defer svc.Close()
		id, err := svc.Create(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = svc.Start(ctx, id, producer.ID(), "Create a daily Jazz show with Miles from 20:00 until midnight using this playlist."); err != nil {
			t.Fatal(err)
		}
		done := waitDone(t, store, id)
		expected := 5
		if inspect {
			expected++
		}
		if done.Status != "idle" || model.n != expected {
			t.Fatal(done.Status, model.n, done.Messages)
		}
		list, err := shows.List(ctx, owner.ID)
		if err != nil || len(list) != 1 || list[0].Duration() != 240 || len(list[0].Weekdays()) != 7 {
			t.Fatal("duplicate or incorrect show", list, err)
		}
	}
}
