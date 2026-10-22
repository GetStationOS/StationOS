package broadcast

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
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
	return balancedShortlist(candidates, history, previous)
}

// rankByLastPlayed puts never-played recordings first, then oldest actual plays.
// A seeded tie-break varies equal timestamps without changing recency priority.
func rankByLastPlayed(candidates []content.AudioItem, history Rotation, seed string) {
	rank := func(id string) uint64 {
		h := fnv.New64a()
		_, _ = h.Write([]byte(seed + "\x00" + id))
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

func (m *Manager) selectionCandidates(pool []content.AudioItem, previous string, history Rotation, remaining time.Duration) []content.AudioItem {
	if selector, ok := m.options.Selector.(interface{ FullPlaylist() bool }); ok && selector.FullPlaylist() {
		return pool
	}
	return trackShortlist(pool, previous, history, remaining)
}

func (m *Manager) editorialPool(ctx context.Context, o schedule.Occurrence, pool []content.AudioItem) ([]content.AudioItem, error) {
	if selector, ok := m.options.Selector.(interface {
		FilterCandidates(context.Context, schedule.Occurrence, []content.AudioItem) ([]content.AudioItem, error)
	}); ok {
		return selector.FilterCandidates(ctx, o, pool)
	}
	return pool, nil
}

func (m *Manager) episodeMusicPolicy(ctx context.Context, o schedule.Occurrence) string {
	if selector, ok := m.options.Selector.(interface {
		RepetitionAllowed(context.Context, schedule.Occurrence) bool
	}); ok && selector.RepetitionAllowed(ctx, o) {
		return "album"
	}
	return "varied"
}

// EligibleTracks applies the same station rotation and variety rules used at playback,
// projected past the ready queue so background preparation cannot bypass those rules.
func (m *Manager) EligibleTracks(ctx context.Context, o schedule.Occurrence, at time.Time, prior []production.SelectionPlay) ([]content.AudioItem, error) {
	pool, err := m.repo.Pool(ctx, o.PlaylistID)
	if err != nil {
		return nil, err
	}
	pool, err = m.editorialPool(ctx, o, pool)
	if err != nil {
		return nil, err
	}
	rotation, err := m.rotation(ctx, o.StationID, at)
	if err != nil {
		return nil, err
	}
	copied := map[string]time.Time{}
	for id, played := range rotation.LastPlayed {
		copied[id] = played
	}
	rotation.LastPlayed = copied
	catalogue := append([]content.AudioItem(nil), pool...)
	previous := rotation.LastTrackID
	current := content.AudioItem{}
	for _, played := range prior {
		if played.Item.ID == "" {
			continue
		}
		if played.Started.After(rotation.LastPlayed[played.Item.ID]) {
			rotation.LastPlayed[played.Item.ID] = played.Started
		}
		previous = played.Item.ID
		current = played.Item
		catalogue = append(catalogue, played.Item)
	}
	eligible, _ := rotationPool(pool, rotation, at, previous)
	eligible = varietyPoolFormat(eligible, catalogue, rotation, previous, current, m.episodeMusicPolicy(ctx, o))
	// Repeated preparation sees the same order; progress through the episode
	// varies ties without letting recently played recordings leapfrog older ones.
	seed := fmt.Sprintf("%s:%s:%d", o.Key, previous, len(prior))
	rankByLastPlayed(eligible, rotation, seed)
	return eligible, nil
}

// Background acceptance owns soft rotation checks. Do not reject an approved queue
// again at airtime; retain hard membership/source checks against the current pool.
func (m *Manager) withApproved(ctx context.Context, o schedule.Occurrence, pool, candidates []content.AudioItem, current string) ([]content.AudioItem, error) {
	selector, ok := m.options.Selector.(interface {
		ApprovedTracks(context.Context, schedule.Occurrence) ([]string, error)
	})
	if !ok {
		return candidates, nil
	}
	ids, err := selector.ApprovedTracks(ctx, o)
	if err != nil {
		return nil, err
	}
	approved := map[string]bool{}
	for _, id := range ids {
		approved[id] = true
	}
	seen := map[string]bool{}
	for _, a := range candidates {
		seen[a.ID] = true
	}
	out := append([]content.AudioItem(nil), candidates...)
	for _, a := range pool {
		if approved[a.ID] && !seen[a.ID] && a.ID != current {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *Manager) CanPrepareEpisode(o schedule.Occurrence) bool {
	state := m.State(o.StationID)
	return state.Listeners > 0 && state.Status == "Broadcasting" && schedule.SameEpisode(state.Following, o)
}
