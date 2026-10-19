package broadcast

import (
	"sort"
	"stationos.local/stationos/internal/content"
)

// Apply after exact-recording rotation and before either AI or local selection.
// Specialist pools relax preferences oldest first, never exact-repeat policy.
func varietyPool(pool, catalogue []content.AudioItem, history Rotation, previous string, current content.AudioItem) []content.AudioItem {
	return varietyPoolFormat(pool, catalogue, history, previous, current, "varied")
}
func varietyPoolFormat(pool, catalogue []content.AudioItem, history Rotation, previous string, current content.AudioItem, format string) []content.AudioItem {
	byID := map[string]content.AudioItem{}
	for _, a := range catalogue {
		byID[a.ID] = a
	}
	if current.ID != "" {
		byID[current.ID] = current
	}
	recent := []content.AudioItem{}
	for id := range history.LastPlayed {
		if a, ok := byID[id]; ok && id != previous {
			recent = append(recent, a)
		}
	}
	sort.Slice(recent, func(i, j int) bool {
		a, b := history.LastPlayed[recent[i].ID], history.LastPlayed[recent[j].ID]
		if a.Equal(b) {
			return recent[i].ID < recent[j].ID
		}
		return a.After(b)
	})
	if len(recent) > 4 {
		recent = recent[:4]
	}
	if a, ok := byID[previous]; ok {
		recent = append([]content.AudioItem{a}, recent...)
	}
	for len(recent) > 0 {
		eligible := []content.AudioItem{}
		for _, a := range pool {
			conflict := false
			for _, b := range recent {
				if content.MusicConflict(a, b, format) {
					conflict = true
					break
				}
			}
			if !conflict {
				eligible = append(eligible, a)
			}
		}
		if len(eligible) > 0 {
			return eligible
		}
		recent = recent[:len(recent)-1]
	}
	return pool
}
func samePerformerOrAlbum(a, b content.AudioItem) bool { return content.MusicConflict(a, b, "varied") }
