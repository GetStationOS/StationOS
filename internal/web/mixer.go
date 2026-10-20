package web

import (
	"errors"
	"net/http"
	"stationos.local/stationos/internal/mixer"
	"stationos.local/stationos/internal/station"
	"strconv"
)

// Channel presentation stays independent of the currently supported audio kinds.
// Add future effect, advert and jingle channels through this shared strip.
type mixerChannel struct {
	Key, Number, Name, Description, Field, Volume string
}

func populateMixerChannels(v *mixerPageView) {
	v.MixerChannels = []mixerChannel{
		{"music", "01", "Music", "Original music level", "music_volume", v.MusicVolume},
		{"dj", "02", "DJ", "Levelled presenter voice", "dj_volume", v.DJVolume},
	}
}

func (s *Server) mixerView(r *http.Request) (mixerPageView, error) {
	v := mixerPageView{pageView: s.baseView(r)}
	var err error
	v.Station, err = s.d.Stations.Get(r.Context(), r.PathValue("id"))
	v.StationID = v.Station.ID
	v.Section = "mixer"
	v.Title = "Mixer"
	if r.URL.Query().Get("notice") == "mixer-saved" {
		v.Notice = "Mixer saved."
	}
	if err == nil {
		levels, e := s.d.Mixer.Get(r.Context(), v.Station.ID)
		err = e
		v.MusicVolume = strconv.Itoa(levels.Music)
		v.DJVolume = strconv.Itoa(levels.DJ)
		v.NormaliseMusic = levels.NormaliseMusic
	}
	return v, err
}
func (s *Server) mixerPage(w http.ResponseWriter, r *http.Request) {
	if s.d.Mixer == nil {
		http.Error(w, "Mixer unavailable", 503)
		return
	}
	v, err := s.mixerView(r)
	if errors.Is(err, station.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	populateMixerChannels(&v)
	s.renderPage(w, r, http.StatusOK, "mixer-page", v)
}
func (s *Server) saveMixer(w http.ResponseWriter, r *http.Request) {
	if s.d.Mixer == nil {
		http.Error(w, "Mixer unavailable", 503)
		return
	}
	v, err := s.mixerView(r)
	if errors.Is(err, station.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	autosave := r.Header.Get("X-Mixer-Autosave") == "true"
	v.MusicVolume = r.PostForm.Get("music_volume")
	v.DJVolume = r.PostForm.Get("dj_volume")
	music, e1 := strconv.Atoi(v.MusicVolume)
	dj, e2 := strconv.Atoi(v.DJVolume)
	v.NormaliseMusic = r.PostForm.Get("normalise_music") == "on"
	levels := mixer.Levels{Music: music, DJ: dj, NormaliseMusic: v.NormaliseMusic}
	if e1 != nil || e2 != nil || levels.Validate() != nil {
		v.MixerError = "Choose music and DJ volumes between 0% and 400%."
		if autosave {
			s.json(w, http.StatusBadRequest, map[string]string{"error": v.MixerError})
			return
		}
		populateMixerChannels(&v)
		s.renderPage(w, r, http.StatusOK, "mixer-page", v)
		return
	}
	if err = s.d.Mixer.Save(r.Context(), v.Station.ID, levels); err != nil {
		v.MixerError = "Couldn’t save the mixer. Please try again."
		if autosave {
			s.json(w, http.StatusServiceUnavailable, map[string]string{"error": v.MixerError})
			return
		}
		populateMixerChannels(&v)
		s.renderPage(w, r, http.StatusOK, "mixer-page", v)
		return
	}
	if autosave {
		if s.d.Broadcast != nil {
			s.d.Broadcast.Changed()
		}
		s.json(w, http.StatusOK, map[string]string{"status": "saved"})
		return
	}
	s.redirect(w, r, "/producer/stations/"+v.Station.ID+"/mixer?notice=mixer-saved")
}
