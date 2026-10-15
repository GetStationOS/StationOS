package producer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

type partialRecoveryModel struct {
	t                            *testing.T
	n                            int
	station, pool, dj            string
	recovered                    bool
	firstFocus                   bool
	contextFailure, failedBudget bool
	inputFailure                 bool
}

func (m *partialRecoveryModel) Respond(_ context.Context, _, _, prompt string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	if m.contextFailure && !m.failedBudget {
		m.failedBudget = true
		if m.inputFailure {
			return producer.Response{Usage: producer.Usage{Input: 16384, Output: 457}}, producer.ErrInputContextLimit
		}
		return producer.Response{}, producer.ErrContextBudget
	}
	n := m.n
	if m.firstFocus {
		n += 4
	}
	m.n++
	all, _ := json.Marshal(input)
	text := string(all)
	if n == 0 || n == 4 {
		for _, want := range []string{"unscheduled_periods", "18:00", "Saturday", "Sunday", m.pool} {
			if !strings.Contains(text, want) {
				m.t.Errorf("missing measured state %q", want)
			}
		}
	}
	// This model keeps choosing a harmless check whenever it is offered.
	// A reminder alone cannot make this test advance: recovery must narrow tools.
	if !m.recovered {
		for _, tool := range tools {
			if tool.Name == "list_voices" {
				return responseCall(n, "list_voices", map[string]any{}), nil
			}
		}
		m.recovered = true
		if n != 4 {
			m.t.Errorf("unexpected recovery round %d", n)
		}
		for _, tool := range tools {
			if tool.Name == "save_show" || tool.Name == "delete_station" {
				m.t.Errorf("unrelated tool remained in playlist recovery: %s", tool.Name)
			}
		}
	}
	if n == 4 {
		if !m.firstFocus && !strings.Contains(text, "Progress recovery:") {
			m.t.Error("no early recovery before stall")
		}
		return responseCall(n, "add_playlist_rule", map[string]any{"playlist_id": m.pool, "rule": map[string]string{"kind": "genre", "value": "Jazz"}}), nil
	}
	if n == 5 || n == 6 {
		days := []int{0, 1, 2, 3, 4, 5, 6}
		start, end := 1080, 0
		name := "Evening Jazz"
		if n == 6 {
			days = []int{5, 6}
			start, end = 0, 1080
			name = "Weekend Jazz"
		}
		return responseCall(n, "create_shows", map[string]any{"show": map[string]any{"station_id": m.station, "name": name, "dj_id": m.dj, "playlist_id": m.pool, "start": start, "end": end}, "days": days}), nil
	}
	if n == 7 {
		return responseCall(n, "save_task_progress", producer.Task{Brief: "Complete our 24/7 jazz schedule and populate the music pool", Completed: []string{"Verified weekday and weekend coverage and music eligibility"}}), nil
	}
	return producer.Response{Text: "The saved schedule and music pool are complete."}, nil
}

