package broadcast

import (
	"context"
	"time"

	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

func (m *Manager) reviewAudience(ctx context.Context, show schedule.Occurrence) {
	reviewer, ok := m.options.Links.(production.AudienceReviewer)
	if !ok || m.options.Audience == nil || show.ShowID == "" || !(show.DJ.HasKey || show.DJ.TextReady) {
		return
	}
	at := m.now()
	messages, err := m.options.Audience.ForShow(ctx, show.StationID, show.Key, audience.Pending, at, audience.MaxBatch)
	if err != nil {
		m.logger.Warn("Audience review queue unavailable", "station_id", show.StationID, "error", err)
		return
	}
	if len(messages) == 0 {
		return
	}
	decisions, err := reviewer.Review(ctx, show, production.AudienceItems(messages))
	if err != nil {
		if ctx.Err() == nil {
			m.logger.Warn("Audience AI review failed", "station_id", show.StationID, "error", err)
		}
		return
	}
	// Validate the complete batch even when the adapter is not OpenAI.
	allowed := map[string]bool{}
	for _, message := range messages {
		allowed[message.ID] = true
	}
	if len(decisions) != len(messages) {
		return
	}
	for _, d := range decisions {
		if !allowed[d.ID] {
			return
		}
		delete(allowed, d.ID)
	}
	if err := m.options.Audience.Review(ctx, show.StationID, decisions, m.now()); err != nil && ctx.Err() == nil {
		m.logger.Warn("Audience review results could not be saved", "station_id", show.StationID, "error", err)
	}
}
func (m *Manager) approvedAudience(ctx context.Context, show schedule.Occurrence, at time.Time) []audience.Message {
	if m.options.Audience == nil || show.ShowID == "" {
		return nil
	}
	messages, err := m.options.Audience.ForShow(ctx, show.StationID, show.Key, audience.Approved, at, 100)
	if err != nil {
		return nil
	}
	return messages
}
func requestTrack(messages []audience.Message, pool []content.AudioItem, remaining time.Duration) content.AudioItem {
	for _, message := range messages {
		if message.Kind != audience.Request {
			continue
		}
		for _, track := range pool {
			if track.ID == message.TrackID && track.Duration != nil && *track.Duration > 0 && (remaining == 0 || (remaining > 0 && *track.Duration <= remaining)) {
				return track
			}
		}
	}
	return content.AudioItem{}
}
func requestIDs(messages []audience.Message, track string) []string {
	var ids []string
	for _, message := range messages {
		if message.Kind == audience.Request && message.TrackID == track {
			ids = append(ids, message.ID)
			if len(ids) == audience.MaxBatch {
				break
			}
		}
	}
	return ids
}
func (m *Manager) audienceLink(ctx context.Context, req production.LinkRequest) ([]byte, []string, []string, error) {
	m.reviewAudience(ctx, req.Show)
	adapter, ok := m.options.Links.(production.AudienceLinkProducer)
	if !ok || req.Handover {
		audio, err := m.options.Links.Link(ctx, req)
		return audio, nil, nil, err
	}
	messages := m.approvedAudience(ctx, req.Show, m.now())
	var selected []audience.Message
	// A request for the exact queued recording takes priority, even without a
	// dedication. The DJ must introduce its requester before that track.
	for _, message := range messages {
		if message.Kind == audience.Request && message.TrackID == req.Next.ID {
			selected = append(selected, message)
			break
		}
	}
	if len(selected) == 0 {
		for _, message := range messages {
			if message.Kind == audience.Shoutout {
				selected = append(selected, message)
				if len(selected) == production.MaxLinkShoutouts {
					break
				}
			}
		}
	}
	req.Audience = production.AudienceItems(selected)
	audio, ids, err := adapter.AudienceLink(ctx, req)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(ids) > len(selected) {
		return nil, nil, nil, production.ErrUnavailable
	}
	var consumed []string
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, nil, nil, production.ErrUnavailable
		}
		seen[id] = true
		found := false
		for _, message := range selected {
			if message.ID == id {
				found = true
				if message.Kind == audience.Shoutout {
					consumed = append(consumed, id)
				}
			}
		}
		if !found {
			return nil, nil, nil, production.ErrUnavailable
		}
	}
	// Always progress the oldest offered item; additional shout-outs are optional.
	if len(selected) > 0 && !seen[selected[0].ID] {
		return nil, nil, nil, production.ErrUnavailable
	}
	// Request acknowledgements remain approved until the actual track plays.
	return audio, consumed, ids, nil
}
