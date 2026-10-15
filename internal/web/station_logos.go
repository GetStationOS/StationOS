package web

import (
	"bytes"
	"errors"
	"net/http"
	"stationos.local/stationos/internal/station"
	"strings"
	"time"
)

func (s *Server) stationLogo(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.PathValue("image"), ".jpg")
	if id == r.PathValue("image") {
		http.NotFound(w, r)
		return
	}
	st, err := s.d.Stations.Get(r.Context(), id)
	if errors.Is(err, station.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if !st.Public() && !strings.HasPrefix(r.URL.Path, "/producer/station-logos/") && !s.adminAuthenticated(r) {
		http.NotFound(w, r)
		return
	}
	data, err := assets.ReadFile("static/station-logo.jpg")
	if st.LogoVersion != "" {
		reader, ok := s.d.Stations.(station.LogoReader)
		if !ok {
			http.NotFound(w, r)
			return
		}
		data, err = reader.Logo(r.Context(), id)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, no-cache")
	if !st.Public() || strings.HasPrefix(r.URL.Path, "/producer/") {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	http.ServeContent(w, r, "logo.jpg", time.Time{}, bytes.NewReader(data))
}
func (s *Server) stationFormError(w http.ResponseWriter, r *http.Request, st station.Station, err error) {
	if r.Header.Get("HX-Request") == "true" {
		http.Error(w, err.Error(), 422)
		return
	}
	v := stationPageView{pageView: s.baseView(r)}
	v.Station = st
	v.StationRepeatInput = r.PostForm.Get("repeat_minutes")
	v.StationRepeatSupplied = r.PostForm.Has("repeat_minutes")
	v.StationError = err.Error() + " Choose the logo again if you selected a file."
	v.Title = "Create station"
	v.Page = "/producer/stations/new"
	if st.ID != "" {
		v.Page = "/producer/stations/" + st.ID + "/settings"
		v.Section = "settings"
		v.Title = "Station settings"
	}
	name := "new-station-page"
	if st.ID != "" {
		name = "station-settings-page"
	}
	s.renderPage(w, r, http.StatusUnprocessableEntity, name, v)
}
