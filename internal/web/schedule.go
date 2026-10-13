package web

import (
	"errors"
	"net/http"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
	"strconv"
	"strings"
	"time"
)

type scheduleHour struct{ Class, Label string }
type scheduleDay struct {
	Index int
	Name  string
}
type scheduleCard struct{ ID, Name, DJ, Playlist, Time, Class string }

var weekDays = []scheduleDay{{0, "Monday"}, {1, "Tuesday"}, {2, "Wednesday"}, {3, "Thursday"}, {4, "Friday"}, {5, "Saturday"}, {6, "Sunday"}}

func calendarCards(shows []schedule.Show) []scheduleCard {
	cards := []scheduleCard{}
	for _, s := range shows {
		end := s.Start + s.Duration()
		add := func(day, start, end int, continuation bool) {
			name := s.Name
			if continuation {
				name += " (continued)"
			}
			cards = append(cards, scheduleCard{s.ID, name, s.DJName, s.PlaylistName, schedule.TimeLabel(start) + "–" + schedule.TimeLabel(end), "day-" + strconv.Itoa(day) + " at-" + strconv.Itoa(start/15) + " length-" + strconv.Itoa((end-start)/15)})
		}
		add(s.Day, s.Start, min(end, 1440), false)
		if end > 1440 {
			add((s.Day+1)%7, 0, end-1440, true)
		}
	}
	return cards
}
func (s *Server) scheduleView(r *http.Request) (view, error) {
	v, err := s.baseView(r)
	if err != nil {
		return v, err
	}
	v.Station, err = s.d.Stations.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		return v, err
	}
	v.StationID = v.Station.ID
	v.Section = "schedule"
	v.Title = "Schedule"
	v.ScheduleMode = "list"
	v.ScheduleDays = weekDays
	v.ShowDays = map[int]bool{0: true}
	for h := 0; h < 24; h++ {
		v.ScheduleHours = append(v.ScheduleHours, scheduleHour{"at-" + strconv.Itoa(h*4), schedule.TimeLabel(h * 60)})
	}
	v.DJs, err = s.d.DJs.List(r.Context())
	if err != nil {
		return v, err
	}
	v.Shows, err = s.d.Schedule.List(r.Context(), v.Station.ID)
	if err != nil {
		return v, err
	}
	v.ScheduleCards = calendarCards(v.Shows)
	v.Show = schedule.Show{StationID: v.Station.ID, Start: 9 * 60, End: 10 * 60}
	v.ShowStart = "09:00"
	v.ShowEnd = "10:00"
	if strings.HasSuffix(r.URL.Path, "/new") {
		v.ScheduleMode = "form"
		v.Title = "Add show"
	}
	if id := r.PathValue("show"); id != "" {
		v.Show, err = s.d.Schedule.Get(r.Context(), v.Station.ID, id)
		if err != nil {
			return v, err
		}
		v.ScheduleMode = "form"
		v.Title = v.Show.Name
		v.ShowStart = schedule.TimeLabel(v.Show.Start)
		v.ShowEnd = schedule.TimeLabel(v.Show.End)
		if strings.HasSuffix(r.URL.Path, "/delete") {
			v.ScheduleMode = "delete"
			v.Title = "Delete show"
		}
	}
	switch r.URL.Query().Get("notice") {
	case "shows-added":
		v.Notice = "Shows added on the selected days."
	case "show-saved":
		v.Notice = "Show saved."
	case "show-deleted":
		v.Notice = "Show deleted."
	}
	return v, nil
}
func (s *Server) schedulePage(w http.ResponseWriter, r *http.Request) {
	if s.d.Schedule == nil || s.d.DJs == nil {
		http.Error(w, "Schedule unavailable", 503)
		return
	}
	v, err := s.scheduleView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	s.render(w, r, v)
}
func (s *Server) scheduleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, schedule.ErrNotFound), errors.Is(err, station.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, schedule.ErrOverlap), errors.Is(err, schedule.ErrInvalid):
		http.Error(w, err.Error(), 422)
	default:
		s.fail(w, err)
	}
}
func parseShowTime(raw string) (int, error) {
	t, err := time.Parse("15:04", raw)
	if err != nil {
		return 0, schedule.ErrInvalid
	}
	return t.Hour()*60 + t.Minute(), nil
}
func (s *Server) saveShow(w http.ResponseWriter, r *http.Request) {
	if s.d.Schedule == nil {
		http.Error(w, "Schedule unavailable", 503)
		return
	}
	days := []int{}
	selected := map[int]bool{}
	dayError := false
	for _, raw := range r.PostForm["day"] {
		day, err := strconv.Atoi(raw)
		if err != nil || day < 0 || day > 6 || selected[day] {
			dayError = true
			continue
		}
		selected[day] = true
		days = append(days, day)
	}
	day := 0
	if len(days) > 0 {
		day = days[0]
	}
	start, e2 := parseShowTime(r.PostForm.Get("start"))
	end, e3 := parseShowTime(r.PostForm.Get("end"))
	v := schedule.Show{ID: r.PostForm.Get("show_id"), StationID: r.PathValue("id"), Name: r.PostForm.Get("name"), Description: r.PostForm.Get("description"), DJID: r.PostForm.Get("dj"), PlaylistID: r.PostForm.Get("playlist"), Day: day, Start: start, End: end}
	if r.PostForm.Get("audience_settings") == "yes" {
		v.AcceptRequests = r.PostForm.Get("accept_requests") == "yes"
		v.AcceptShoutouts = r.PostForm.Get("accept_shoutouts") == "yes"
	} else if v.ID != "" {
		old, err := s.d.Schedule.Get(r.Context(), v.StationID, v.ID)
		if err != nil {
			s.scheduleError(w, r, err)
			return
		}
		v.AcceptRequests, v.AcceptShoutouts = old.AcceptRequests, old.AcceptShoutouts
	}
	if dayError || len(days) == 0 || (v.ID != "" && len(days) != 1) || e2 != nil || e3 != nil {
		s.invalidShow(w, r, v, "Choose at least one day and valid quarter-hour start/end times.")
		return
	}
	service := schedule.Service{Repo: s.d.Schedule}
	var err error
	notice := "show-saved"
	if v.ID == "" {
		err = service.CreateDays(r.Context(), v, days)
		notice = "shows-added"
	} else {
		_, err = service.Save(r.Context(), v)
	}
	if err != nil {
		if errors.Is(err, schedule.ErrInvalid) || errors.Is(err, schedule.ErrOverlap) {
			s.invalidShow(w, r, v, err.Error())
		} else {
			s.scheduleError(w, r, err)
		}
		return
	}
	s.redirect(w, r, "/producer/stations/"+v.StationID+"/schedule?notice="+notice)
}
func (s *Server) deleteShow(w http.ResponseWriter, r *http.Request) {
	if s.d.Schedule == nil {
		http.Error(w, "Schedule unavailable", 503)
		return
	}
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "Confirm show deletion", 400)
		return
	}
	if err := s.d.Schedule.Delete(r.Context(), r.PathValue("id"), r.PathValue("show")); err != nil {
		s.scheduleError(w, r, err)
		return
	}
	s.redirect(w, r, "/producer/stations/"+r.PathValue("id")+"/schedule?notice=show-deleted")
}

func (s *Server) invalidShow(w http.ResponseWriter, r *http.Request, show schedule.Show, message string) {
	if r.Header.Get("HX-Request") == "true" {
		http.Error(w, message, 422)
		return
	}
	v, err := s.scheduleView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	v.Show = show
	v.ShowDays = map[int]bool{}
	for _, raw := range r.PostForm["day"] {
		if day, err := strconv.Atoi(raw); err == nil && day >= 0 && day <= 6 {
			v.ShowDays[day] = true
		}
	}
	v.ShowStart = r.PostForm.Get("start")
	v.ShowEnd = r.PostForm.Get("end")
	v.ScheduleMode = "form"
	v.Title = "Add show"
	if show.ID != "" {
		v.Title = show.Name
	}
	v.ShowError = message
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(422)
	s.render(w, r, v)
}
