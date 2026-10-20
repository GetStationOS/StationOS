package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"time"

	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

type cardTrack struct {
	Label, Empty, Duration, Artwork string
	Item                            content.AudioItem
}

func stationTrack(stationID, label, empty string, item content.AudioItem) cardTrack {
	t := cardTrack{Label: label, Empty: empty, Item: item, Duration: duration(item.Duration)}
	if item.ID != "" && item.Artwork != "" {
		hash := sha256.Sum256([]byte(item.Artwork))
		t.Artwork = "/producer/stations/" + url.PathEscape(stationID) + "/artwork/" + url.PathEscape(item.ID) + "?v=" + hex.EncodeToString(hash[:8])
	}
	return t
}

// Speech uses the portrait of the presenter actually producing this segment.
func currentBroadcastTrack(stationID string, state broadcast.State) cardTrack {
	if state.Phase == "chatter" || state.Phase == "handover" {
		t := cardTrack{Label: "DJ ON AIR", Empty: "Nothing playing", Item: state.Current}
		if state.Current.ID != "" {
			t.Artwork = state.Programme.DJ.PortraitURL
		}
		return t
	}
	return stationTrack(stationID, "NOW PLAYING", "Nothing playing", state.Current)
}

func programmeDetails(state broadcast.State, timezone string, now time.Time) (string, string) {
	if state.Programme.ShowID == "" || state.Current.ID == "" {
		return "", ""
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	times := state.Programme.Start.In(loc).Format("15:04") + "–" + state.Programme.End.In(loc).Format("15:04")
	status := presenterStatus(state)
	if status == "Music only" && !state.Programme.DJ.HasKey {
		status = "Music only · shared API key missing"
	}
	if !state.Programme.End.IsZero() && !now.Before(state.Programme.End) && state.Current.Kind == content.Track {
		status = "Finishing current track"
	}
	if state.Phase == "handover" {
		status = "Handing over"
	}
	return times, status
}
func (s *Server) stationArtwork(w http.ResponseWriter, r *http.Request) {
	// A short private cache avoids re-fetching unchanged artwork on status polls.
	w.Header().Set("Cache-Control", "private, max-age=60")
	if s.d.Broadcast == nil || s.d.ArtworkFactory == nil {
		http.NotFound(w, r)
		return
	}
	state := s.d.Broadcast.State(r.PathValue("id"))
	var item content.AudioItem
	for _, a := range []content.AudioItem{state.Current, state.Next} {
		if a.ID == r.PathValue("item") {
			item = a
			break
		}
	}
	if item.Artwork == "" || item.SourceID == "" {
		http.NotFound(w, r)
		return
	}
	s.serveItemArtwork(w, r, item)
}

func (s *Server) serveItemArtwork(w http.ResponseWriter, r *http.Request, item content.AudioItem) {
	if s.d.ArtworkFactory == nil || item.Artwork == "" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	select {
	case s.artworkSlots <- struct{}{}:
		defer func() { <-s.artworkSlots }()
	case <-ctx.Done():
		http.Error(w, "artwork unavailable", 503)
		return
	}
	config, err := s.d.Sources.Get(ctx, item.SourceID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	provider, err := s.d.ArtworkFactory(ctx, config)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	reader, ok := provider.(sources.ArtworkReader)
	if !ok || !provider.Profile().Capabilities.Artwork {
		http.NotFound(w, r)
		return
	}
	data, mime, err := reader.Artwork(ctx, item.Artwork)
	if err != nil || len(data) == 0 || len(data) > 4<<20 {
		http.NotFound(w, r)
		return
	}
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(data)
}

func producerLogo(st station.Station) string {
	if st.ID == "" {
		return st.LogoURL()
	}
	return "/producer" + st.LogoURL()
}

// Speech preparation and validated music curation have independent outcomes.
func presenterStatus(state broadcast.State) string {
	speech := map[string]string{"Preparing": "Preparing speech", "Ready": "Speech ready", "Music only": "Music only", "Speaking": "DJ speaking", "Unavailable": "Speech unavailable"}[state.PresenterStatus]
	curation := map[string]string{"Preparing": "Preparing curation", "Ready": "Curation ready", "Unavailable": "Curation unavailable · music continues"}[state.CurationStatus]
	if curation != "" {
		if speech != "" {
			return speech + " · " + curation
		}
		return curation
	}
	if state.PresenterStatus == "Unavailable" {
		return "Speech unavailable · music continues"
	}
	return speech
}

// Keep the two DJ capabilities independent; readiness of one never implies the other.
type djIndicator struct {
	Kind, State, Label string
}

func djIndicators(state broadcast.State) []djIndicator {
	if state.Programme.ShowID == "" || state.Current.ID == "" {
		return nil
	}
	var indicators []djIndicator
	speech := map[string]djIndicator{
		"Preparing":   {"speech", "preparing", "DJ speech is being prepared"},
		"Ready":       {"speech", "ready", "DJ speech is ready"},
		"Speaking":    {"speech", "speaking", "DJ is speaking"},
		"Unavailable": {"speech", "unavailable", "DJ speech is unavailable; music continues"},
		"Music only":  {"speech", "unavailable", "DJ speech is off; music continues"},
	}
	selection := map[string]djIndicator{
		"Preparing":   {"selection", "preparing", "DJ music selection is being prepared"},
		"Ready":       {"selection", "ready", "DJ music selection is ready"},
		"Unavailable": {"selection", "unavailable", "DJ music selection is unavailable; music continues"},
	}
	if indicator, ok := speech[state.PresenterStatus]; ok {
		indicators = append(indicators, indicator)
	}
	if indicator, ok := selection[state.CurationStatus]; ok {
		indicators = append(indicators, indicator)
	}
	return indicators
}
