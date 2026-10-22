package episodes

import (
	"context"
	"errors"
	"stationos.local/stationos/internal/production"
	"testing"
)

type decisionText struct{ textStub }

func (*decisionText) Kind(context.Context, string) (string, error) { return "openai", nil }

type selectionFunc func(context.Context, production.SelectionRequest) (string, error)

func (f selectionFunc) SelectTrack(ctx context.Context, q production.SelectionRequest) (string, error) {
	return f(ctx, q)
}

func TestScheduledDecisionPreservesQueueAndRejectsStaleOrInvalidChoice(t *testing.T) {
	for _, mode := range []string{"valid", "unoffered", "edit", "aired", "failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			o := futureEpisode()
			o.DJ.Model = "gpt-6-luna"
			store := &memoryStore{}
			pool := poolStub{recording("aired", 40), recording("queued", 40), recording("excluded", 40)}
			for i := 0; i < 20; i++ {
				pool = append(pool, recording(string(rune('a'+i)), 40))
			}
			store.Update(ctx, o, func(e *Episode) error {
				e.Aired = []Track{Snapshot(pool[0])}
				e.Plan = []Track{Snapshot(pool[1])}
				e.Excluded = []string{"excluded"}
				return nil
			})
			text := &decisionText{}
			svc := &Service{Store: store, Pool: pool, Text: text}
			calls := 0
			svc.Fallback = selectionFunc(func(_ context.Context, q production.SelectionRequest) (string, error) {
				calls++
				if len(q.Candidates) != 10 {
					t.Fatal("unbounded shortlist", len(q.Candidates))
				}
				for _, a := range q.Candidates {
					if a.ID == "aired" || a.ID == "queued" || a.ID == "excluded" {
						t.Fatal("blocked candidate", a.ID)
					}
				}
				switch mode {
				case "unoffered":
					return "invented", nil
				case "edit":
					store.Update(ctx, o, func(e *Episode) error { e.EditorialRevision++; return nil })
				case "aired":
					store.Update(ctx, o, func(e *Episode) error { e.AiredRevision++; return nil })
				case "failure":
					return "", errors.New("provider unavailable")
				}
				return q.Candidates[0].ID, nil
			})
			err := svc.Plan(ctx, o)
			e, _ := store.Get(ctx, o.Key)
			if calls != 1 || text.calls != 0 || e.Plan[0].ID != "queued" {
				t.Fatal(calls, text.calls, e)
			}
			if mode == "valid" {
				if err != nil || len(e.Plan) != 2 || !e.Plan[1].Approved || e.Plan[1].SelectionSource != "dj" {
					t.Fatal(err, e)
				}
			} else if err == nil || len(e.Plan) != 1 {
				t.Fatal("accepted failed/stale choice", err, e)
			}
		})
	}
}
