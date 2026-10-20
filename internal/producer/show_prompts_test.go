package producer_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

func TestAssistantCreatesDistinctMusicPromptsAndReadsThemBack(t *testing.T) {
	_, registry, _, _, _, _ := fixture(t)
	owner := call(t, registry, "save_station", map[string]any{"name": "Curious Radio"}).Data.(station.Station)
	pool := call(t, registry, "save_playlist", map[string]any{"station_id": owner.ID, "name": "Shared eligible music"})
	raw, _ := json.Marshal(pool.Data)
	var p struct{ ID string }
	json.Unmarshal(raw, &p)
	directions := []string{"Start with bright familiar recordings, explore connections across different performers and albums in the middle, and finish with an uplifting closer. Avoid consecutive performers and albums. " + strings.Repeat("Choose a fresh discovery. ", 8), "An artist spotlight: explore one performer's development across different albums, interleave early and later recordings and explain musical connections. Repeated performers are intentional."}
	created := []schedule.Show{}
	for i, direction := range directions {
		result := call(t, registry, "create_shows", map[string]any{"show": map[string]any{"station_id": owner.ID, "name": []string{"Open Windows", "Artist Stories"}[i], "description": "A listener-facing invitation.", "music_direction": direction, "dj_id": "default-pop", "playlist_id": p.ID, "start": 540 + i*60, "end": 600 + i*60, "accept_requests": true}, "days": []int{0, 2}})
		editions := result.Data.([]schedule.Show)
		if len(editions) != 2 {
			t.Fatal(result)
		}
		created = append(created, editions...)
		for _, show := range editions {
			full := call(t, registry, "get_show", map[string]any{"station_id": owner.ID, "id": show.ID}).Data.(schedule.Show)
			if full.MusicDirection != strings.TrimSpace(direction) || full.Description != "A listener-facing invitation." || !full.AcceptRequests || full.DJ.ID != "" {
				t.Fatal("show prompt or public settings lost", full)
			}
		}
	}
	// Editing a time/name must preserve editorial instructions and participation.
	changed := call(t, registry, "save_show", map[string]any{"station_id": owner.ID, "id": created[0].ID, "name": "Open Windows revised"}).Data.(schedule.Show)
	if changed.MusicDirection != strings.TrimSpace(directions[0]) || !changed.AcceptRequests {
		t.Fatal("partial edit erased settings", changed)
	}
	result := call(t, registry, "list_shows", map[string]any{"station_id": owner.ID})
	raw, _ = json.Marshal(result.Data)
	var rows []map[string]any
	json.Unmarshal(raw, &rows)
	seen := false
	for _, row := range rows {
		if row["ID"] == created[0].ID {
			seen = true
			if row["music_direction_truncated"] != true || !strings.HasPrefix(row["music_direction"].(string), "Start with bright") {
				t.Fatal(row)
			}
		}
	}
	if !seen {
		t.Fatal("missing prompt in schedule readback", string(raw))
	}
	for _, name := range []string{"save_show", "create_shows"} {
		tool, _ := registry.Find(name)
		schema, _ := json.Marshal(tool.Parameters)
		if !strings.Contains(string(schema), "music_direction") || !strings.Contains(string(schema), "DJ music prompt") || strings.Contains(string(schema), `"rotation_format":`) {
			t.Fatal("outdated show schema", name, string(schema))
		}
	}
}
func TestAssistantEpisodeToolsAreReadOnlyUntilProducerIntervention(t *testing.T) {
	_, registry, stations, _, shows, _ := fixture(t)
	ctx := context.Background()
	owner := call(t, registry, "save_station", map[string]any{"name": "Episodes", "timezone": "Europe/London"}).Data.(station.Station)
	pool := call(t, registry, "save_playlist", map[string]any{"station_id": owner.ID, "name": "Music"})
	raw, _ := json.Marshal(pool.Data)
	var p struct{ ID string }
	json.Unmarshal(raw, &p)
	loc, _ := time.LoadLocation(owner.Timezone)
	tomorrow := time.Now().In(loc).AddDate(0, 0, 1)
	day := (int(tomorrow.Weekday()) + 6) % 7
	show := call(t, registry, "save_show", map[string]any{"station_id": owner.ID, "name": "Tomorrow", "dj_id": "default-pop", "playlist_id": p.ID, "day": day, "start": 540, "end": 600, "music_direction": "A varied mix."}).Data.(schedule.Show)
	var o schedule.Occurrence
	for _, candidate := range schedule.Occurrences([]schedule.Show{show}, tomorrow, loc) {
		if candidate.Start.YearDay() == tomorrow.YearDay() && candidate.Start.Year() == tomorrow.Year() {
			o = candidate
		}
	}
	base := map[string]any{"station_id": owner.ID, "show_id": show.ID, "start": o.Start.Format(time.RFC3339)}
	call(t, registry, "list_episodes", map[string]any{"station_id": owner.ID, "week": tomorrow.Format("2006-01-02"), "limit": 1})
	result := call(t, registry, "get_episode", base)
	data := result.Data.(map[string]any)
	if data["status"] != "Scheduled" || data["version"] != 0 || !strings.Contains(result.URL, "/episode?start=") {
		t.Fatal(result)
	}
	var count int
	if err := stations.DB.QueryRow(`SELECT count(*) FROM show_episodes`).Scan(&count); err != nil || count != 0 {
		t.Fatal("inspection started preparation", count, err)
	}
	store := persistence.Episodes{DB: stations.DB}
	if err := store.Update(ctx, o, func(e *episodes.Episode) error {
		e.Plan = []episodes.Track{{ID: "a", Title: "First"}, {ID: "b", Title: "Second", Locked: true}}
		e.Theme = "Night journeys"
		e.Note("private session", "not part of tool readback")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page := map[string]any{"station_id": owner.ID, "show_id": show.ID, "start": o.Start.Format(time.RFC3339), "limit": 1}
	result = call(t, registry, "get_episode", page)
	data = result.Data.(map[string]any)
	if data["plan_total"] != 2 || len(data["tracks"].([]episodes.Track)) != 1 {
		t.Fatal(result)
	}
	raw, _ = json.Marshal(result.Data)
	if strings.Contains(string(raw), "not part of tool readback") {
		t.Fatal("session transcript leaked")
	}
	version := data["version"].(int)
	edit := map[string]any{"station_id": owner.ID, "show_id": show.ID, "start": o.Start.Format(time.RFC3339), "version": version, "notes": "Start gently and build energy.", "exclude_track_ids": []string{"a"}}
	call(t, registry, "save_episode", edit)
	saved, _ := store.Get(ctx, o.Key)
	if saved.Notes != "Start gently and build energy." || !saved.Plan[1].Locked || len(saved.Excluded) != 1 || saved.Status != "attention" {
		t.Fatal(saved)
	}
	stale, _ := json.Marshal(edit)
	if result := registry.Call(ctx, "save_episode", stale); !strings.Contains(result.Error, "episode changed") {
		t.Fatal("stale producer edit accepted", result)
	}
	other := call(t, registry, "save_station", map[string]any{"name": "Other"}).Data.(station.Station)
	wrong, _ := json.Marshal(map[string]any{"station_id": other.ID, "show_id": show.ID, "start": o.Start.Format(time.RFC3339)})
	if result := registry.Call(ctx, "get_episode", wrong); result.Error == "" {
		t.Fatal("foreign show accepted")
	}
	// None of the episode operations changes the recurring show prompt.
	recurring, err := shows.Get(ctx, owner.ID, show.ID)
	if err != nil || recurring.MusicDirection != "A varied mix." {
		t.Fatal(recurring, err)
	}
	for _, name := range []string{"get_show", "list_episodes", "get_episode"} {
		tool, ok := registry.Find(name)
		if !ok || tool.Mutates {
			t.Fatal("read tool mutates", name)
		}
	}
}
