package episodes

import (
	"context"
	"errors"
	"fmt"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
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

func TestDecisionShortlistKeepsRecencyOrderWhileSkippingIneligibleTracks(t *testing.T) {
	ctx := context.Background()
	o := futureEpisode()
	o.DJ.Model = "gpt-6-luna"
	store := &memoryStore{}
	store.Update(ctx, o, func(e *Episode) error { e.Excluded = []string{"excluded"}; return nil })
	pool := poolStub{recording("excluded", 40), recording("too-long", 900)}
	// The eligibility adapter supplies timestamp order, deliberately unlike ID order.
	for i := 0; i < 15; i++ {
		pool = append(pool, recording(fmt.Sprintf("track-%02d", 15-i), 40))
	}
	svc := &Service{Store: store, Pool: pool, Text: &decisionText{}, Eligibility: upcomingGate{pool: pool}}
	svc.Fallback = selectionFunc(func(_ context.Context, q production.SelectionRequest) (string, error) {
		if len(q.Candidates) != 10 {
			t.Fatal(len(q.Candidates))
		}
		for i, a := range q.Candidates {
			if a.ID != pool[i+2].ID {
				t.Fatal("lost oldest-first order or admitted an ineligible track", i, a.ID)
			}
		}
		return q.Candidates[0].ID, nil
	})
	if err := svc.Plan(ctx, o); err != nil {
		t.Fatal(err)
	}
}

type balancedGate struct {
	upcomingGate
	shortlist func([]content.AudioItem) ([]content.AudioItem, error)
}

func (g balancedGate) ShortlistTracks(_ context.Context, _ schedule.Occurrence, pool []content.AudioItem, _ string) ([]content.AudioItem, error) {
	return g.shortlist(pool)
}

func TestBalancingReceivesValidatedTracksAndConstrainsProviderChoice(t *testing.T) {
	ctx := context.Background()
	o := futureEpisode()
	o.DJ.Model = "gpt-6-luna"
	store := &memoryStore{}
	store.Update(ctx, o, func(e *Episode) error { e.Excluded = []string{"excluded"}; return nil })
	pool := poolStub{recording("excluded", 40), recording("too-long", 900)}
	for i := 0; i < 15; i++ {
		pool = append(pool, recording(fmt.Sprintf("eligible-%02d", i), 40))
	}
	balanced := false
	gate := balancedGate{upcomingGate: upcomingGate{pool: pool}, shortlist: func(eligible []content.AudioItem) ([]content.AudioItem, error) {
		balanced = true
		if len(eligible) != 15 {
			t.Fatal("balance must happen after local validation", len(eligible))
		}
		for _, a := range eligible {
			if a.ID == "excluded" || a.ID == "too-long" {
				t.Fatal("invalid balance input", a.ID)
			}
		}
		return eligible[12:15], nil
	}}
	svc := &Service{Store: store, Pool: pool, Text: &decisionText{}, Eligibility: gate}
	svc.Fallback = selectionFunc(func(_ context.Context, q production.SelectionRequest) (string, error) {
		if !balanced || len(q.Candidates) != 3 || q.Candidates[0].ID != "eligible-12" {
			t.Fatal("balanced pool ignored", q.Candidates)
		}
		return q.Candidates[0].ID, nil
	})
	if err := svc.Plan(ctx, o); err != nil {
		t.Fatal(err)
	}
}