func TestResumedPartialStationRecoversFromUnchangedReads(t *testing.T) {
	for _, scenario := range []struct {
		name            string
		focused, budget bool
		input           bool
	}{{"mid_run_stall", false, false, false}, {"stalled_resume", true, false, false}, {"context_recovery", false, true, false}, {"reported_input_overflow", false, true, true}} {
		focused := scenario.focused
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			store, r, stations, pools, shows, _ := fixture(t)
			owner := call(t, r, "save_station", map[string]string{"name": "Partial Radio"}).Data.(station.Station)
			pool := call(t, r, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Jazz"}).Data.(playlists.Playlist)
			dj := call(t, r, "save_dj", map[string]string{"name": "Existing Presenter"}).Data.(djs.Profile)
			call(t, r, "create_shows", map[string]any{"show": map[string]any{"station_id": owner.ID, "name": "Existing Daytime", "dj_id": dj.ID, "playlist_id": pool.ID, "start": 0, "end": 1080}, "days": []int{0, 1, 2, 3, 4}})
			before, _ := shows.List(ctx, owner.ID)
			src := sources.Config{ID: "library", Name: "Music", Kind: "upnp", Endpoint: "http://server/desc", Revision: 1}
			if err := (persistence.Sources{DB: stations.DB}).Save(ctx, src); err != nil {
				t.Fatal(err)
			}
			item := content.AudioItem{ID: content.ID(src.ID, "one"), SourceID: src.ID, SourceItemID: "one", Title: "Library Jazz", Genre: "Jazz", Kind: content.Track, Origin: content.Original}
			if err := (persistence.Catalogue{DB: stations.DB}).Replace(ctx, src, []content.AudioItem{item}); err != nil {
				t.Fatal(err)
			}
			_ = store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"})
			model := &partialRecoveryModel{t: t, station: owner.ID, pool: pool.ID, dj: dj.ID, firstFocus: focused || scenario.budget, contextFailure: scenario.budget, inputFailure: scenario.input}
			svc, err := producer.New(ctx, store, r, model)
			if err != nil {
				t.Fatal(err)
			}
			defer svc.Close()
			// A project-wide conversation finds its station through the durable action
			// link rather than guessing which of the server's stations to configure.
			id, _ := svc.Create(ctx, "")
			if err := store.Begin(ctx, id, producer.ID(), "Complete our 24/7 jazz schedule and populate the music pool"); err != nil {
				t.Fatal(err)
			}
			_ = store.Append(ctx, id, producer.Message{Role: "action", Text: "Saved station", URL: "/producer/stations/" + owner.ID})
			task := producer.Task{Brief: "Complete our 24/7 jazz schedule and populate the music pool", Remaining: []string{"Fill evenings and weekends", "Populate pool"}}
			if err := store.Checkpoint(ctx, id, []byte(`[]`), task); err != nil {
				t.Fatal(err)
			}
			previousStop := "Previous run stopped"
			if focused {
				previousStop = "The assistant stopped making progress. Completed actions remain saved."
			}
			_ = store.Finish(ctx, id, "failed", []byte(`[]`), producer.Message{Role: "assistant", Text: previousStop})
			if err := svc.Start(ctx, id, producer.ID(), "Continue the saved request"); err != nil {
				t.Fatal(err)
			}
			c := waitDone(t, store, id)
			expected := 10
			if focused || scenario.budget {
				expected = 6
			}
			if c.Status != "idle" || model.n != expected {
				t.Fatalf("recovery failed: %s %d %+v", c.Status, model.n, c.Messages)
			}
			saved, err := shows.List(ctx, owner.ID)
			if err != nil {
				t.Fatal(err)
			}
			var covered [schedule.WeekMinutes]int
			for _, show := range saved {
				for minute := 0; minute < show.Duration(); minute++ {
					covered[(show.Day*1440+show.Start+minute)%schedule.WeekMinutes]++
				}
			}
			for minute, count := range covered {
				if count != 1 {
					t.Fatalf("minute %d has %d shows", minute, count)
				}
			}
			for _, old := range before {
				current, err := shows.Get(ctx, owner.ID, old.ID)
				if err != nil || current.Revision != old.Revision {
					t.Fatal("earlier edition changed", current, err)
				}
			}
			resolved, err := pools.Resolve(ctx, pool.ID, 0, 1)
			if err != nil || resolved.Total-resolved.Unavailable != 1 {
				t.Fatal("music pool still empty", resolved, err)
			}
		})
	}
}

