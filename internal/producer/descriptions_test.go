package producer_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

func TestDescriptionOnlyToolsPreserveConfigurationAndAvoidUnchangedWrites(t *testing.T) {
	_, r, stations, pools, shows, DJs := fixture(t)
	ctx := context.Background()
	owner := call(t, r, "save_station", map[string]any{"name": "Description Radio", "description": "Before", "timezone": "Europe/London", "visibility": "private", "stream_slug": "stable-address", "repeat_minutes": 240}).Data.(station.Station)
	pool := call(t, r, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Music"}).Data.(playlists.Playlist)
	show := call(t, r, "create_shows", map[string]any{"days": []int{5, 6}, "show": map[string]any{"station_id": owner.ID, "name": "Weekend", "description": "Old show copy", "music_direction": "Explore verified 1980s recordings; vary performers.", "dj_id": "default-pop", "playlist_id": pool.ID, "start": 360, "end": 1200, "accept_requests": true, "accept_shoutouts": true}}).Data.([]schedule.Show)[0]
	stationBefore, _ := stations.Get(ctx, owner.ID)
	showBefore, _ := shows.Get(ctx, owner.ID, show.ID)
	images := &testImages{}
	r = producer.NewRegistry(producer.Dependencies{Stations: stations, Playlists: pools, Schedule: shows, DJs: DJs, Images: images})
	stationText := "An inviting public station description."
	showText := strings.TrimSpace(strings.Repeat("A listener-facing weekend invitation. ", 10))
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"set_station_description", map[string]any{"station_id": owner.ID, "description": stationText}},
		{"set_show_description", map[string]any{"station_id": owner.ID, "show_id": show.ID, "description": showText}},
	} {
		result := call(t, r, tc.tool, tc.args)
		if result.Unchanged {
			t.Fatal("changed description reported unchanged", result)
		}
		raw, _ := json.Marshal(result.Data)
		var saved struct{ Description string }
		json.Unmarshal(raw, &saved)
		if saved.Description != tc.args["description"] {
			t.Fatal("saved description missing from result", string(raw))
		}
		if same := call(t, r, tc.tool, tc.args); !same.Unchanged {
			t.Fatal("identical description must be a no-op", same)
		}
	}
	if len(images.calls) != 0 {
		t.Fatal("description-only edit generated artwork", len(images.calls))
	}
	stationAfter, _ := stations.Get(ctx, owner.ID)
	if stationAfter.Description != stationText {
		t.Fatal(stationAfter)
	}
	stationAfter.Description = stationBefore.Description
	if !reflect.DeepEqual(stationBefore, stationAfter) {
		t.Fatal("description edit changed station settings", stationBefore, stationAfter)
	}
	showAfter, _ := shows.Get(ctx, owner.ID, show.ID)
	if showAfter.Description != showText || showAfter.Revision != showBefore.Revision+1 {
		t.Fatal("repeated save changed revision or lost text", showBefore, showAfter)
	}
	showAfter.Description = showBefore.Description
	showAfter.Revision = showBefore.Revision
	if !reflect.DeepEqual(showBefore, showAfter) {
		t.Fatal("description edit changed scheduling, slots, DJ, eligibility or direction", showBefore, showAfter)
	}
	list := call(t, r, "list_shows", map[string]string{"station_id": owner.ID})
	raw, _ := json.Marshal(list.Data)
	var rows []struct {
		Description string
		Truncated   bool `json:"description_truncated"`
	}
	json.Unmarshal(raw, &rows)
	if len(rows) != 1 || rows[0].Description == "" || !rows[0].Truncated {
		t.Fatal("description missing from bounded list readback", string(raw))
	}
	full := call(t, r, "get_show", map[string]string{"station_id": owner.ID, "id": show.ID}).Data.(schedule.Show)
	if full.Description != showText {
		t.Fatal("full description unavailable", full)
	}
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"set_station_description", map[string]any{"station_id": owner.ID, "description": ""}},
		{"set_show_description", map[string]any{"station_id": owner.ID, "show_id": show.ID, "description": ""}},
	} {
		call(t, r, tc.tool, tc.args)
	}
	clearedStation, _ := stations.Get(ctx, owner.ID)
	clearedShow, _ := shows.Get(ctx, owner.ID, show.ID)
	if clearedStation.Description != "" || clearedShow.Description != "" {
		t.Fatal("explicit clearing failed")
	}
}

