package producer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"stationos.local/stationos/internal/producer"
)

const fullBrief = "Create The Listening Room with a jazz playlist, a fictional presenter, shows on Friday and Sunday from 22:00 to 01:00, and assign the default playlist."

func responseCall(n int, name string, args any) producer.Response {
	raw, _ := json.Marshal(args)
	id := fmt.Sprintf("call-%d", n)
	item, _ := json.Marshal(map[string]any{"type": "function_call", "call_id": id, "name": name, "arguments": string(raw)})
	return producer.Response{Items: []producer.Item{item}, Calls: []producer.Call{{ID: id, Name: name, Arguments: raw}}}
}
func resultID(input []producer.Item) string {
	for i := len(input) - 1; i >= 0; i-- {
		var v struct {
			Type   string `json:"type"`
			Output string `json:"output"`
		}
		_ = json.Unmarshal(input[i], &v)
		if v.Type == "function_call_output" {
			var result struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			_ = json.Unmarshal([]byte(v.Output), &result)
			if result.Data.ID != "" {
				return result.Data.ID
			}
		}
	}
	return ""
}

type fullStationModel struct {
	n                     int
	station, playlist, dj string
	t                     *testing.T
}

func (m *fullStationModel) Respond(ctx context.Context, model, key, prompt string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	if !strings.Contains(prompt, fullBrief) {
		m.t.Error("original requirements lost from briefing")
	}
	n := m.n
	m.n++
	switch {
	case n == 0:
		return responseCall(n, "save_task_progress", producer.Task{Brief: fullBrief, Remaining: []string{"Create station and playlist", "Create DJ", "Schedule Friday and Sunday shows", "Assign default playlist"}}), nil
	case n == 1:
		return responseCall(n, "save_station", map[string]string{"name": "The Listening Room"}), nil
	case n == 2:
		m.station = resultID(input)
		return responseCall(n, "save_playlist", map[string]string{"station_id": m.station, "name": "Jazz"}), nil
	case n <= 30:
		if n == 3 {
			m.playlist = resultID(input)
		}
		// Exercise a long sequence of actual configuration edits across batch
		// compaction; read-only churn is deliberately bounded by the runner.
		return responseCall(n, "save_playlist", map[string]string{"id": m.playlist, "description": fmt.Sprintf("Eligibility refinement %d", n)}), nil
	case n == 31:
		return responseCall(n, "save_station", map[string]string{"id": m.station, "description": "Jazz station"}), nil
	case n == 32:
		m.station = resultID(input)
		return responseCall(n, "save_playlist", map[string]string{"id": m.playlist, "description": "Verified jazz pool"}), nil
	case n == 33:
		m.playlist = resultID(input)
		return responseCall(n, "add_playlist_rule", map[string]any{"playlist_id": m.playlist, "rule": map[string]string{"kind": "genre", "value": "Jazz"}}), nil
	case n == 34:
		return responseCall(n, "save_dj", map[string]string{"name": "Maya Night"}), nil
	case n == 35:
		m.dj = resultID(input)
		return responseCall(n, "create_shows", map[string]any{"show": map[string]any{"station_id": m.station, "name": "Late Jazz", "dj_id": m.dj, "playlist_id": m.playlist, "start": 1320, "end": 60}, "days": []int{4, 6}}), nil
	case n == 36:
		return responseCall(n, "assign_default_playlist", map[string]string{"station_id": m.station, "playlist_id": m.playlist}), nil
	case n == 37:
		return responseCall(n, "save_task_progress", producer.Task{Brief: fullBrief, Completed: []string{"Created station " + m.station, "Configured playlist " + m.playlist, "Created presenter " + m.dj, "Scheduled both overnight shows", "Assigned default playlist"}, Blockers: nil}), nil
	default:
		return producer.Response{Text: "All requested station configuration is complete. All services use the configured shared key."}, nil
	}
}
func TestFullRequestContinuesBeyondBatchLimit(t *testing.T) {
	store, r, stations, _, shows, _ := fixture(t)
	ctx := context.Background()
	_ = store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"})
	model := &fullStationModel{t: t}
	svc, e := producer.New(ctx, store, r, model)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(svc.Close)
	id, _ := svc.Create(ctx, "")
	if e = svc.Start(ctx, id, producer.ID(), fullBrief); e != nil {
		t.Fatal(e)
	}
	c := waitDone(t, store, id)
	if c.Status != "idle" || len(c.Task.Remaining) != 0 || len(c.Task.Completed) != 5 || model.n <= producer.BatchRounds {
		t.Fatal("request was not completed", c.Task, c.Status, model.n)
	}
	saved, _ := stations.List(ctx)
	if len(saved) != 1 {
		t.Fatal("duplicated or missing station", saved)
	}
	scheduled, e := shows.List(ctx, saved[0].ID)
	if e != nil || len(scheduled) != 2 {
		t.Fatal("shows missing", scheduled, e)
	}
	if len(c.Requests) != 1 || c.Requests[0] != fullBrief {
		t.Fatal("automatic continuation changed human instructions", c.Requests)
	}
}

type prematureModel struct{ n int }

func (m *prematureModel) Respond(ctx context.Context, model, key, prompt string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	n := m.n
	m.n++
	switch n {
	case 0:
		return responseCall(n, "save_task_progress", producer.Task{Brief: "Answer the request", Remaining: []string{"Finish answer"}}), nil
	case 1:
		return producer.Response{Text: "Ask me to continue."}, nil
	case 2:
		return responseCall(n, "save_task_progress", producer.Task{Brief: "Answer the request", Completed: []string{"Finished answer"}}), nil
	default:
		return producer.Response{Text: "Finished the request."}, nil
	}
}
func TestUnfinishedChecklistContinuesWithoutHumanPrompt(t *testing.T) {
	store, r, _, _, _, _ := fixture(t)
	ctx := context.Background()
	_ = store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"})
	svc, e := producer.New(ctx, store, r, &prematureModel{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(svc.Close)
	id, _ := svc.Create(ctx, "")
	_ = svc.Start(ctx, id, producer.ID(), "Answer my request")
	c := waitDone(t, store, id)
	if c.Status != "idle" || len(c.Task.Completed) != 1 || len(c.Requests) != 1 {
		t.Fatal(c)
	}
	for _, message := range c.Messages {
		if message.Text == "Ask me to continue." {
			t.Fatal("premature response was presented as completion")
		}
	}
}

type stalledModel struct{ n int }

func (m *stalledModel) Respond(ctx context.Context, model, key, prompt string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	n := m.n
	m.n++
	return responseCall(n, "list_voices", map[string]any{}), nil
}
func TestRepeatedUnchangedReadsPauseInsteadOfLoopingForever(t *testing.T) {
	store, r, _, _, _, _ := fixture(t)
	ctx := context.Background()
	_ = store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"})
	model := &stalledModel{}
	svc, e := producer.New(ctx, store, r, model)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(svc.Close)
	id, _ := svc.Create(ctx, "")
	_ = svc.Start(ctx, id, producer.ID(), fullBrief)
	c := waitDone(t, store, id)
	if c.Status != "failed" || model.n > producer.MaxNoProgressRounds+1 || !strings.Contains(string(c.Transcript), fullBrief) {
		t.Fatal("stall lost request or did not pause", c.Status, model.n)
	}
}
