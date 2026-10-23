package producer_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

type namedEditModel struct {
	t                          *testing.T
	n, edited                  int
	station, other             string
	showIDs                    []string
	eraBlocked, bound, blocked bool
}

func (m *namedEditModel) Respond(_ context.Context, _, _, prompt string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	n := m.n
	m.n++
	offered := map[string]bool{}
	for _, tool := range tools {
		offered[tool.Name] = true
	}
	// Detect invalid interleaving: a work-step call must receive its result before
	// fresh system readbacks are appended to the provider protocol exchange.
	for i, item := range input {
		var call struct{ Type, Name string }
		_ = json.Unmarshal(item, &call)
		if call.Type == "function_call" && call.Name == "select_work_step" {
			if i+1 >= len(input) {
				m.t.Fatal("missing selection result")
			}
			var next struct{ Type string }
			_ = json.Unmarshal(input[i+1], &next)
			if next.Type != "function_call_output" {
				m.t.Fatalf("selection result interrupted by %s", input[i+1])
			}
		}
	}
	if !m.bound && offered["list_stations"] {
		switch n % 3 {
		case 0:
			return responseCall(n, "list_stations", map[string]any{}), nil
		case 1:
			return responseCall(n, "list_playlists", map[string]any{}), nil
		default:
			return responseCall(n, "get_station", map[string]string{"id": m.other}), nil
		}
	}
	if !m.bound {
		if !offered["select_work_step"] || offered["save_station"] {
			m.t.Fatal("unscoped recovery did not require selection", offered)
		}
		m.bound = true
		return responseCall(n, "select_work_step", map[string]string{"area": "schedule", "station_name": "The Spectrum"}), nil
	}
	if m.blocked {
		return producer.Response{Text: "The strict 1980s selection cannot be verified from this catalogue; the original shows remain saved."}, nil
	}
	if m.eraBlocked {
		raw, _ := json.Marshal(input)
		if !strings.Contains(string(raw), "catalogue_release_year_available") {
			m.t.Error("missing truthful date capability")
		}
		m.eraBlocked = false
		m.blocked = true
		return responseCall(n, "save_task_progress", producer.Task{Brief: "Create an exclusively 1980s show on The Spectrum weekdays 18:00–20:00", Blockers: []string{"No matching recordings have been verified in this empty catalogue; album knowledge cannot supply unavailable library music"}}), nil
	}
	if m.edited < len(m.showIDs) {
		for _, name := range []string{"list_stations", "list_playlists", "get_station"} {
			if offered[name] {
				m.t.Fatalf("discovery leaked into bound work step: %s", name)
			}
		}
		id := m.showIDs[m.edited]
		m.edited++
		return responseCall(n, "save_show", map[string]any{"station_id": m.station, "id": id, "name": "Evening Jazz"}), nil
	}
	if m.edited == len(m.showIDs) {
		m.edited++
		return responseCall(n, "save_task_progress", producer.Task{Brief: "Update The Spectrum weekday 18:00–20:00 shows", Completed: []string{"Updated all five existing weekday editions; preserved other configuration"}}), nil
	}
	return producer.Response{Text: "Verified the saved request outcome and any recorded blocker."}, nil
}

func TestNamedStationEditWithoutChecklistBindsInsteadOfRediscovering(t *testing.T) {
	for _, era := range []bool{false, true} {
		t.Run(map[bool]string{false: "edit", true: "no_matching_music"}[era], func(t *testing.T) {
			ctx := context.Background()
			store, r, stations, _, shows, _ := fixture(t)
			unrelated := call(t, r, "save_station", map[string]string{"name": "Other Station"}).Data.(station.Station)
			owner := call(t, r, "save_station", map[string]string{"name": "The Spectrum"}).Data.(station.Station)
			pool := call(t, r, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Existing Pool"}).Data.(playlists.Playlist)
			dj := call(t, r, "save_dj", map[string]string{"name": "Existing Presenter"}).Data.(djs.Profile)
			created := call(t, r, "create_shows", map[string]any{"show": map[string]any{"station_id": owner.ID, "name": "Original Show", "dj_id": dj.ID, "playlist_id": pool.ID, "start": 1080, "end": 1200}, "days": []int{0, 1, 2, 3, 4}}).Data.([]schedule.Show)
			_ = store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"})
			model := &namedEditModel{t: t, station: owner.ID, other: unrelated.ID, eraBlocked: era}
			if !era {
				for _, show := range created {
					model.showIDs = append(model.showIDs, show.ID)
				}
			}
			svc, err := producer.New(ctx, store, r, model)
			if err != nil {
				t.Fatal(err)
			}
			defer svc.Close()
			id, _ := svc.Create(ctx, "")
			request := "On The Spectrum, rename the 6pm–8pm weekday shows Evening Jazz. Make the changes."
			if era {
				request = "On The Spectrum, from 6pm to 8pm on weekdays I'd like a 1980's show featuring just music from that era. Make the changes."
			}
			if err := svc.Start(ctx, id, producer.ID(), request); err != nil {
				t.Fatal(err)
			}
			conversation := waitDone(t, store, id)
			if conversation.Status != "idle" || model.n > 16 {
				t.Fatal("unscoped request kept checking", conversation.Status, model.n, conversation.Messages)
			}
			if len(conversation.Requests) != 1 || conversation.Requests[0] != request {
				t.Fatal("request lost", conversation.Requests)
			}
			saved, err := shows.List(ctx, owner.ID)
			if err != nil || len(saved) != 1 || len(saved[0].Weekdays()) != 5 {
				t.Fatal(saved, err)
			}
			for _, show := range saved {
				expected := "Evening Jazz"
				if era {
					expected = "Original Show"
				}
				if show.Name != expected || show.DJID != dj.ID || show.PlaylistID != pool.ID || show.Start != 1080 || show.End != 1200 {
					t.Fatal("unexpected schedule edit", show)
				}
			}
			if era && len(conversation.Task.Blockers) != 1 {
				t.Fatal("missing verified blocker", conversation.Task)
			}
			other, err := stations.Get(ctx, unrelated.ID)
			if err != nil || other.Name != "Other Station" {
				t.Fatal("unrelated station changed", other, err)
			}
		})
	}
}
