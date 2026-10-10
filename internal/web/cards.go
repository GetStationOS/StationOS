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
)

type cardTrack struct {
	Label, Empty, Duration, Artwork string
	Item                            content.AudioItem
}

func stationTrack(stationID, label, empty string, item content.AudioItem) cardTrack {
	t := cardTrack{Label: label, Empty: empty, Item: item, Duration: duration(item.Duration)}
	if item.ID != "" && item.Artwork != "" {
		hash := sha256.Sum256([]byte(item.Artwork))
		t.Artwork = "/stations/" + url.PathEscape(stationID) + "/artwork/" + url.PathEscape(item.ID) + "?v=" + hex.EncodeToString(hash[:8])
	}
	return t
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
	status := map[string]string{"Preparing": "Preparing DJ", "Ready": "DJ ready", "Music only": "Music only", "Speaking": "DJ speaking", "Unavailable": "DJ unavailable · music continues"}[state.PresenterStatus]
	if status == "Music only" && !state.Programme.DJ.HasKey {
		status = "Music only · no DJ key"
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
