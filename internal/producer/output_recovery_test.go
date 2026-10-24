package producer_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stationos.local/stationos/internal/producer"
)

type incompleteModel struct {
	calls int
	err   error
}

func (m *incompleteModel) Respond(_ context.Context, _, _, _ string, _ []producer.Item, _ []producer.Tool) (producer.Response, error) {
	m.calls++
	// Even apparently complete calls from an incomplete response must never run.
	return producer.Response{Text: "Private incomplete answer", Usage: producer.Usage{Input: 100, Output: 200}, Calls: []producer.Call{{ID: "partial", Name: "save_station", Arguments: []byte(`{"name":"Must not execute"}`)}}}, m.err
}

func TestIncompleteRecoveryIsBoundedAndDoesNotExecutePartialActions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		calls   int
	}{
		{"completed-output-limit", producer.ErrOutputLimit, 3},
		{"completed-empty-response", producer.ErrEmptyResponse, 2},
		{"ambiguous-network-failure", errors.New("Provider response failed; request outcome is uncertain and was not retried"), 1},
		{"request-deadline", context.DeadlineExceeded, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, registry, stations, _, _, _ := fixture(t)
			if err := store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"}); err != nil {
				t.Fatal(err)
			}
			model := &incompleteModel{err: tc.failure}
			svc, err := producer.New(ctx, store, registry, model)
			if err != nil {
				t.Fatal(err)
			}
			defer svc.Close()
			id, err := svc.Create(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			if err = svc.Start(ctx, id, producer.ID(), "Build a complete weekly station around my music library."); err != nil {
				t.Fatal(err)
			}
			conversation := waitDone(t, store, id)
			all, err := stations.List(ctx)
			if err != nil || len(all) != 0 || model.calls != tc.calls || conversation.Status != "failed" {
				t.Fatal("unsafe or unbounded recovery", len(all), err, model.calls, conversation.Status)
			}
			if len(conversation.Requests) != 1 || !strings.Contains(conversation.Requests[0], "complete weekly station") {
				t.Fatal("original brief lost")
			}
			if strings.Contains(string(conversation.Transcript), "Private incomplete answer") || strings.Contains(string(conversation.Transcript), "Must not execute") {
				t.Fatal("partial response was checkpointed")
			}
			for _, message := range conversation.Messages {
				if strings.Contains(message.Text, "Private incomplete answer") {
					t.Fatal("private partial answer published")
				}
			}
			usage := false
			expected := fmt.Sprintf("%d input tokens (including cached input), %d output tokens", 100*tc.calls, 200*tc.calls)
			for _, message := range conversation.Messages {
				if strings.Contains(message.Text, expected) {
					usage = true
				}
			}
			if !usage {
				t.Fatal("incomplete response usage was lost", conversation.Messages)
			}
		})
	}
}

type emptyContinuationModel struct {
	t       *testing.T
	step    int
	station string
}

func (m *emptyContinuationModel) Respond(_ context.Context, _, _, _ string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	step := m.step
	m.step++
	switch step {
	case 0:
		return responseCall(step, "save_station", map[string]string{"name": "Before Empty"}), nil
	case 1:
		m.station = resultID(input)
		return producer.Response{}, producer.ErrEmptyResponse
	case 2:
		offered := map[string]bool{}
		for _, tool := range tools {
			offered[tool.Name] = true
		}
		if !offered["inspect_station"] || !offered["save_station"] {
			m.t.Fatal("empty response did not narrow tools", offered)
		}
		raw, _ := json.Marshal(input)
		if !strings.Contains(string(raw), "Empty response recovery") || !strings.Contains(string(raw), m.station) {
			m.t.Fatal("lost recovery contract or committed ID")
		}
		return responseCall(step, "select_work_step", map[string]string{"area": "station", "station_id": m.station}), nil
	case 3:
		return responseCall(step, "save_task_progress", producer.Task{Brief: "Create Before Empty", Completed: []string{"Verified the saved station; no repeated creation"}}), nil
	}
	return producer.Response{Text: "Verified the saved station."}, nil
}
func TestCompletedEmptyReplyContinuesWithoutRecreatingCommittedStation(t *testing.T) {
	ctx := context.Background()
	store, registry, stations, _, _, _ := fixture(t)
	if err := store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"}); err != nil {
		t.Fatal(err)
	}
	model := &emptyContinuationModel{t: t}
	svc, err := producer.New(ctx, store, registry, model)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	id, err := svc.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Start(ctx, id, producer.ID(), "Create a station named Before Empty."); err != nil {
		t.Fatal(err)
	}
	conversation := waitDone(t, store, id)
	saved, err := stations.List(ctx)
	if err != nil || len(saved) != 1 || saved[0].ID != model.station || conversation.Status != "idle" || model.step != 5 {
		t.Fatal("lost or repeated committed work", saved, err, conversation.Status, model.step)
	}
}
