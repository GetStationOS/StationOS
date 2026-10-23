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
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

type albumEraModel struct {
	t                         *testing.T
	n                         int
	station, pool, dj, source string
}

func (m *albumEraModel) Respond(_ context.Context, _, _, prompt string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	n := m.n
	m.n++
	if !strings.Contains(prompt, "a missing year field alone is not a blocker") {
		m.t.Error("musical knowledge fallback missing")
	}
	switch n {
	case 0:
		return responseCall(n, "select_work_step", map[string]string{"area": "playlists", "station_name": "The Spectrum"}), nil
	case 1:
		return responseCall(n, "explore_music", map[string]any{"kind": "albums", "text": "Thriller", "limit": 5}), nil
	case 2:
		return responseCall(n, "search_music", map[string]any{"album": "Thriller", "source_id": m.source, "limit": 5}), nil
	case 3:
		raw, _ := json.Marshal(input)
		for _, want := range []string{"Thriller", "Michael Jackson", m.source} {
			if !strings.Contains(string(raw), want) {
				m.t.Error("album/artist not observed before selection", want)
			}
		}
		return responseCall(n, "add_playlist_rule", map[string]any{"playlist_id": m.pool, "rule": map[string]string{"kind": "album", "value": "Thriller", "source_id": m.source}}), nil
	case 4:
		return responseCall(n, "select_work_step", map[string]string{"area": "schedule", "station_name": "The Spectrum"}), nil
	case 5:
		return responseCall(n, "list_djs", map[string]any{}), nil
	case 6:
		return responseCall(n, "get_dj", map[string]string{"id": m.dj}), nil
	case 7:
		return responseCall(n, "create_shows", map[string]any{"show": map[string]any{"station_id": m.station, "name": "Eighties After Six", "dj_id": m.dj, "playlist_id": m.pool, "start": 1080, "end": 1200}, "days": []int{0, 1, 2, 3, 4}}), nil
	case 8:
		return responseCall(n, "get_playlist", map[string]any{"id": m.pool, "limit": 5}), nil
	case 9:
		return responseCall(n, "save_task_progress", producer.Task{Brief: "The Spectrum weekday 18:00–20:00 1980s show", Completed: []string{"Selected an exact matching album pool and scheduled all five editions"}}), nil
	}
	return producer.Response{Text: "Configured Eighties After Six weekdays 18:00–20:00 with an album-based music pool."}, nil
}

func TestAlbumEraSelectionDoesNotRequireYearMetadata(t *testing.T) {
	ctx := context.Background()
	store, r, stations, pools, shows, _ := fixture(t)
	owner := call(t, r, "save_station", map[string]string{"name": "The Spectrum"}).Data.(station.Station)
	pool := call(t, r, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Eighties After Six"}).Data.(playlists.Playlist)
	dj := call(t, r, "save_dj", map[string]string{"name": "Evening Presenter"}).Data.(djs.Profile)
	source := sources.Config{ID: "album-library", Name: "Library", Kind: "upnp", Endpoint: "http://server/desc", Revision: 1}
	if err := (persistence.Sources{DB: stations.DB}).Save(ctx, source); err != nil {
		t.Fatal(err)
	}
	items := []content.AudioItem{
		{ID: content.ID(source.ID, "eighties"), SourceID: source.ID, SourceItemID: "eighties", Title: "Beat It", Artist: "Michael Jackson", Album: "Thriller", Genre: "Pop", Kind: content.Track, Origin: content.Original},
		{ID: content.ID(source.ID, "other"), SourceID: source.ID, SourceItemID: "other", Title: "Later Recording", Artist: "Other Artist", Album: "Later Album", Genre: "Pop", Kind: content.Track, Origin: content.Original},
	}
	selectTestSources(t, stations.DB)
	if err := (persistence.Catalogue{DB: stations.DB}).Replace(ctx, source, items); err != nil {
		t.Fatal(err)
	}
	_ = store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"})
	model := &albumEraModel{t: t, station: owner.ID, pool: pool.ID, dj: dj.ID, source: source.ID}
	svc, err := producer.New(ctx, store, r, model)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	id, _ := svc.Create(ctx, "")
	_ = store.Finish(ctx, id, "failed", []byte(`[]`), producer.Message{Role: "assistant", Text: "The assistant could not turn its checks into saved changes"})
	request := "On The Spectrum, from 6pm to 8pm on weekdays I'd like a 1980's show featuring just music from that era. Make the changes."
	if err := svc.Start(ctx, id, producer.ID(), request); err != nil {
		t.Fatal(err)
	}
	c := waitDone(t, store, id)
	if c.Status != "idle" || len(c.Task.Blockers) != 0 || model.n != 12 {
		t.Fatal("album-based setup stalled", c.Status, c.Task, model.n, c.Messages)
	}
	rules, err := pools.Rules(ctx, pool.ID)
	if err != nil || len(rules) != 1 || rules[0].Kind != "album" || rules[0].Value != "Thriller" || rules[0].SourceID != source.ID {
		t.Fatal("broad or incorrect eligibility", rules, err)
	}
	resolved, err := pools.Resolve(ctx, pool.ID, 0, 10)
	if err != nil || resolved.Total != 1 || resolved.Entries[0].Item.ID != items[0].ID {
		t.Fatal("out-of-pool music included", resolved, err)
	}
	saved, err := shows.List(ctx, owner.ID)
	if err != nil || len(saved) != 1 || len(saved[0].Weekdays()) != 5 {
		t.Fatal(saved, err)
	}
	for day, show := range schedule.ExpandDays(saved) {
		if show.Day != day || show.Start != 1080 || show.End != 1200 || show.PlaylistID != pool.ID || show.DJID != dj.ID {
			t.Fatal("wrong weekday edition", show)
		}
	}
}
