package producer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/inference"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/sources"
)

func TestLocalAIResumesLargeHistoryAndBuildsApprovedWeeklyStation(t *testing.T) {
	ctx := context.Background()
	store, registry, stations, pools, shows, djs := fixture(t)
	source, err := (sources.Service{Repo: persistence.Sources{DB: stations.DB}}).Save(ctx, sources.Config{Name: "Music", Kind: "upnp", Endpoint: "http://music/description"})
	if err != nil {
		t.Fatal(err)
	}
	// A measured summary can establish genre eligibility without fetching all tracks.
	if _, err = stations.DB.Exec(`INSERT INTO catalogue_items(id,source_id,source_item_id,title,artist,album,metadata) VALUES('jazz-track',?,'jazz-track','Jazz recording','Quartet','Session','{"genre":"Jazz","duration_ns":180000000000}')`, source.ID); err != nil {
		t.Fatal(err)
	}
	const brief = "Create Velvet Notes, an original jazz station with Julian Vance and five distinct eligibility pools."
	const plan = "Weekdays: Morning Brew 08:00–11:00; Midday Melodies 11:00–14:00; Afternoon Aesthetics 14:00–17:00; Evening Elegance 17:00–20:00; Twilight Tunes 20:00–22:00. Saturday Soirée 18:00–22:00. Sunday Sanctuary 10:00–14:00."
	names := []string{"Morning Brew", "Midday Melodies", "Afternoon Aesthetics", "Evening Elegance", "Twilight Tunes", "Saturday Soirée", "Sunday Sanctuary"}
	starts := []int{480, 660, 840, 1020, 1200, 1080, 600}
	ends := []int{660, 840, 1020, 1200, 1320, 1320, 840}
	var mu sync.Mutex
	next := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var body struct {
			Messages json.RawMessage
			Tools    json.RawMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		text := string(body.Messages)
		if !strings.Contains(text, brief) || !strings.Contains(text, "Sunday Sanctuary") || !strings.Contains(text, "Yes, please proceed") {
			t.Error("lost approved requirements")
		}
		if len(body.Messages)+len(body.Tools) > (16384-2048)*3 {
			t.Error("oversized request reached local provider")
		}
		n := next
		next++
		tool := ""
		var args any
		savedStations, _ := stations.List(ctx)
		stationID := ""
		if len(savedStations) > 0 {
			stationID = savedStations[0].ID
		}
		savedPools, _ := pools.List(ctx, stationID)
		poolID := func(index int) string {
			for _, p := range savedPools {
				if p.Name == names[index] {
					return p.ID
				}
			}
			return ""
		}
		if n > 2 && (stationID == "" || !strings.Contains(text, stationID)) {
			t.Error("lost committed station ID")
		}
		switch {
		case n == 0:
			tool = "save_task_progress"
			args = producer.Task{Brief: brief + " " + plan, Remaining: []string{"Create station, pools and DJ", "Create all weekday and weekend shows", "Assign default playlist"}}
		case n == 1:
			tool = "summarize_music"
			args = map[string]any{"genre": "Jazz"}
		case n == 2:
			tool = "save_station"
			args = map[string]any{"name": "Velvet Notes"}
		case n >= 3 && n <= 12:
			index := (n - 3) / 2
			if (n-3)%2 == 0 {
				tool = "save_playlist"
				args = map[string]any{"station_id": stationID, "name": names[index]}
			} else {
				tool = "add_playlist_rule"
				args = map[string]any{"playlist_id": poolID(index), "rule": map[string]string{"kind": "genre", "value": "Jazz"}}
			}
		case n == 13:
			tool = "save_dj"
			args = map[string]string{"name": "Julian Vance", "voice": "onyx"}
		case n >= 14 && n <= 20:
			index := n - 14
			days := []int{0, 1, 2, 3, 4}
			if index == 5 {
				days = []int{5}
			}
			if index == 6 {
				days = []int{6}
			}
			allDJs, _ := djs.List(ctx)
			djID := ""
			for _, d := range allDJs {
				if d.Name == "Julian Vance" {
					djID = d.ID
				}
			}
			tool = "create_shows"
			args = map[string]any{"show": map[string]any{"station_id": stationID, "name": names[index], "dj_id": djID, "playlist_id": poolID(index % 5), "start": starts[index], "end": ends[index]}, "days": days}
		case n == 21:
			tool = "assign_default_playlist"
			args = map[string]string{"station_id": stationID, "playlist_id": poolID(0)}
		case n == 22:
			tool = "save_task_progress"
			args = producer.Task{Brief: brief + " " + plan, Completed: []string{"Created station, five eligible Jazz pools, Julian Vance, 27 scheduled shows and default playlist"}}
		default:
			if n >= 24 && (!strings.Contains(text, "Mandatory schedule readback") || !strings.Contains(text, "Saturday") || !strings.Contains(text, "Sunday")) {
				t.Error("completion accepted without measured schedule readback")
			}
			json.NewEncoder(w).Encode(map[string]any{"done": true, "message": map[string]any{"content": "Configured Velvet Notes with all approved shows."}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"done": true, "message": map[string]any{"tool_calls": []any{map[string]any{"function": map[string]any{"name": tool, "arguments": args}}}}})
	}))
	defer server.Close()
	providers := store.(ai.Store)
	if err = providers.SaveConnection(ctx, ai.Connection{ID: "local", Name: "Local AI", Type: "ollama", Address: server.URL, ContextTokens: 16384, OutputTokens: 2048}, ai.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	settings, _ := store.Settings(ctx)
	settings.Model = "gemma4:12b"
	settings.Routes.Assistant.Connection = "local"
	if err = store.Configure(ctx, settings, producer.KeyChange{Clear: true}); err != nil {
		t.Fatal(err)
	}
	svc, err := producer.New(ctx, store, registry, inference.New(providers, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	id, _ := svc.Create(ctx, "")
	if err = store.Begin(ctx, id, producer.ID(), brief); err != nil {
		t.Fatal(err)
	}
	items := []producer.Item{}
	for _, v := range []map[string]string{{"role": "user", "content": brief}, {"role": "assistant", "content": producer.RecordedResultPrefix + `{"data":{"items":[{"ID":"old-track","Title":"` + strings.Repeat("old catalogue detail ", 6500) + `"}],"total":100000}}`}, {"role": "assistant", "content": plan}} {
		raw, _ := json.Marshal(v)
		items = append(items, raw)
	}
	transcript, _ := json.Marshal(items)
	if err = store.Finish(ctx, id, "failed", transcript, producer.Message{Role: "assistant", Text: plan}); err != nil {
		t.Fatal(err)
	}
	if err = svc.Start(ctx, id, producer.ID(), "Yes, please proceed"); err != nil {
		t.Fatal(err)
	}
	conversation := waitDone(t, store, id)
	if conversation.Status != "idle" || len(conversation.Task.Remaining) > 0 {
		t.Fatal("local setup did not finish", conversation.Status, conversation.Messages)
	}
	savedStations, _ := stations.List(ctx)
	if len(savedStations) != 1 || savedStations[0].Name != "Velvet Notes" {
		t.Fatal(savedStations)
	}
	savedPools, _ := pools.List(ctx, savedStations[0].ID)
	savedShows, _ := shows.List(ctx, savedStations[0].ID)
	if len(savedPools) != 5 || len(savedShows) != 27 {
		t.Fatal("missing or duplicate configuration", len(savedPools), len(savedShows))
	}
	if next < 25 {
		t.Fatal("final answer was accepted before schedule verification", next)
	}
	for _, show := range savedShows {
		if (show.Name == "Saturday Soirée" && show.Day != 5) || (show.Name == "Sunday Sanctuary" && show.Day != 6) {
			t.Fatal("weekend special on wrong day", show)
		}
	}
	if strings.Contains(string(conversation.Transcript), "old catalogue detail") {
		t.Fatal("catalogue details remained after summary")
	}
}