func TestDescriptionToolsRejectMissingFieldsAndUnrelatedChanges(t *testing.T) {
	_, r, stations, _, shows, _ := fixture(t)
	ctx := context.Background()
	owner := call(t, r, "save_station", map[string]string{"name": "Untouched", "description": "Keep"}).Data.(station.Station)
	other := call(t, r, "save_station", map[string]string{"name": "Other"}).Data.(station.Station)
	pool := call(t, r, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Music"}).Data.(playlists.Playlist)
	show := call(t, r, "save_show", map[string]any{"station_id": owner.ID, "name": "Saturday", "description": "Keep show", "dj_id": "default-pop", "playlist_id": pool.ID, "day": 5, "start": 360, "end": 1200}).Data.(schedule.Show)
	before, _ := shows.Get(ctx, owner.ID, show.ID)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"set_station_description", map[string]any{"description": "Lost ID"}},
		{"set_station_description", map[string]any{"station_id": owner.ID}},
		{"set_station_description", map[string]any{"station_id": owner.ID, "description": nil}},
		{"set_station_description", map[string]any{"station_id": "missing", "description": "No creation"}},
		{"set_station_description", map[string]any{"station_id": owner.ID, "description": strings.Repeat("x", 4001)}},
		{"set_station_description", map[string]any{"station_id": owner.ID, "description": "Change", "name": "Unrequested rename"}},
		{"set_show_description", map[string]any{"station_id": owner.ID, "description": "Missing show ID"}},
		{"set_show_description", map[string]any{"station_id": owner.ID, "show_id": show.ID}},
		{"set_show_description", map[string]any{"station_id": other.ID, "show_id": show.ID, "description": "Wrong station"}},
		{"set_show_description", map[string]any{"station_id": owner.ID, "show_id": show.ID, "description": strings.Repeat("x", 2001)}},
		{"set_show_description", map[string]any{"station_id": owner.ID, "show_id": show.ID, "description": "Change", "start": 480}},
	} {
		raw, _ := json.Marshal(tc.args)
		if result := r.Call(ctx, tc.tool, raw); result.Error == "" {
			t.Fatal("invalid description edit accepted", tc.tool, tc.args)
		}
	}
	saved, _ := stations.Get(ctx, owner.ID)
	after, _ := shows.Get(ctx, owner.ID, show.ID)
	all, _ := stations.List(ctx)
	if saved.Description != "Keep" || len(all) != 2 || !reflect.DeepEqual(before, after) {
		t.Fatal("failed edit mutated configuration", saved, after)
	}
}

func TestDescriptionToolSchemasExposeOnlyExplicitEditFields(t *testing.T) {
	_, r, _, _, _, _ := fixture(t)
	for _, name := range []string{"set_station_description", "set_show_description"} {
		tool, ok := r.Find(name)
		if !ok {
			t.Fatal(name)
		}
		raw, _ := json.Marshal(tool.Parameters)
		var schema struct {
			Required             []string
			AdditionalProperties bool
			Properties           map[string]any
		}
		json.Unmarshal(raw, &schema)
		expected := 2
		if name == "set_show_description" {
			expected = 3
		}
		if len(schema.Properties) != expected || len(schema.Required) != expected || schema.AdditionalProperties {
			t.Fatal("ambiguous edit schema", string(raw))
		}
	}
}

type descriptionRecoveryModel struct {
	t                   *testing.T
	mode, station, show string
	n                   int
}

func (m *descriptionRecoveryModel) Respond(_ context.Context, _, _, _ string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	n := m.n
	m.n++
	offered := map[string]bool{}
	for _, tool := range tools {
		offered[tool.Name] = true
	}
	if m.mode == "unchanged" {
		return responseCall(n, "set_show_description", map[string]string{"station_id": m.station, "show_id": m.show, "description": "Existing public show copy"}), nil
	}
	if n == 0 {
		if !offered["inspect_station"] || !offered["create_show"] || !offered["create_playlist"] {
			m.t.Fatal("recovery inferred extra work from intentional gaps or unrelated empty pools", offered)
		}
		return responseCall(n, "select_work_step", map[string]string{"area": m.mode, "station_id": m.station}), nil
	}
	if m.mode == "descriptions" {
		if n == 1 {
			raw, _ := json.Marshal(input)
			if !offered["list_djs"] || !offered["list_playlists"] || !offered["set_show_description"] || !strings.Contains(string(raw), "Existing public show copy") {
				m.t.Fatal("description recovery omitted copy or exposed scheduling work", offered, string(raw))
			}
			return responseCall(n, "set_station_description", map[string]string{"station_id": m.station, "description": "Fresh station copy"}), nil
		}
		if n == 2 {
			return responseCall(n, "set_show_description", map[string]string{"station_id": m.station, "show_id": m.show, "description": "Fresh show copy"}), nil
		}
		if n == 3 {
			return responseCall(n, "save_task_progress", producer.Task{Brief: "Update public station and show descriptions", Completed: []string{"Verified saved station and show text"}}), nil
		}
	} else {
		if n == 1 {
			return responseCall(n, "list_shows", map[string]string{"station_id": m.station}), nil
		}
		if n == 2 {
			raw, _ := json.Marshal(input)
			for _, want := range []string{"Saturday", "06:00", "20:00"} {
				if !strings.Contains(string(raw), want) {
					m.t.Fatal("missing measured verification", want)
				}
			}
			return responseCall(n, "save_task_progress", producer.Task{Brief: "Create Saturday 06:00–20:00 1980s show hosted by Alex", Completed: []string{"Verified Saturday hours and saved assignments"}}), nil
		}
	}
	return producer.Response{Text: "Verified the requested saved configuration."}, nil
}

