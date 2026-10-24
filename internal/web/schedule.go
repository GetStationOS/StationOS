package web

import (
	"errors"
	"net/http"
	"stationos.local/stationos/internal/episodes"
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
type scheduleCard struct {
	ID, Name, DJ, Playlist, Time, Class string
	EpisodeStart, Status                string
	TimingChanged                       bool
	Day                                 int
	URL                                 string
}

var weekDays = []scheduleDay{{0, "Monday"}, {1, "Tuesday"}, {2, "Wednesday"}, {3, "Thursday"}, {4, "Friday"}, {5, "Saturday"}, {6, "Sunday"}}

func (s *Server) scheduleView(r *http.Request) (schedulePageView, error) {
	v := schedulePageView{pageView: s.baseView(r)}
	var err error
	v.Station, err = s.d.Stations.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		return v, err
	}
	v.StationID = v.Station.ID
	v.Section = "schedule"
	if strings.Contains(r.URL.Path, "/shows") || r.PathValue("episode") != "" {
		v.Section = "shows"
	}
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
	v.ScheduleCards = []scheduleCard{}
	loc, err := time.LoadLocation(v.Station.Timezone)
	if err != nil {
		return v, err
	}
	week := schedule.Monday(time.Now(), loc)
	if raw := r.URL.Query().Get("week"); raw != "" {
		parsed, e := time.ParseInLocation("2006-01-02", raw, loc)
		if e != nil {
			return v, schedule.ErrInvalid
		}
		week = schedule.Monday(parsed, loc)
	}
	v.Week = week.Format("2006-01-02")
	v.WeekLabel = week.Format("2 Jan 2006") + " – " + week.AddDate(0, 0, 6).Format("2 Jan 2006")
	v.PreviousWeek = week.AddDate(0, 0, -7).Format("2006-01-02")
	v.NextWeek = week.AddDate(0, 0, 7).Format("2006-01-02")
	for _, o := range schedule.Occurrences(v.Shows, week, loc) {
		if !o.End.After(week) || !o.Start.Before(week.AddDate(0, 0, 7)) {
			continue
		}
		e := episodes.New(o)
		if s.d.Episodes != nil {
			loaded, loadErr := s.d.Episodes.Store.Get(r.Context(), o.Key)
			if loadErr == nil {
				e = loaded
				if e.ShowKey != o.Key && e.Status != "finished" {
					e.Status = "attention"
				}
			} else if !errors.Is(loadErr, episodes.ErrNotFound) {
				return v, loadErr
			}
		}
		class := episodeScheduleClass(e)
		for date := time.Date(o.Start.In(loc).Year(), o.Start.In(loc).Month(), o.Start.In(loc).Day(), 0, 0, 0, 0, loc); date.Before(o.End); date = date.AddDate(0, 0, 1) {
			if date.Before(week) || !date.Before(week.AddDate(0, 0, 7)) {
				continue
			}
			first, last := o.Start.In(loc), o.End.In(loc)
			if first.Before(date) {
				first = date
			}
			midnight := date.AddDate(0, 0, 1)
			if last.After(midnight) {
				last = midnight
			}
			startMinute, endMinute := first.Hour()*60+first.Minute(), last.Hour()*60+last.Minute()
			if last.Equal(midnight) {
				endMinute = 1440
			}
			day := (int(date.Weekday()) + 6) % 7
			v.ScheduleCards = append(v.ScheduleCards, scheduleCard{ID: o.ShowID, Name: o.Title, DJ: o.DJ.Name, Playlist: o.PlaylistName, Time: schedule.TimeLabel(startMinute) + "–" + schedule.TimeLabel(endMinute), Class: "day-" + strconv.Itoa(day) + " at-" + strconv.Itoa(startMinute/15) + " length-" + strconv.Itoa(max(1, (endMinute-startMinute)/15)) + " " + class, EpisodeStart: o.Start.Format(time.RFC3339), Status: e.Label(), TimingChanged: !e.TimingChangedAt.IsZero(), Day: day, URL: episodePath(o.StationID, o.Key)})
		}
	}
	v.Show = schedule.Show{StationID: v.Station.ID, Start: 9 * 60, End: 10 * 60}
	v.ShowStart = "09:00"
	v.ShowEnd = "10:00"
	if strings.HasSuffix(r.URL.Path, "/new") || r.Method == http.MethodPost {
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
		v.ShowDays = map[int]bool{}
		for _, day := range v.Show.Weekdays() {
			v.ShowDays[day] = true
		}
		v.ShowStart = schedule.TimeLabel(v.Show.Start)
		v.ShowEnd = schedule.TimeLabel(v.Show.End)
		if strings.HasSuffix(r.URL.Path, "/delete") {
			v.ScheduleMode = "delete"
			v.Title = "Delete show"
		}
	}
	if v.ScheduleMode == "form" {
		if err := s.loadPlaylistOptions(r.Context(), &v.playlistChoices, v.Station); err != nil {
			return v, err
		}
	}
	switch r.URL.Query().Get("notice") {
	case "shows-added":
		v.Notice = "Show added on the selected days."
	case "show-saved":
		v.Notice = "Show saved."
	case "show-deleted":
		v.Notice = "Show archived. Its saved episodes are kept."
	}
	if selected := r.URL.Query().Get("episode"); selected != "" && s.d.Episodes != nil {
		var o schedule.Occurrence
		v, o, err = s.loadEpisode(r, v, selected)
		_ = o
		if err != nil {
			return v, err
		}
		v.SelectedEpisodeID = selected
		v.ScheduleMode = "list"
	}
	if r.URL.Query().Get("notice") == "episode-saved" {
		v.Notice = "Producer changes saved. The DJ will update the running order automatically."
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
	if v.SelectedEpisodeID != "" {
		s.renderPage(w, r, http.StatusOK, "episode-page", v)
		return
	}
	s.renderSchedulePage(w, r, http.StatusOK, v)
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
	v := schedule.Show{ID: r.PostForm.Get("show_id"), StationID: r.PathValue("id"), Name: r.PostForm.Get("name"), Description: r.PostForm.Get("description"), MusicDirection: r.PostForm.Get("music_direction"), DJID: r.PostForm.Get("dj"), PlaylistID: r.PostForm.Get("playlist"), Day: day, Days: days, Start: start, End: end}
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
	if dayError || len(days) == 0 || e2 != nil || e3 != nil {
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
	s.redirect(w, r, "/producer/stations/"+v.StationID+"/shows?notice="+notice)
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
	s.redirect(w, r, "/producer/stations/"+r.PathValue("id")+"/shows?notice=show-deleted")
}

func (s *Server) invalidShow(w http.ResponseWriter, r *http.Request, show schedule.Show, message string) {
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
	if i := strings.LastIndex(message, ", show_id="); i >= 0 {
		if end := strings.Index(message[i:], ")"); end >= 0 {
			message = message[:i] + message[i+end:]
		}
	}
	v.ShowError = message
	if r.Header.Get("HX-Request") == "true" {
		destination := "/producer/stations/" + v.Station.ID + "/shows/new"
		if show.ID != "" {
			destination = "/producer/stations/" + v.Station.ID + "/shows/" + show.ID
		}
		w.Header().Set("HX-Push-Url", destination)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.renderSchedulePage(w, r, http.StatusUnprocessableEntity, v)
}

func (s *Server) renderSchedulePage(w http.ResponseWriter, r *http.Request, status int, v schedulePageView) {
	name := "schedule-list-page"
	switch v.ScheduleMode {
	case "form":
		name = "schedule-form-page"
	case "delete":
		if fragmentTarget(r, "dialog-body") {
			s.renderHTML(w, status, "delete-show", v)
			return
		}
		name = "schedule-delete-page"
	}
	s.renderPage(w, r, status, name, v)
}

func episodeScheduleClass(e episodes.Episode) string {
	switch {
	case e.Cancelled:
		return "episode-cancelled"
	case e.Status == "starting" || e.Status == "planning":
		return "episode-preparing"
	case e.PlanningFailed:
		return "episode-delayed"
	case e.Status == "ready":
		return "episode-ready"
	case e.Status == "attention":
		return "episode-preparing"
	default:
		return "episode-scheduled"
	}
}
