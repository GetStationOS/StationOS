package web

import (
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
	operations, stopOperations := s.d.Changes.Watch("operations")
	defer stopOperations()
	var health <-chan struct{}
	if s.d.Recording != nil {
		var release func()
		health, release = s.d.Recording.WatchChanges()
		defer release()
	}
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
		var html []byte
		var err error
		eventName := "stations"
		if id != "" {
			eventName = "player"
			st, err := s.d.Stations.Get(r.Context(), id)
			if err != nil {
				return false
			}
			if html, err = s.executeHTML("player-tracks", playerView{Station: st, Broadcast: s.d.Broadcast.State(id)}); err != nil {
				return false
			}
		} else {
			v := stationDirectoryView{pageView: s.baseView(r)}
			err := s.loadStationCards(r, &v)
			if err != nil {
				return false
			}
			if html, err = s.executeHTML("station-cards", v); err != nil {
				return false
			}
		}
		version := sha256.Sum256(html)
		if version == previous {
			return true
		}
		data, err := json.Marshal(string(html))
		if err != nil {
			return false
		}
		if !write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", eventName, data))) {
			return false
		}
		previous = version
		return true
	}
	// Transport heartbeat only: committed configuration/runtime changes drive snapshots.
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
		case <-operations:
			if !update() {
				return
			}
		case <-health:
			if !update() {
				return
			}
		case <-heartbeat.C:
			if !write([]byte(": keepalive\n\n")) {
				return
			}
		}
	}
}
