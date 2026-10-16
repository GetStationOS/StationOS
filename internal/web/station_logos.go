package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"stationos.local/stationos/internal/artwork"
	"stationos.local/stationos/internal/producer"
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
	v.StationLogoGeneration = s.logoGenerationAvailable(r.Context())
	if value := r.PostForm.Get("generated_logo"); value != "" {
		if data, e := decodeLogoDraft(value); e == nil {
			v.StationLogoDraft = base64.StdEncoding.EncodeToString(data)
		}
	}
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

func (s *Server) logoGenerationAvailable(ctx context.Context) bool {
	if s.d.Logos == nil {
		return false
	}
	available, err := s.d.Logos.Available(ctx)
	if err != nil {
		s.d.Logger.Warn("logo generation configuration unavailable", "error", err)
	}
	return err == nil && available
}

func decodeLogoDraft(value string) ([]byte, error) {
	if len(value) > 7<<20 {
		return nil, errors.New("Generated logo is too large")
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.New("Generated logo could not be read; generate it again")
	}
	return artwork.Normalize(data)
}

// Generation prepares a draft image. Only the ordinary Save action persists it.
func (s *Server) generateStationLogo(w http.ResponseWriter, r *http.Request, st station.Station) {
	if !s.logoGenerationAvailable(r.Context()) {
		s.stationFormError(w, r, st, errors.New("Logo generation is unavailable. Configure artwork in Assistant settings"))
		return
	}
	select {
	case s.logoSlots <- struct{}{}:
		defer func() { <-s.logoSlots }()
	default:
		s.stationFormError(w, r, st, errors.New("A logo is already being generated. Wait for it to finish"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	image, err := s.d.Logos.Generate(ctx, producer.ImageRequest{Kind: "station", Name: st.Name, Description: st.Description})
	var data []byte
	if err == nil {
		data, err = artwork.Normalize(image.Data)
	}
	if err != nil {
		s.stationFormError(w, r, st, err)
		return
	}
	v := stationPageView{pageView: s.baseView(r)}
	v.Station = st
	v.StationLogoGeneration = true
	v.StationLogoDraft = base64.StdEncoding.EncodeToString(data)
	v.StationRepeatInput = r.PostForm.Get("repeat_minutes")
	v.StationRepeatSupplied = r.PostForm.Has("repeat_minutes")
	if fragmentTarget(r, "station-logo-editor") {
		s.renderHTML(w, http.StatusOK, "station-logo-field", v)
		return
	}
	name := "new-station-page"
	v.Title = "Create a station"
	if st.ID != "" {
		name = "station-settings-page"
		v.StationID = st.ID
		v.Section = "settings"
		v.Title = "Station settings"
	}
	s.renderPage(w, r, http.StatusOK, name, v)
}
