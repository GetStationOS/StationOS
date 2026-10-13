package web

import (
	"errors"
	"net/http"
	"stationos.local/stationos/internal/station"
	"strings"
)

func (s *Server) stationPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.baseView(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.Station, err = s.d.Stations.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, station.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	v.StationID = v.Station.ID
	v.StreamURL = s.streamURL(r, v.Station.StreamSlug)
	if s.d.BroadcastConfig != nil {
		v.BroadcastPlaylist, err = s.d.BroadcastConfig.Assignment(r.Context(), v.Station.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	if s.d.Broadcast != nil {
		v.Broadcast = s.d.Broadcast.State(v.Station.ID)
	}
	v.Title = v.Station.Name
	v.Section = "overview"
	if strings.HasSuffix(r.URL.Path, "/settings") {
		v.Section = "settings"
		v.Title = "Station settings"
	}
	if strings.HasSuffix(r.URL.Path, "/delete") {
		v.Section = "delete"
		v.Title = "Delete station"
	}
	s.render(w, r, v)
}

func (s *Server) deleteStation(w http.ResponseWriter, r *http.Request) {
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "confirm station deletion", 400)
		return
	}
	err := s.d.Stations.Delete(r.Context(), r.PathValue("id"))
	if errors.Is(err, station.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if s.d.Broadcast != nil {
		s.d.Broadcast.Stop(r.PathValue("id"))
	}
	s.redirect(w, r, "/producer?notice=station-deleted")
}
