package producer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecoveryFindsStationOutsideVisibleActionWindow(t *testing.T) {
	items := []Item{
		mustJSON(map[string]any{"role": "assistant", "content": RecordedResultPrefix + `{"summary":"Saved station: Radio","data":{"ID":"old-station"}}`}),
		mustJSON(map[string]any{"role": "assistant", "content": RecordedResultPrefix + `{"data":{"StationID":"pool-station","ID":"not-a-station"}}`}),
		mustJSON(map[string]any{"role": "assistant", "content": RecordedResultPrefix + `{"error":"not saved","data":{"StationID":"failed-station"}}`}),
	}
	raw, _ := json.Marshal(items)
	ids := conversationStations(Conversation{Transcript: raw, Messages: []Message{{Role: "action", Text: "AI usage"}}})
	if len(ids) != 2 || !ids["old-station"] || !ids["pool-station"] {
		t.Fatal(ids)
	}
}
func TestMeasuredGapsHandleSundayOvernight(t *testing.T) {
	raw, _ := json.Marshal(scheduleGaps([]namedShow{{Day: 6, Start: 1380, End: 60}}))
	text := string(raw)
	for _, want := range []string{`"day_name":"Monday","end_time":"24:00","start_time":"01:00"`, `"day_name":"Sunday","end_time":"23:00","start_time":"00:00"`, `"day_name":"Saturday","end_time":"24:00","start_time":"00:00"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
}

func TestFocusedToolsCapReadsAndPreserveWrites(t *testing.T) {
	tools := []Tool{{Name: "list_voices"}, {Name: "get_playlist"}, {Name: "explore_music"}, {Name: "add_playlist_rule", Mutates: true}, {Name: "save_task_progress", Mutates: true}, {Name: "save_show", Mutates: true}}
	got := focusedTools(tools, "playlists", map[string]int{"get_playlist": 2})
	names := map[string]bool{}
	for _, tool := range got {
		names[tool.Name] = true
	}
	if names["list_voices"] || names["get_playlist"] || names["save_show"] || !names["explore_music"] || !names["add_playlist_rule"] || !names["save_task_progress"] {
		t.Fatal(names)
	}
	got = focusedTools(tools, "schedule", nil)
	names = map[string]bool{}
	for _, tool := range got {
		names[tool.Name] = true
	}
	if names["add_playlist_rule"] || !names["save_show"] || !names["get_playlist"] {
		t.Fatal(names)
	}
}

func TestUnscopedRecoveryOnlyOffersStepSelection(t *testing.T) {
	tools := []Tool{{Name: "list_stations"}, {Name: "list_playlists"}, {Name: "get_station"}, {Name: "save_station", Mutates: true}, {Name: "save_task_progress", Mutates: true}}
	for _, focus := range []string{"choose", "remaining", "unknown"} {
		offered := focusedTools(tools, focus, nil)
		names := map[string]bool{}
		for _, tool := range offered {
			names[tool.Name] = true
		}
		if len(names) != 2 || !names["select_work_step"] || !names["save_task_progress"] {
			t.Fatal("unscoped recovery reopened discovery", focus, names)
		}
	}
}
