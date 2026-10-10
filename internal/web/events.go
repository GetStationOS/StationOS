package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"stationos.local/stationos/internal/station"
	"time"
)

func (s *Server) stationEvents(w http.ResponseWriter, r *http.Request) {
	if s.d.Broadcast == nil {
		http.Error(w, "broadcast unavailable", 503)
		return
	}
	id := r.PathValue("id")
	if id != "" {
		if _, err := s.d.Stations.Get(r.Context(), id); err != nil {
			if errors.Is(err, station.ErrNotFound) {
				http.NotFound(w, r)
			} else {
				s.fail(w, err)
			}
			return
		}
	}
	select {
	case s.eventSlots <- struct{}{}:
		defer func() { <-s.eventSlots }()
	default:
		http.Error(w, "too many live views", 503)
		return
	}
	changes, stop := s.d.Broadcast.WatchChanges()
	defer stop()
	rc := http.NewResponseController(w)
	defer rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	write := func(data []byte) bool {
		if rc.SetWriteDeadline(time.Now().Add(10*time.Second)) != nil {
			return false
		}
		if _, err := w.Write(data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	var previous [32]byte
	update := func() bool {
		var html bytes.Buffer
		eventName := "stations"
		if id != "" {
			eventName = "player"
			if err := s.t.ExecuteTemplate(&html, "player-tracks", view{Broadcast: s.d.Broadcast.State(id)}); err != nil {
				return false
			}
		} else {
			v, err := s.baseView(r)
			if err != nil {
				return false
			}
			if err = s.t.ExecuteTemplate(&html, "station-cards", v); err != nil {
				return false
			}
		}
		version := sha256.Sum256(html.Bytes())
		if version == previous {
			return true
		}
		data, err := json.Marshal(html.String())
		if err != nil {
			return false
		}
		if !write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", eventName, data))) {
			return false
		}
		previous = version
		return true
	}
	// Keep the connection alive and reconcile clock/config changes made outside
	// this process. This sends no card event unless visible content changed.
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case _, open := <-changes:
			if !open {
				return
			}
			if !update() {
				return
			}
		case <-heartbeat.C:
			if !update() || !write([]byte(": keepalive\n\n")) {
				return
			}
		}
	}
}