func TestDescriptionAndSaturdayVerificationRecoveryStayWithinRequestedScope(t *testing.T) {
	for _, mode := range []string{"descriptions", "review", "unchanged"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			store, r, stations, pools, shows, _ := fixture(t)
			source := sources.Config{ID: "library", Name: "Fixture music", Kind: "upnp", Endpoint: "http://fixture.invalid/description", Revision: 1}
			if err := (persistence.Sources{DB: stations.DB}).Save(ctx, source); err != nil {
				t.Fatal(err)
			}
			item := content.AudioItem{ID: content.ID(source.ID, "one"), SourceID: source.ID, SourceItemID: "one", Title: "Verified 1985 recording", Year: 1985, Genre: "Pop", Kind: content.Track, Origin: content.Original}
			if err := (persistence.Catalogue{DB: stations.DB}).Replace(ctx, source, []content.AudioItem{item}); err != nil {
				t.Fatal(err)
			}
			owner := call(t, r, "save_station", map[string]string{"name": "Saturday Radio", "description": "Existing station copy"}).Data.(station.Station)
			pool := call(t, r, "save_playlist", map[string]string{"station_id": owner.ID, "name": "1980s"}).Data.(playlists.Playlist)
			call(t, r, "add_playlist_rule", map[string]any{"playlist_id": pool.ID, "rule": map[string]string{"kind": "decade", "value": "1980"}})
			call(t, r, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Unrelated future pool"})
			show := call(t, r, "save_show", map[string]any{"station_id": owner.ID, "name": "The 80s Collection", "description": "Existing public show copy", "music_direction": "Explore verified 1980s recordings.", "dj_id": "default-pop", "playlist_id": pool.ID, "day": 5, "start": 360, "end": 1200}).Data.(schedule.Show)
			if err := store.SaveSettings(ctx, "scripted-model", producer.KeyChange{Value: "test-key"}); err != nil {
				t.Fatal(err)
			}
			before, _ := shows.Get(ctx, owner.ID, show.ID)
			model := &descriptionRecoveryModel{t: t, mode: mode, station: owner.ID, show: show.ID}
			svc, err := producer.New(ctx, store, r, model)
			if err != nil {
				t.Fatal(err)
			}
			defer svc.Close()
			id, err := svc.Create(ctx, owner.ID)
			if err != nil {
				t.Fatal(err)
			}
			brief := "Create a Saturday show from 6am until 8pm showcasing our 80s collection, hosted by Alex."
			if mode == "descriptions" {
				brief = "Add descriptions to this station and its existing shows."
			}
			if mode != "unchanged" {
				if err = store.Begin(ctx, id, producer.ID(), brief); err != nil {
					t.Fatal(err)
				}
				task := producer.Task{Brief: brief, Completed: []string{"Existing configuration saved"}, Remaining: []string{"Verify requested configuration"}}
				if err = store.Checkpoint(ctx, id, []byte(`[]`), task); err != nil {
					t.Fatal(err)
				}
				if err = store.Finish(ctx, id, "failed", []byte(`[]`), producer.Message{Role: "assistant", Text: "The assistant could not turn its checks into saved changes, including after focused recovery."}); err != nil {
					t.Fatal(err)
				}
			}
			if err = svc.Start(ctx, id, producer.ID(), "Continue the saved request"); err != nil {
				t.Fatal(err)
			}
			conversation := waitDone(t, store, id)
			after, _ := shows.Get(ctx, owner.ID, show.ID)
			savedStation, _ := stations.Get(ctx, owner.ID)
			allShows, _ := shows.List(ctx, owner.ID)
			allPools, _ := pools.List(ctx, owner.ID)
			if len(allShows) != 1 || len(allPools) != 2 {
				t.Fatal("recovery created unrequested configuration", allShows, allPools)
			}
			if mode == "descriptions" {
				if conversation.Status != "idle" || model.n != 5 || after.Description != "Fresh show copy" || savedStation.Description != "Fresh station copy" {
					t.Fatal("description recovery did not complete", conversation.Status, model.n, after, savedStation)
				}
				after.Description = before.Description
				after.Revision = before.Revision
			} else if mode == "review" {
				if conversation.Status != "idle" || model.n != 4 || len(conversation.Task.Remaining) != 0 {
					t.Fatal("read-only verification required new writes", conversation.Status, model.n, conversation.Task)
				}
			} else if conversation.Status != "failed" || model.n != 3 {
				t.Fatal("unchanged edits were not paused early", conversation.Status, model.n)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("recovery changed unrelated show configuration", before, after)
			}
		})
	}
}
