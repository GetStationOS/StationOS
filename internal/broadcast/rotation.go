package broadcast

import (
	"context"
	"stationos.local/stationos/internal/content"
	"time"
)

// Rotation is station-wide actual play history, independent of show and catalogue.
type Rotation struct {
	Interval    time.Duration
	LastTrackID string
	LastPlayed  map[string]time.Time
}
type History interface {
	Rotation(context.Context, string, time.Time) (Rotation, error)
	RecordPlay(context.Context, string, string, time.Time) error
}

func (m *Manager) rotation(ctx context.Context, id string, at time.Time) (Rotation, error) {
	if m.options.History == nil {
		return Rotation{}, nil
	}
	return m.options.History.Rotation(ctx, id, at)
}

// A small pool keeps broadcasting by relaxing only to the oldest alternative.
// The previous track stays excluded whenever another member is available.
func rotationPool(pool []content.AudioItem, history Rotation, at time.Time, previous string) ([]content.AudioItem, bool) {
	if history.Interval <= 0 {
		return pool, false
	}
	eligible := []content.AudioItem{}
	oldest := []content.AudioItem{}
	var oldestAt time.Time
	for _, item := range pool {
		if len(pool) > 1 && item.ID == previous {
			continue
		}
		played, exists := history.LastPlayed[item.ID]
		if !exists || !at.Before(played.Add(history.Interval)) {
			eligible = append(eligible, item)
			continue
		}
		if len(oldest) == 0 || played.Before(oldestAt) {
			oldest, oldestAt = []content.AudioItem{item}, played
		} else if played.Equal(oldestAt) {
			oldest = append(oldest, item)
		}
	}
	if len(eligible) > 0 {
		return eligible, false
	}
	return oldest, len(oldest) > 0
}
