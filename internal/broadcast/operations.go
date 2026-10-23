package broadcast

import (
	"errors"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

func transmissionEvent(id string, s State) operations.Event {
	kind := "music"
	reason := "Selected from the eligible playlist; selection method was not recorded"
	if s.Current.Kind == content.PresenterLink {
		kind = "speech"
		reason = "Prepared presenter speech"
	}
	return operations.Event{ID: operations.ID(), StationID: id, Kind: kind, WritingProvider: s.WritingProvenance.Provider, WritingConnection: s.WritingProvenance.Connection, WritingModel: s.WritingProvenance.Model, Speech: s.SpeechText, SpeechProvider: s.SpeechProvenance.Provider, SpeechProviderName: s.SpeechProvenance.ProviderName, SpeechConnection: s.SpeechProvenance.Connection, SpeechModel: s.SpeechProvenance.Model, SpeechVoice: s.SpeechProvenance.Voice, SpeechVoiceName: s.SpeechProvenance.VoiceName, Title: s.Current.Title, Artist: s.Current.Artist, Composer: s.Current.Composer, Album: s.Current.Album, TrackID: s.Current.ID, SourceID: s.Current.SourceID, SourceItemID: s.Current.SourceItemID, ShowID: s.Programme.ShowID, Show: s.Programme.Title, DJID: s.Programme.DJ.ID, DJ: s.Programme.DJ.Name, PlaylistID: s.Programme.PlaylistID, Playlist: s.Programme.PlaylistName, Phase: s.Phase, Reason: reason}
}
func (m *Manager) record(e operations.Event) {
	if m.options.Operations != nil {
		m.options.Operations.Record(e)
	}
}
func (m *Manager) observe(id, kind, message string, s State, code string, recover bool) {
	if m.options.Operations == nil {
		return
	}
	severity, effect, action := "warning", "Music continues", "Review the affected component and related transmission history."
	if code == "" && (kind == "handover" || kind == "started" || kind == "stopped" || kind == "intervention") {
		severity = "info"
		effect = ""
		action = ""
	}
	if code == "output" {
		severity = "critical"
		effect = "Station unavailable"
		action = "Check the default playlist, source availability and database."
	}
	if code == "audio" {
		effect = "Audio interrupted"
		action = "Check the track source and audio format."
	}
	if code == "supply" {
		effect = "Audio supply delayed; listener impact unknown"
		action = "Check source response times and encoder host load."
	}
	if code == "listener" {
		effect = "One listener disconnected"
		action = "Check the listener connection and proxy buffering."
	}
	e := transmissionEvent(id, s)
	e.Kind = kind
	e.Title = ""
	e.Started = m.now()
	e.Ended = e.Started
	e.Outcome = "observed"
	e.Reason = message
	e.IncidentCode = code
	e.Recover = recover
	e.Severity = severity
	e.Effect = effect
	e.Action = action
	m.record(e)
}

// Selection observations describe applied queue outcomes, never inferred intent or airtime.
func (m *Manager) recordSelection(id string, show schedule.Occurrence, item content.AudioItem, requested bool, err error) {
	if item.ID == "" {
		return
	}
	event := transmissionEvent(id, State{Programme: show, Current: item})
	event.Kind = "selection"
	label := "DJ selected"
	reason := "The DJ choice passed playlist and timing checks and was queued next."
	switch {
	case requested:
		label = "Listener request queued"
		reason = "An approved listener request took priority over DJ selection."
	case show.DJ.ID == "":
		label = "Automatic selection"
		reason = "Queued from the eligible playlist; no DJ is assigned."
	case errors.Is(err, production.ErrPlanPending):
		label = "Automatic selection"
		reason = "The DJ choice was not ready; automatic music selection was queued to keep the show playing."
		var pending *production.PendingSelection
		if errors.As(err, &pending) {
			reason = pending.Reason + " Automatic music selection was queued."
			if !pending.RetryAt.IsZero() {
				reason += " Next DJ retry: " + pending.RetryAt.In(show.Start.Location()).Format("15:04 MST") + "."
			}
		}
	case err != nil:
		label = "Automatic selection"
		reason = "The DJ choice was unavailable or could not be used; automatic music selection was queued."
	}
	event.Title = label + " · " + item.Title
	event.Reason = reason
	event.Started = m.now()
	event.Ended = event.Started
	event.Outcome = "observed"
	m.record(event)
}
