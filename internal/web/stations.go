package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"stationos.local/stationos/internal/station"
	"strings"
)

func (s *Server) stationPage(w http.ResponseWriter, r *http.Request) {
	v := stationPageView{pageView: s.baseView(r)}
	var err error
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
	if v.Section != "settings" {
		v.Operations = s.operationalSummary(r, v.Station.ID)
	}
	v.StreamURL = s.streamURL(r, v.Station.StreamSlug)
	v.Title = v.Station.Name
	v.Section = "overview"
	if strings.HasSuffix(r.URL.Path, "/settings") {
		v.Section = "settings"
		v.Title = "Station settings"
		v.StationLogoGeneration = s.imageGenerationAvailable(r.Context())
		if err := s.loadStationSourceOptions(r.Context(), &v); err != nil {
			s.fail(w, err)
			return
		}
	}
	if strings.HasSuffix(r.URL.Path, "/delete") {
		v.Section = "delete"
		v.Title = "Delete station"
	}
	if v.Section == "overview" && s.d.Broadcast != nil {
		v.Broadcast = s.d.Broadcast.State(v.Station.ID)
	}
	if (v.Section == "overview" || v.Section == "delete") && s.d.Playlists != nil {
		pools, err := s.d.Playlists.List(r.Context(), v.Station.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		v.StationPlaylistCount = len(pools)
	}
	name := "station-overview-page"
	switch v.Section {
	case "settings":
		name = "station-settings-page"
	case "delete":
		if fragmentTarget(r, "dialog-body") {
			s.renderHTML(w, http.StatusOK, "delete-station", v)
			return
		}
		name = "delete-station-page"
	}
	s.renderPage(w, r, http.StatusOK, name, v)
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

// Preserve old bookmarks after consolidating upcoming audio into Overview.
func (s *Server) upcomingOverview(w http.ResponseWriter, r *http.Request) {
	_, err := s.d.Stations.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, station.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/producer/stations/"+url.PathEscape(r.PathValue("id"))+"#upcoming", http.StatusFound)
}

func (s *Server) loadStationSourceOptions(ctx context.Context, v *stationPageView) error {
	if s.d.Sources == nil {
		return nil
	}
	all, err := s.d.Sources.List(ctx)
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, id := range v.Station.SourceIDs {
		selected[id] = true
	}
	for _, source := range all {
		u, _ := url.Parse(source.Endpoint)
		v.SourceOptions = append(v.SourceOptions, stationSourceOption{ID: source.ID, Name: source.Name, Host: u.Host, Selected: v.Station.SourceIDs == nil || selected[source.ID]})
	}
	return nil
}
