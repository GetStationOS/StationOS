package episodes

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"time"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

// planDecision offers only recordings that already pass the local timing,
// repeat and variety rules. The provider returns one ID, never a replacement queue.
func (s *Service) planDecision(ctx context.Context, o schedule.Occurrence, base Episode, pool []content.AudioItem, byID, eligible map[string]content.AudioItem) error {
	remaining := int64(o.End.Sub(planningStart(base, o, time.Now())).Seconds())
	format := "varied"
	if base.RepetitionReason != "" {
		format = "album"
	}
	ordered := append([]content.AudioItem(nil), pool...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	seed := fnv.New64a()
	fmt.Fprintf(seed, "%s:%d:%d", base.Key, len(base.Aired), len(base.Plan))
	offset := 0
	if len(ordered) > 0 {
		offset = int(seed.Sum64() % uint64(len(ordered)))
	}
	offered := map[string]bool{}
	candidates := []content.AudioItem{}
	for n := 0; n < len(ordered) && len(candidates) < 10; n++ {
		a := ordered[(offset+n)%len(ordered)]
		cmd := command{Tracks: []string{a.ID}, Theme: base.Theme, RepetitionReason: base.RepetitionReason}
		if _, err := validateNextTrack(cmd, base, eligible, map[string]bool{a.ID: true}, remaining, format); err != nil {
			continue
		}
		if offered[a.ID] {
			continue
		}
		offered[a.ID] = true
		candidates = append(candidates, a)
	}
	if len(candidates) == 0 {
		return ai.ErrNoEligibleTracks
	}
	current := byID[base.QueueCurrent.ID]
	if current.ID == "" && len(base.Aired) > 0 {
		current = byID[base.Aired[len(base.Aired)-1].ID]
	}
	id, err := s.Fallback.SelectTrack(ctx, production.SelectionRequest{Show: o, Current: current, Candidates: candidates, RemainingSeconds: remaining})
	if err != nil {
		return err
	}
	cmd := command{Tracks: []string{id}, Theme: base.Theme, RepetitionReason: base.RepetitionReason}
	if _, err = validateNextTrack(cmd, base, eligible, offered, remaining, format); err != nil {
		return err
	}
	return s.Store.Update(ctx, o, func(e *Episode) error {
		if e.Cancelled || e.EditorialRevision != base.EditorialRevision || e.AiredRevision != base.AiredRevision {
			return ErrStale
		}
		e.Plan = s.readyTracks(*e, byID)
		if len(e.Plan) >= 2 {
			return ErrStale
		}
		plan, err := validateNextTrack(cmd, *e, eligible, offered, remaining, format)
		if err != nil {
			return ErrStale
		}
		plan[0].Approved = true
		plan[0].SelectionSource = "dj"
		e.Plan = append(e.Plan, plan...)
		e.PlanStartsAt = queueStart(base, o, time.Now())
		e.Status = "ready"
		e.PlanningFailed = false
		e.FailureCode, e.Problem = "", ""
		e.RetryAt = time.Time{}
		return nil
	})
}
