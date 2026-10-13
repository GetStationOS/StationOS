package web

import (
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/station"
	"strings"
	"time"
)

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	if s.d.Broadcast == nil {
		http.Error(w, "broadcast unavailable", 503)
		return
	}
	var st station.Station
	var err error
	if lookup, ok := s.d.Stations.(station.StreamLookup); ok {
		st, err = lookup.ByStreamSlug(r.Context(), r.PathValue("id"))
	} else {
		var items []station.Station
		items, err = s.d.Stations.List(r.Context())
		for _, item := range items {
			if item.StreamSlug == r.PathValue("id") {
				st = item
				break
			}
		}
	}
	if err != nil && !errors.Is(err, station.ErrNotFound) {
		s.fail(w, err)
		return
	}
	if st.ID == "" {
		http.NotFound(w, r)
		return
	}
	if !st.Public() && !strings.HasPrefix(r.URL.Path, "/producer/streams/") && !s.adminAuthenticated(r) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("icy-name", cleanICY(st.Name))
	w.Header().Set("icy-br", "128")
	logoURL := strings.TrimSuffix(s.streamURL(r, st.StreamSlug), "/streams/"+st.StreamSlug) + st.LogoURL()
	w.Header().Set("icy-logo", logoURL)
	metadata := r.Header.Get("Icy-MetaData") == "1"
	if metadata {
		w.Header().Set("icy-metaint", "8192")
	}
	// A live programme has no byte-addressable representation. Ignore Range
	// and return the live 200 response, including Safari's bytes=0-1 probe.
	// Rejecting that probe makes Safari report otherwise valid MP3 as unplayable.
	if r.Method == http.MethodHead {
		return
	}
	// Only app-generated opaque IDs group requests; IP addresses and cookies
	// cannot distinguish multiple players on the same browser or home network.
	clientID := r.URL.Query().Get("listener")
	if clientID != "" {
		if decoded, err := hex.DecodeString(clientID); err != nil || len(decoded) != 16 {
			http.Error(w, "invalid listener session", http.StatusBadRequest)
			return
		}
	}
	sub, err := s.d.Broadcast.SubscribeStream(r.Context(), st.ID, clientID)
	if err != nil {
		s.d.Logger.Warn("stream admission failed", "station", st.ID, "error", err)
		http.Error(w, "station unavailable; please try again shortly", 503)
		return
	}
	defer sub.Close()
	timer := time.NewTimer(25 * time.Second)
	defer timer.Stop()
	var first []broadcast.StreamFrame
	select {
	case <-sub.Done:
		http.Error(w, "station could not play its music; please try again shortly", 503)
		return
	case <-r.Context().Done():
		return
	case <-timer.C:
		http.Error(w, "station could not start; please try again shortly", 503)
		return
	case data, ok := <-sub.Frames:
		if !ok {
			http.Error(w, "station could not play its music; please try again shortly", 503)
			return
		}
		first = data
	}
	rc := http.NewResponseController(w)
	defer rc.SetWriteDeadline(time.Time{})
	var output io.Writer = w
	icy := icyWriter{output: w, remaining: icyInterval, logo: logoURL}
	if metadata {
		output = &icy
	}
	write := func(frames []broadcast.StreamFrame) bool {
		select {
		case <-sub.Done:
			return false
		default:
		}
		if rc.SetWriteDeadline(time.Now().Add(10*time.Second)) != nil {
			return false
		}
		for _, frame := range frames {
			icy.title = icyTitle(frame, st.Name)
			if _, err := output.Write(frame.Audio); err != nil {
				return false
			}
		}
		return rc.Flush() == nil
	}
	// A bare URL's tiny media probe must not become another endless listener.
	// Keep the Safari-compatible 200 MP3 response, but finish after initial
	// audio. Session-bearing app probes are replaced by their playback request.
	probe := clientID == "" && strings.TrimSpace(r.Header.Get("Range")) == "bytes=0-1"
	if probe && len(first) > 1 {
		first = first[:1]
	}
	if !write(first) {
		return
	}
	if probe {
		return
	}
	for {
		select {
		case <-sub.Done:
			return
		case <-r.Context().Done():
			return
		case data, ok := <-sub.Frames:
			if !ok || !write(data) {
				return
			}
		}
	}
}
func (s *Server) assignBroadcast(w http.ResponseWriter, r *http.Request) {
	if s.d.BroadcastConfig == nil {
		http.Error(w, "broadcast unavailable", 503)
		return
	}
	id := r.PathValue("id")
	err := s.d.BroadcastConfig.Assign(r.Context(), id, r.PostForm.Get("playlist"))
	if errors.Is(err, station.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, playlists.ErrInvalid) {
		http.Error(w, "Choose a playlist belonging to this station.", 422)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if s.d.Broadcast != nil {
		s.d.Broadcast.Stop(id)
	}
	s.redirect(w, r, "/producer/stations/"+id+"/settings?notice=broadcast-saved")
}
func (s *Server) liveStation(w http.ResponseWriter, r *http.Request) {
	st, err := s.d.Stations.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, station.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	v := view{Station: st, Broadcast: broadcast.State{Status: "Dormant"}}
	if s.d.Broadcast != nil {
		v.Broadcast = s.d.Broadcast.State(st.ID)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.t.ExecuteTemplate(w, "station-live", v); err != nil {
		s.d.Logger.Error("status rendering failed", "error", err)
	}
}
func (s *Server) streamURL(r *http.Request, id string) string {
	base := s.d.PublicURL
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/streams/" + id
}

func (s *Server) liveCards(w http.ResponseWriter, r *http.Request) {
	v, err := s.baseView(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.t.ExecuteTemplate(w, "station-cards", v); err != nil {
		s.d.Logger.Error("cards rendering failed", "error", err)
	}
}
