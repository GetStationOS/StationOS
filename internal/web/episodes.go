package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/schedule"
)

func (s *Server) episodeView(r *http.Request) (schedulePageView, schedule.Occurrence, error) {
	v, err := s.scheduleView(r)
	if err != nil {
		return v, schedule.Occurrence{}, err
	}
	if s.d.Episodes == nil {
		return v, schedule.Occurrence{}, schedule.ErrNotFound
	}
	return s.loadEpisode(r, v, v.Show.ID)
}
func (s *Server) loadEpisode(r *http.Request, v schedulePageView, showID string) (schedulePageView, schedule.Occurrence, error) {
	var err error
	v.Show, err = s.d.Schedule.Get(r.Context(), v.Station.ID, showID)
	if err != nil {
		return v, schedule.Occurrence{}, err
	}
	start, err := time.Parse(time.RFC3339, r.URL.Query().Get("start"))
	if err != nil {
		return v, schedule.Occurrence{}, schedule.ErrInvalid
	}
	loc, err := time.LoadLocation(v.Station.Timezone)
	if err != nil {
		return v, schedule.Occurrence{}, err
	}
	for _, o := range schedule.Occurrences([]schedule.Show{v.Show}, start, loc) {
		if !o.Start.Equal(start) {
			continue
		}
		v.Episode, err = s.d.Episodes.Store.Get(r.Context(), o.Key)
		if errors.Is(err, episodes.ErrNotFound) {
			v.Episode = episodes.New(o)
			err = nil
		}
		if err == nil && v.Episode.ShowKey != o.Key {
			v.Episode.Status = "attention"
			v.Episode.Problem = "Show settings changed; the DJ will update this episode automatically."
		}
		v.Title = o.Title + " · " + o.Start.In(loc).Format("2 Jan 2006")
		v.EpisodeStart = o.Start.Format(time.RFC3339)
		v.Week = schedule.Monday(o.Start, loc).Format("2006-01-02")
		return v, o, err
	}
	return v, schedule.Occurrence{}, schedule.ErrNotFound
}
func (s *Server) episodePage(w http.ResponseWriter, r *http.Request) {
	v, _, err := s.episodeView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	if r.URL.Query().Get("notice") == "saved" {
		v.Notice = "Producer changes saved. The DJ will update the plan automatically."
	}
	s.renderPage(w, r, 200, "episode-page", v)
}
func (s *Server) saveEpisode(w http.ResponseWriter, r *http.Request) {
	v, o, err := s.episodeView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	version, err := strconv.Atoi(r.PostForm.Get("version"))
	if err == nil {
		err = s.d.Episodes.Edit(r.Context(), o, version, r.PostForm.Get("notes"), r.PostForm["locked"], r.PostForm["excluded"])
	}
	if err != nil {
		v.Episode.Notes = r.PostForm.Get("notes")
		kept := map[string]bool{}
		for _, id := range r.PostForm["locked"] {
			kept[id] = true
		}
		for i := range v.Episode.Plan {
			v.Episode.Plan[i].Locked = kept[v.Episode.Plan[i].ID]
		}
		v.EpisodeExclusions = map[string]bool{}
		for _, id := range r.PostForm["excluded"] {
			v.EpisodeExclusions[id] = true
		}
		v.EpisodeError = err.Error()
		s.renderPage(w, r, 422, "episode-page", v)
		return
	}
	s.redirect(w, r, "/producer/stations/"+v.Station.ID+"/schedule/"+url.PathEscape(o.ShowID)+"/episode?start="+url.QueryEscape(o.Start.Format(time.RFC3339))+"&notice=saved")
}
func (s *Server) scheduleLive(w http.ResponseWriter, r *http.Request) {
	v, err := s.scheduleView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	s.renderHTML(w, 200, "schedule-list", v)
}
