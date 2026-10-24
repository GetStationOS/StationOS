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

func TestToolAvailabilityStableAcrossRecovery(t *testing.T) {
	tools := []Tool{{Name: "summarize_music"}, {Name: "list_playlists"}, {Name: "create_playlist", Mutates: true}, {Name: "create_show", Mutates: true}, {Name: "save_task_progress", Mutates: true}}
	for _, focus := range []string{"", "choose", "playlists", "schedule", "descriptions", "review", "unknown"} {
		got := agentTools(tools)
		if len(got) != len(tools)+1 {
			t.Fatal(focus, got)
		}
		for i, v := range tools {
			if got[i].Name != v.Name {
				t.Fatal(focus, got)
			}
		}
	}
}
