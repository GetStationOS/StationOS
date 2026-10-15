package producer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/station"
)

type actionBatchModel struct {
	t                  *testing.T
	stationID          string
	size, calls        int
	oversize, repeated bool
}

func (m *actionBatchModel) Respond(_ context.Context, _, _, _ string, input []producer.Item, _ []producer.Tool) (producer.Response, error) {
	m.calls++
	if m.oversize && (m.calls == 1 || m.repeated) {
		out := producer.Response{Text: "Discarded oversized reply"}
		for i := 0; i < producer.MaxToolCalls+1; i++ {
			call := responseCall(i, "save_playlist", map[string]string{"station_id": m.stationID, "name": fmt.Sprintf("Discarded pool %02d", i)})
			out.Calls = append(out.Calls, call.Calls...)
			out.Items = append(out.Items, call.Items...)
		}
		return out, nil
	}
	batchTurn := 1
	if m.oversize {
		batchTurn = 2
	}
	if m.calls == batchTurn {
		out := producer.Response{}
		for i := 0; i < m.size; i++ {
			call := responseCall(i, "save_playlist", map[string]string{"station_id": m.stationID, "name": fmt.Sprintf("Programme pool %02d", i)})
			out.Calls = append(out.Calls, call.Calls...)
			out.Items = append(out.Items, call.Items...)
		}
		return out, nil
	}
	results := 0
	for _, raw := range input {
		var item struct {
			Type   string
			Output string
		}
		if json.Unmarshal(raw, &item) == nil && item.Type == "function_call_output" {
			var result producer.Result
			if json.Unmarshal([]byte(item.Output), &result) != nil || result.Error != "" {
				m.t.Errorf("batch result failed: %s", item.Output)
			}
			results++
		}
	}
	if results != m.size {
		m.t.Errorf("expected %d sequential outcomes, got %d", m.size, results)
	}
	return producer.Response{Text: "All programme pools are saved."}, nil
}

func TestAssistantExecutesAcceptedActionBatchesAndRecoversOversizedReplies(t *testing.T) {
	for _, tc := range []struct {
		name               string
		size               int
		oversize, repeated bool
		calls              int
	}{
		{"nine-valid-actions", 9, false, false, 2},
		{"maximum-valid-actions", producer.MaxToolCalls, false, false, 2},
		{"oversized-reply-recovered", producer.MaxToolCalls, true, false, 3},
		{"repeated-oversized-replies", producer.MaxToolCalls, true, true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, registry, _, pools, _, _ := fixture(t)
			owner := call(t, registry, "save_station", map[string]string{"name": "Batch Radio"}).Data.(station.Station)
			if err := store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"}); err != nil {
				t.Fatal(err)
			}
			model := &actionBatchModel{t: t, stationID: owner.ID, size: tc.size, oversize: tc.oversize, repeated: tc.repeated}
			svc, err := producer.New(ctx, store, registry, model)
			if err != nil {
				t.Fatal(err)
			}
			defer svc.Close()
			id, err := svc.Create(ctx, owner.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = svc.Start(ctx, id, producer.ID(), "Set up the programme music pools for my weekly station."); err != nil {
				t.Fatal(err)
			}
			conversation := waitDone(t, store, id)
			saved, err := pools.List(ctx, owner.ID)
			wantStatus, wantCount := "idle", tc.size
			if tc.repeated {
				wantStatus, wantCount = "failed", 0
			}
			if err != nil || len(saved) != wantCount || conversation.Status != wantStatus || model.calls != tc.calls {
				t.Fatal("batch execution or recovery failed", len(saved), err, conversation.Status, model.calls)
			}
			if strings.Contains(string(conversation.Transcript), "Discarded pool") || strings.Contains(string(conversation.Transcript), "Discarded oversized reply") {
				t.Fatal("oversized response was checkpointed")
			}
			for _, pool := range saved {
				if !strings.HasPrefix(pool.Name, "Programme pool ") {
					t.Fatal("discarded action executed", pool)
				}
			}
			outcomes := 0
			for _, message := range conversation.Messages {
				if strings.HasPrefix(message.Text, "Saved playlist:") {
					outcomes++
				}
			}
			if outcomes != wantCount {
				t.Fatal("action outcomes not recorded exactly once", outcomes, wantCount)
			}
		})
	}
}
