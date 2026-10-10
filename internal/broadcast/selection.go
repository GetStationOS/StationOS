package broadcast

import (
	"math"
	"stationos.local/stationos/internal/content"
	"time"
)

// Curated choices remain separate from playlist membership. Near the handover,
// bounded duration planning picks a first track leading to the smallest overrun.
// Unknown lengths never masquerade as zero-duration filler.
func selectTrack(pool []content.AudioItem, previous string, curated []string, remaining time.Duration) content.AudioItem {
	ranked := []content.AudioItem{}
	used := map[string]bool{}
	byID := map[string]content.AudioItem{}
	for _, a := range pool {
		byID[a.ID] = a
	}
	for _, id := range curated {
		if a, ok := byID[id]; ok && !used[id] {
			ranked = append(ranked, a)
			used[id] = true
		}
	}
	for _, a := range pool {
		if !used[a.ID] {
			ranked = append(ranked, a)
		}
	}
	if remaining > 0 && remaining <= 20*time.Minute {
		target := int(math.Ceil(remaining.Seconds()))
		limit := target + 1200
		candidates := []content.AudioItem{}
		seenDuration := map[int]int{}
		for _, a := range ranked {
			if a.Duration == nil || *a.Duration <= 0 {
				continue
			}
			d := int(math.Round(a.Duration.Seconds()))
			if d <= 0 || d > limit || seenDuration[d] >= 2 {
				continue
			}
			seenDuration[d]++
			candidates = append(candidates, a)
			if len(candidates) == 256 {
				break
			}
		}
		type path struct {
			first, last string
			count       int
		}
		reachable := make([]path, limit+1)
		reachable[0] = path{last: previous, count: 1}
		for sum := 0; sum <= limit; sum++ {
			state := reachable[sum]
			if state.count == 0 || state.count > 12 {
				continue
			}
			if sum >= target && state.first != "" {
				return byID[state.first]
			}
			for _, a := range candidates {
				if a.ID == state.last && len(pool) > 1 {
					continue
				}
				next := sum + int(math.Round(a.Duration.Seconds()))
				if next > limit || reachable[next].count != 0 {
					continue
				}
				first := state.first
				if first == "" {
					first = a.ID
				}
				reachable[next] = path{first: first, last: a.ID, count: state.count + 1}
			}
		}
		// Very long/short or single-track pools may have no bounded plan.
		best := content.AudioItem{}
		overrun := time.Duration(1<<63 - 1)
		for _, a := range ranked {
			if a.Duration == nil || *a.Duration <= 0 || a.ID == previous && len(pool) > 1 {
				continue
			}
			delta := *a.Duration - remaining
			if delta < 0 {
				delta = -delta
			}
			if delta < overrun {
				best = a
				overrun = delta
			}
		}
		if best.ID != "" {
			return best
		}
	}
	for _, id := range curated {
		if a, ok := byID[id]; ok && (id != previous || len(pool) == 1) {
			return a
		}
	}
	return choose(pool, previous)
}
