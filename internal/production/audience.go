package production

import (
	"context"
	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/schedule"
)

// MaxLinkShoutouts bounds a brief listener section independently of review batches.
const MaxLinkShoutouts = 3

// AudienceItem exposes only consented editorial data to the presenter adapter.
type AudienceItem struct {
	ID, Name, Text, TrackID, TrackTitle, TrackArtist string
	Kind                                             audience.Kind
}

func AudienceItems(messages []audience.Message) []AudienceItem {
	items := make([]AudienceItem, 0, len(messages))
	for _, m := range messages {
		items = append(items, AudienceItem{m.ID, m.Name, m.Text, m.TrackID, m.TrackTitle, m.TrackArtist, m.Kind})
	}
	return items
}

type AudienceReviewer interface {
	Review(context.Context, schedule.Occurrence, []AudienceItem) ([]audience.Decision, error)
}
type AudienceLinkProducer interface {
	AudienceLink(context.Context, LinkRequest) ([]byte, []string, error)
}
