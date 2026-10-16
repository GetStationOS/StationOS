package broadcast

import (
	"context"
	"errors"
	"hash/fnv"
	"sort"
	"stationos.local/stationos/internal/production"
	"time"

	"stationos.local/stationos/internal/content"
)

// trackShortlist bounds AI work after station rotation has enforced eligibility.
// Prefer least recently played music, varying ties with the current recording.
func trackShortlist(pool []content.AudioItem, previous string, history Rotation, remaining time.Duration) []content.AudioItem {
	candidates := []content.AudioItem{}
	for _, item := range pool {
		if item.ID == previous && len(pool) > 1 {
			continue
		}
		candidates = append(candidates, item)
	}
	if remaining > 0 && remaining <= 20*time.Minute {
		fitting := []content.AudioItem{}
		for _, item := range candidates {
			if item.Duration != nil && *item.Duration > 0 && *item.Duration <= remaining {
				fitting = append(fitting, item)
			}
		}
		if len(fitting) > 0 {
			candidates = fitting
		} else if len(candidates) > 0 {
			// With no fitting track, keep the normal duration planner authoritative.
			return []content.AudioItem{selectTrack(candidates, previous, remaining)}
		}
	}
	rank := func(id string) uint64 {
		h := fnv.New64a()
		_, _ = h.Write([]byte(previous + "\x00" + id))
		return h.Sum64()
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := history.LastPlayed[candidates[i].ID], history.LastPlayed[candidates[j].ID]
		if !a.Equal(b) {
			return a.Before(b)
		}
		x, y := rank(candidates[i].ID), rank(candidates[j].ID)
		if x == y {
			return candidates[i].ID < candidates[j].ID
		}
		return x < y
	})
	return candidates[:min(10, len(candidates))]
}

// Listener requests have priority. Every AI choice must belong to the shortlist;
// provider failures leave the preselected normal-playback fallback intact.
func (m *Manager) selectNextTrack(ctx context.Context, request production.SelectionRequest, fallback content.AudioItem, requested bool) (content.AudioItem, error) {
	if requested || request.Show.DJ.ID == "" {
		return fallback, nil
	}
	id, err := m.options.Selector.SelectTrack(ctx, request)
	if err != nil {
		return fallback, err
	}
	for _, item := range request.Candidates {
		if item.ID == id {
			return item, nil
		}
	}
	return fallback, errors.New("DJ selected an unoffered track")
}
