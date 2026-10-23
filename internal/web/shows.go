package web

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/schedule"
)

type showEpisodeRow struct {
	URL, Date, Time, Status string
	Start                   time.Time
}

type showEpisodeHistory interface {
	ListShow(context.Context, string, string) ([]episodes.Summary, error)
}

func (s *Server) showsPage(w http.ResponseWriter, r *http.Request) {
	if s.d.Schedule == nil || s.d.DJs == nil {
		http.Error(w, "Shows unavailable", 503)
		return
	}
	v, err := s.scheduleView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	v.Title = "Shows"
	if r.URL.Query().Get("archived") == "1" {
		if repo, ok := s.d.Schedule.(interface {
			ListArchived(context.Context, string) ([]schedule.Show, error)
		}); ok {
			v.Shows, err = repo.ListArchived(r.Context(), v.Station.ID)
			if err != nil {
				s.scheduleError(w, r, err)
				return
			}
			v.ScheduleMode = "archived"
		}
	}
	sort.Slice(v.Shows, func(i, j int) bool {
		if v.Shows[i].Name == v.Shows[j].Name {
			return v.Shows[i].ID < v.Shows[j].ID
		}
		return v.Shows[i].Name < v.Shows[j].Name
	})
	s.renderPage(w, r, http.StatusOK, "shows-page", v)
}

func (s *Server) showEpisodesPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.scheduleView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	v.Title = v.Show.Name
	v.ScheduleMode = "episodes"
	loc, err := time.LoadLocation(v.Station.Timezone)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	known := map[string]bool{}
	add := func(e episodes.Summary) {
		key := episodes.SessionKey(e.Key)
		if known[key] {
			return
		}
		known[key] = true
		start, end := e.Start.In(loc), e.End.In(loc)
		status := e.Label
		if e.End.Before(time.Now()) && !e.Cancelled {
			status = "No airtime recorded"
			if e.AiredCount > 0 {
				status = "Aired"
			}
		}
		v.ShowEpisodes = append(v.ShowEpisodes, showEpisodeRow{URL: episodePath(v.Station.ID, key), Date: start.Format("Mon 2 Jan 2006"), Time: start.Format("15:04") + "–" + end.Format("15:04 MST"), Status: status, Start: start})
	}
	now := time.Now()
	for _, o := range schedule.Occurrences([]schedule.Show{v.Show}, now, loc) {
		if o.End.Before(now) {
			continue
		}
		e := episodes.New(o)
		if s.d.Episodes != nil {
			loaded, err := s.d.Episodes.Store.Get(r.Context(), o.Key)
			if err == nil {
				e = loaded
				if len(e.Aired) == 0 {
					e.Start, e.End = o.Start, o.End
				}
			} else if !errors.Is(err, episodes.ErrNotFound) {
				s.scheduleError(w, r, err)
				return
			}
		}
		add(episodes.Summary{Key: e.Key, Start: e.Start, End: e.End, Label: e.Label(), AiredCount: len(e.Aired), Cancelled: e.Cancelled})
	}
	if s.d.Episodes != nil {
		if history, ok := s.d.Episodes.Store.(showEpisodeHistory); ok {
			saved, err := history.ListShow(r.Context(), v.Station.ID, v.Show.ID)
			if err != nil {
				s.scheduleError(w, r, err)
				return
			}
			for _, e := range saved {
				add(e)
			}
		}
	}
	sort.Slice(v.ShowEpisodes, func(i, j int) bool {
		a, b := v.ShowEpisodes[i].Start, v.ShowEpisodes[j].Start
		if a.After(now) != b.After(now) {
			return a.After(now)
		}
		if a.After(now) {
			return a.Before(b)
		}
		return a.After(b)
	})
	page, size, err := pageRequest(r.URL.Query())
	if err != nil {
		s.scheduleError(w, r, schedule.ErrInvalid)
		return
	}
	v.ShowEpisodePagination = paging("/producer/stations/"+v.Station.ID+"/shows/"+v.Show.ID+"/episodes", r.URL.Query(), page, size, len(v.ShowEpisodes))
	v.ShowEpisodePagination.CountLabel = "episode"
	v.ShowEpisodePagination.SizeLabel = "Episodes per page"
	v.ShowEpisodes = v.ShowEpisodes[(v.ShowEpisodePagination.Number-1)*size : min(v.ShowEpisodePagination.Number*size, len(v.ShowEpisodes))]
	s.renderPage(w, r, http.StatusOK, "show-episodes-page", v)
}
