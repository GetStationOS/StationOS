package broadcast

import (
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/schedule"
)

// Keep diagnostics beside the playout checks without relaxing their safeguards.
func linkPlaybackRejection(link linkResult, show schedule.Occurrence, next content.AudioItem, musicErr error, audienceUsable bool) string {
	switch {
	case musicErr != nil:
		return "The next recording could not be opened"
	case !audienceUsable:
		return "The audience contribution was no longer available for broadcast"
	case link.request.Handover:
		return "The predicted programme handover did not occur at this track boundary"
	case link.request.Show.Key != show.Key:
		return "The programme changed after speech preparation"
	case link.request.Next.ID != next.ID:
		return "The next recording changed after speech preparation"
	default:
		return ""
	}
}

func (m *Manager) skippedSpeech(id string, link linkResult, show schedule.Occurrence, current content.AudioItem, reason string) {
	m.logger.Warn("Prepared DJ speech skipped", "station_id", id, "reason", reason,
		"prepared_show_key", link.request.Show.Key, "show_key", show.Key,
		"prepared_current_track_id", link.request.Current.ID, "prepared_next_track_id", link.request.Next.ID,
		"actual_track_id", current.ID, "handover", link.request.Handover,
		"speech_provider", link.provenance.Provider, "speech_model", link.provenance.Model)
	m.observe(id, "ai", "Prepared DJ speech skipped: "+reason+"; music continues", State{Programme: show}, "speech", false)
}
