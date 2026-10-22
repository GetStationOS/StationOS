package broadcast

import (
	"context"
	"time"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/schedule"
)

// ShortlistTracks receives recordings that have already passed episode validation.
func (m *Manager) ShortlistTracks(ctx context.Context, o schedule.Occurrence, pool []content.AudioItem, seed string) ([]content.AudioItem, error) {
	history, err := m.rotation(ctx, o.StationID, time.Now())
	if err != nil {
		return nil, err
	}
	return balancedShortlist(pool, history, seed), nil
}

// Reserve at most three places for new music. Prefer distinct albums and
// performers, relaxing performer then album spacing only to fill a small pool.
func balancedShortlist(pool []content.AudioItem, history Rotation, seed string) []content.AudioItem {
	ordered := append([]content.AudioItem(nil), pool...)
	rankByLastPlayed(ordered, history, seed)
	fresh, played := []content.AudioItem{}, []content.AudioItem{}
	for _, a := range ordered {
		if history.LastPlayed[a.ID].IsZero() {
			fresh = append(fresh, a)
		} else {
			played = append(played, a)
		}
	}
	result := []content.AudioItem{}
	seen := map[string]bool{}
	newCount := 0
	add := func(items []content.AudioItem, isNew bool, relax int) {
		for _, a := range items {
			if len(result) == 10 || (isNew && newCount == 3) {
				return
			}
			if seen[a.ID] {
				continue
			}
			conflict := false
			for _, b := range result {
				if relax < 2 && content.MusicConflict(a, b, "artist") {
					conflict = true
					break
				}
				if relax == 0 {
					x, y := a, b
					x.Album = ""
					y.Album = ""
					if content.MusicConflict(x, y, "varied") {
						conflict = true
						break
					}
				}
			}
			if conflict {
				continue
			}
			seen[a.ID] = true
			result = append(result, a)
			if isNew {
				newCount++
			}
		}
	}
	for relax := 0; relax < 3 && len(result) < 10; relax++ {
		add(fresh, true, relax)
		add(played, false, relax)
	}
	return result
}