func TestPlaylistAvailabilityMatchesResolvedEligibility(t *testing.T) {
	ctx := context.Background()
	_, r, stations, pools, _, _ := fixture(t)
	owner := call(t, r, "save_station", map[string]string{"name": "Counts Radio"}).Data.(station.Station)
	pool := call(t, r, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Eligibility"}).Data.(playlists.Playlist)
	counts, err := pools.Availability(ctx, pool.ID)
	if err != nil || counts.Total != 0 || counts.Unavailable != 0 {
		t.Fatal(counts, err)
	}
	src := sources.Config{ID: "counts-source", Name: "Music", Kind: "upnp", Endpoint: "http://server/desc", Revision: 1}
	if err := (persistence.Sources{DB: stations.DB}).Save(ctx, src); err != nil {
		t.Fatal(err)
	}
	items := []content.AudioItem{}
	for _, id := range []string{"one", "two", "manual"} {
		items = append(items, content.AudioItem{ID: content.ID(src.ID, id), SourceID: src.ID, SourceItemID: id, Title: id, Genre: "Jazz", Album: "Album", Kind: content.Track, Origin: content.Original})
	}
	catalogue := persistence.Catalogue{DB: stations.DB}
	if err := catalogue.Replace(ctx, src, items); err != nil {
		t.Fatal(err)
	}
	if err := pools.AddRule(ctx, pool.ID, playlists.Rule{ID: "genre", Kind: "genre", Value: "Jazz"}); err != nil {
		t.Fatal(err)
	}
	if err := pools.AddRule(ctx, pool.ID, playlists.Rule{ID: "album", Kind: "album", Value: "Album", SourceID: src.ID}); err != nil {
		t.Fatal(err)
	}
	if err := pools.SetIncluded(ctx, pool.ID, []string{items[0].ID}, false); err != nil {
		t.Fatal(err)
	}
	if err := pools.SetIncluded(ctx, pool.ID, []string{items[2].ID}, true); err != nil {
		t.Fatal(err)
	}
	if err := catalogue.Replace(ctx, src, items[:2]); err != nil {
		t.Fatal(err)
	}
	resolved, err := pools.Resolve(ctx, pool.ID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	counts, err = pools.Availability(ctx, pool.ID)
	if err != nil || counts.Total != resolved.Total || counts.Unavailable != resolved.Unavailable || counts.Total != 2 || counts.Unavailable != 1 {
		t.Fatal("aggregate diverged from membership", counts, resolved, err)
	}
}

type ignoredFocusModel struct {
	n       int
	station string
}

func (m *ignoredFocusModel) Respond(_ context.Context, _, _, _ string, _ []producer.Item, _ []producer.Tool) (producer.Response, error) {
	n := m.n
	m.n++
	if n < 4 {
		return responseCall(n, "list_voices", map[string]any{}), nil
	}
	if n == 4 {
		return responseCall(n, "delete_station", map[string]string{"id": m.station}), nil
	}
	return responseCall(n, "save_task_progress", producer.Task{Brief: "Configure the requested station", Remaining: []string{fmt.Sprintf("Still checking revision %d", n)}}), nil
}
func TestFocusedRecoveryRejectsUnofferedWritesAndChecklistLoops(t *testing.T) {
	ctx := context.Background()
	store, r, stations, _, _, _ := fixture(t)
	owner := call(t, r, "save_station", map[string]string{"name": "Preserved Radio"}).Data.(station.Station)
	call(t, r, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Unfilled"})
	_ = store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"})
	model := &ignoredFocusModel{station: owner.ID}
	svc, err := producer.New(ctx, store, r, model)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	id, _ := svc.Create(ctx, owner.ID)
	if err := store.Begin(ctx, id, producer.ID(), "Configure this station"); err != nil {
		t.Fatal(err)
	}
	if err := store.Checkpoint(ctx, id, []byte(`[]`), producer.Task{Brief: "Configure this station", Remaining: []string{"Populate its pool"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, id, "failed", []byte(`[]`), producer.Message{Role: "assistant", Text: "Previous run stopped"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, id, producer.ID(), "Configure this station"); err != nil {
		t.Fatal(err)
	}
	c := waitDone(t, store, id)
	if c.Status != "failed" || model.n > producer.MaxNoProgressRounds+1 {
		t.Fatal("checklist rewrites bypassed stall limit", c.Status, model.n)
	}
	if _, err := stations.Get(ctx, owner.ID); err != nil {
		t.Fatal("unoffered write ran", err)
	}
	for _, message := range c.Messages {
		if message.Text == "Started: delete station" {
			t.Fatal("unoffered mutation was started")
		}
	}
	last := c.Messages[len(c.Messages)-1].Text
	if !strings.Contains(last, "Most frequent checks: list_voices") || strings.Contains(last, "use Continue to resume") {
		t.Fatal("missing actionable stall diagnosis", last)
	}
}

// Even before a station has been bound, an actual input rejection receives only
// one compacted retry. Calls attached to a rejected response must never run.
type rejectedInputModel struct {
	t     *testing.T
	calls int
}

func (m *rejectedInputModel) Respond(context.Context, string, string, string, []producer.Item, []producer.Tool) (producer.Response, error) {
	m.t.Fatal("options-aware response path was bypassed")
	return producer.Response{}, nil
}

func (m *rejectedInputModel) RespondWithOptions(_ context.Context, _, _, _ string, _ []producer.Item, tools []producer.Tool, options ai.GenerationOptions) (producer.Response, error) {
	m.calls++
	if m.calls == 2 {
		if !options.CompactContext {
			m.t.Error("recovery did not request compacted context")
		}
		for _, tool := range tools {
			if tool.Name != "select_work_step" && tool.Name != "save_task_progress" {
				m.t.Errorf("unfocused recovery tool: %s", tool.Name)
			}
		}
	}
	return producer.Response{Calls: []producer.Call{{ID: "unsafe", Name: "save_station", Arguments: json.RawMessage(`{"name":"Must not exist"}`)}}}, producer.ErrInputContextLimit
}

func TestUnboundInputContextRecoveryIsBoundedAndCannotExecuteRejectedCalls(t *testing.T) {
	ctx := context.Background()
	store, registry, stations, _, _, _ := fixture(t)
	if err := store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"}); err != nil {
		t.Fatal(err)
	}
	model := &rejectedInputModel{t: t}
	svc, err := producer.New(ctx, store, registry, model)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	id, err := svc.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, id, producer.ID(), "Create a jazz station"); err != nil {
		t.Fatal(err)
	}
	conversation := waitDone(t, store, id)
	if conversation.Status != "failed" || model.calls != 2 {
		t.Fatalf("recovery was not bounded: %s, %d requests", conversation.Status, model.calls)
	}
	saved, err := stations.List(ctx)
	if err != nil || len(saved) != 0 {
		t.Fatal("rejected response changed configuration", saved, err)
	}
}
