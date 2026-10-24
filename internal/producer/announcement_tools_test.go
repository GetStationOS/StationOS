package producer_test

import (
	"context"
	"encoding/json"
	"stationos.local/stationos/internal/announcements"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
	"time"
)

func TestAnnouncementAssistantToolsRespectVersionsAndSchemas(t *testing.T) {
	_, _, stations, _, shows, DJs := fixture(t)
	ctx := context.Background()
	owner := station.Station{ID: "announcements", Name: "Announcements", Timezone: "UTC", Locale: "en"}
	if err := stations.Save(ctx, owner); err != nil {
		t.Fatal(err)
	}
	repo := persistence.Announcements{DB: stations.DB}
	r := producer.NewRegistry(producer.Dependencies{Announcements: repo, Stations: stations, Schedule: shows, DJs: DJs})
	tool, ok := r.Find("save_announcement")
	if !ok {
		t.Fatal("tool absent")
	}
	definition, _ := json.Marshal(tool.Parameters)
	if !strings.Contains(string(definition), `"format":"date-time"`) || !strings.Contains(string(definition), `"required"`) {
		t.Fatal(string(definition))
	}
	now := time.Now()
	args := map[string]any{"station_id": owner.ID, "title": "Test news", "text": "Producer-supplied facts.", "starts": now.Add(-time.Hour).Format(time.RFC3339), "expires": now.Add(time.Hour).Format(time.RFC3339)}
	result := call(t, r, "save_announcement", args)
	m := result.Data.(announcements.Message)
	if m.Status != "draft" || m.Mode != "exact" || m.Version != 1 {
		t.Fatal(m)
	}
	call(t, r, "set_announcement_delivery", map[string]any{"station_id": owner.ID, "id": m.ID, "version": 1, "action": "next"})
	saved, err := repo.Get(ctx, owner.ID, m.ID)
	if err != nil || !saved.Next || saved.Status != "active" || saved.Plays != 0 {
		t.Fatal(saved, err)
	}
	raw, _ := json.Marshal(map[string]any{"station_id": owner.ID, "id": m.ID, "version": 1, "action": "paused"})
	if result := r.Call(ctx, "set_announcement_delivery", raw); result.Error == "" {
		t.Fatal("stale delivery action accepted")
	}
	args["id"] = m.ID
	args["version"] = 2
	args["status"] = "paused"
	args["mode"] = "adapt"
	args["repeat"] = "interval"
	args["interval_minutes"] = 90
	call(t, r, "save_announcement", args)
	call(t, r, "save_announcement_settings", map[string]any{"station_id": owner.ID, "version": 0, "gap_minutes": 15, "dj_id": ""})
	raw, _ = json.Marshal(map[string]any{"station_id": owner.ID, "version": 0, "gap_minutes": 5, "dj_id": ""})
	if r.Call(ctx, "save_announcement_settings", raw).Error == "" {
		t.Fatal("stale settings accepted")
	}
}
