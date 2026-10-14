package web

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

type publicShow struct {
	ID, Name, Presenter, Time, ContributionURL, URL, Description, Next, NextLabel, Portrait string
	Expanded                                                                                bool
}
type publicDay struct {
	Name    string
	URL     string
	Current bool
	Shows   []publicShow
}

// Only read-only listener routes bypass management authentication.
func publicRead(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return r.URL.Path == "/" || r.URL.Path == "/radio-events" || strings.HasPrefix(r.URL.Path, "/radio/") || strings.HasPrefix(r.URL.Path, "/static/")
}
func (s *Server) adminAuthenticated(r *http.Request) bool {
	if s.d.Password == "" {
		return false
	}
	user, pass, ok := r.BasicAuth()
	want, got := sha256.Sum256([]byte(s.d.Password)), sha256.Sum256([]byte(pass))
	return ok && user == "admin" && subtle.ConstantTimeCompare(want[:], got[:]) == 1
}
func (s *Server) publicStation(r *http.Request) (station.Station, error) {
	slug := r.PathValue("slug")
	if lookup, ok := s.d.Stations.(station.StreamLookup); ok {
		st, err := lookup.ByStreamSlug(r.Context(), slug)
		if err == nil && !st.Public() {
			return station.Station{}, station.ErrNotFound
		}
		return st, err
	}
	stations, err := s.d.Stations.List(r.Context())
	if err != nil {
		return station.Station{}, err
	}
	for _, st := range stations {
		if st.StreamSlug == slug && st.Public() {
			return st, nil
		}
	}
	return station.Station{}, station.ErrNotFound
}
func (s *Server) publicError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, station.ErrNotFound) {
		http.NotFound(w, r)
	} else {
		s.fail(w, err)
	}
}
func publicTrack(st station.Station, track cardTrack) cardTrack {
	if strings.HasPrefix(track.Artwork, "/producer/stations/") {
		track.Artwork = strings.Replace(track.Artwork, "/producer/stations/"+url.PathEscape(st.ID), "/radio/"+url.PathEscape(st.StreamSlug), 1)
	} else if strings.HasPrefix(track.Artwork, "/djs/") {
		track.Artwork = "/radio/" + url.PathEscape(st.StreamSlug) + "/presenters/" + strings.TrimPrefix(track.Artwork, "/djs/")
	}
	return track
}
func (s *Server) publicCard(st station.Station) stationCard {
	card := stationCard{Station: st, Broadcast: broadcast.State{Status: "Dormant"}}
	if s.d.Broadcast != nil {
		card.Broadcast = s.d.Broadcast.State(st.ID)
	}
	card.CurrentTrack = publicTrack(st, currentBroadcastTrack(st.ID, card.Broadcast))
	card.NextTrack = publicTrack(st, stationTrack(st.ID, "UP NEXT", "Not queued", card.Broadcast.Next))
	card.Broadcast.Programme.DJ.PortraitURL = publicPresenterPortrait("/radio/"+url.PathEscape(st.StreamSlug), card.Broadcast.Programme.DJ.ID, card.Broadcast.Programme.DJ.PortraitURL)
	// Runtime failure detail and editorial configuration belong to the producer.
	card.Broadcast.Error = ""
	return card
}
func (s *Server) publicView(r *http.Request) (view, error) {
	v := view{Public: true, Page: r.URL.Path, Title: "Radio stations", BroadcastEnabled: s.d.Broadcast != nil}
	if r.PathValue("slug") == "" {
		stations, err := s.d.Stations.List(r.Context())
		if err != nil {
			return v, err
		}
		for _, st := range stations {
			if st.Publication() == station.Listed {
				v.StationCards = append(v.StationCards, s.publicCard(st))
			}
		}
		return v, nil
	}
	st, err := s.publicStation(r)
	if err != nil {
		return v, err
	}
	v.Station = st
	v.AudiencePanel = "contribution-panel"
	v.AudienceBase = "/radio/" + url.PathEscape(st.StreamSlug)
	v.Title = st.Name
	v.StreamURL = s.streamURL(r, st.StreamSlug)
	card := s.publicCard(st)
	v.Broadcast = card.Broadcast
	v.PublicCurrent = card.CurrentTrack
	v.PublicNext = card.NextTrack
	if s.d.Schedule != nil {
		shows, err := s.d.Schedule.List(r.Context(), st.ID)
		if err != nil {
			return v, err
		}
		loc, err := time.LoadLocation(st.Timezone)
		if err != nil {
			return v, err
		}
		now := time.Now()
		var date time.Time
		v.PublicDayLinks, date = publicScheduleDays(now, loc, r.URL.Query().Get("day"), v.AudienceBase)
		v.PublicDate = date.Format("Monday 2 January")
		v.AudienceDay = date.Format("2006-01-02")
		contributions := map[string]string{}
		if s.d.Audience != nil && s.d.BroadcastConfig != nil {
			for _, o := range schedule.Occurrences(shows, now, loc) {
				if o.End.After(now) && !o.Start.After(now.Add(7*24*time.Hour)) && (o.AcceptRequests || o.AcceptShoutouts) && contributions[o.ShowID] == "" {
					contributions[o.ShowID] = "/radio/" + url.PathEscape(st.StreamSlug) + "/contribute?show=" + url.QueryEscape(o.Key)
				}
			}
		}
		if id := r.PathValue("show"); id != "" {
			show, err := s.d.Schedule.Get(r.Context(), st.ID, id)
			if errors.Is(err, schedule.ErrNotFound) {
				return v, station.ErrNotFound
			}
			if err != nil {
				return v, err
			}
			v.AudiencePanel = "contribution-" + id
			v.AudienceBase += "/shows/" + url.PathEscape(id)
			v.Title = show.Name
			v.PublicShow = publicShow{ID: id, Name: show.Name, Presenter: show.DJName, Description: show.Description, Time: weekDays[show.Day].Name + " · " + schedule.TimeLabel(show.Start) + "–" + schedule.TimeLabel(show.End), URL: v.AudienceBase}
			if show.End <= show.Start {
				v.PublicShow.Time += " (next day)"
			}
			v.PublicShow.Portrait = publicPresenterPortrait("/radio/"+url.PathEscape(st.StreamSlug), show.DJID, show.DJ.PortraitURL)
			for _, o := range schedule.Occurrences(shows, now, loc) {
				if o.ShowID == id && ((r.URL.Query().Get("show") != "" && o.Key == r.URL.Query().Get("show")) || (r.URL.Query().Get("show") == "" && o.End.After(now))) {
					v.PublicShow.NextLabel = "Next airing"
					if !o.Start.After(now) {
						v.PublicShow.NextLabel = "This airing"
					}
					v.PublicShow.Next = o.Start.Format("Mon 2 Jan, 15:04 MST")
					if contributions[id] != "" && o.End.After(now) {
						v.PublicShow.ContributionURL = v.AudienceBase + "/contribute?show=" + url.QueryEscape(o.Key) + "&day=" + v.AudienceDay
					}
					break
				}
			}
		}
		v.PublicDays = []publicDay{{Name: v.PublicDate, Shows: publicDayProgramme(shows, now, date, loc, "/radio/"+url.PathEscape(st.StreamSlug), s.d.Audience != nil && s.d.BroadcastConfig != nil)}}
	}
	return v, nil
}
func (s *Server) publicPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.publicView(r)
	if err != nil {
		s.publicError(w, r, err)
		return
	}
	name := "public-layout"
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true" {
		name = "public-fragment"
		if r.PathValue("show") != "" && r.Header.Get("HX-Target") == "show-details-"+r.PathValue("show") {
			name = "public-show-expanded"
		}
		if r.PathValue("show") != "" && r.Header.Get("HX-Target") == v.AudiencePanel {
			name = "audience-form"
		}
	}
	var body bytes.Buffer
	if err = s.t.ExecuteTemplate(&body, name, v); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body.Bytes())
}
func (s *Server) publicArtwork(w http.ResponseWriter, r *http.Request) {
	st, err := s.publicStation(r)
	if err != nil {
		s.publicError(w, r, err)
		return
	}
	r.SetPathValue("id", st.ID)
	s.stationArtwork(w, r)
}
func (s *Server) publicDJPhoto(w http.ResponseWriter, r *http.Request) {
	st, err := s.publicStation(r)
	if err != nil {
		s.publicError(w, r, err)
		return
	}
	// A presenter portrait is available only when scheduled on this station.
	if s.d.Schedule == nil || s.d.DJs == nil {
		http.NotFound(w, r)
		return
	}
	shows, err := s.d.Schedule.List(r.Context(), st.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, show := range shows {
		if show.DJID == r.PathValue("id") {
			s.djPhoto(w, r)
			return
		}
	}
	http.NotFound(w, r)
}
func (s *Server) publicDirectoryEvents(w http.ResponseWriter, r *http.Request) {
	s.publicEvents(w, r, "public-cards", "stations")
}
func (s *Server) publicLive(w http.ResponseWriter, r *http.Request) {
	s.publicEvents(w, r, "public-live", "public")
}
func (s *Server) publicPlayerEvents(w http.ResponseWriter, r *http.Request) {
	s.publicEvents(w, r, "player-tracks", "player")
}

// Bounded observers render public projections without subscribing to audio.
func (s *Server) publicEvents(w http.ResponseWriter, r *http.Request, templateName, eventName string) {
	initial, err := s.publicView(r)
	if err != nil {
		s.publicError(w, r, err)
		return
	}
	if s.d.Broadcast == nil {
		http.Error(w, "live information unavailable", 503)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		return
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
	update := func(v view) bool {
		var html bytes.Buffer
		if s.t.ExecuteTemplate(&html, templateName, v) != nil {
			return false
		}
		hash := sha256.Sum256(html.Bytes())
		if hash == previous {
			return true
		}
		data, err := json.Marshal(html.String())
		if err != nil {
			return false
		}
		if !write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", eventName, data))) {
			return false
		}
		previous = hash
		return true
	}
	if !update(initial) {
		return
	}
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
			v, err := s.publicView(r)
			if err != nil {
				write([]byte("event: unavailable\ndata: {}\n\n"))
				return
			}
			if !update(v) {
				return
			}
		case <-heartbeat.C:
			v, err := s.publicView(r)
			if err != nil {
				write([]byte("event: unavailable\ndata: {}\n\n"))
				return
			}
			if !update(v) || !write([]byte(": keepalive\n\n")) {
				return
			}
		}
	}
}

// Civil-day navigation stays correct across daylight-saving clock changes.
func publicScheduleDays(now time.Time, loc *time.Location, raw, base string) ([]publicDay, time.Time) {
	now = now.In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	selected := today
	days := make([]publicDay, 0, 7)
	for i := 0; i < 7; i++ {
		date := today.AddDate(0, 0, i)
		label := date.Format("Mon 2 Jan")
		if i == 0 {
			label = "Today"
		}
		if i == 1 {
			label = "Tomorrow"
		}
		if raw == date.Format("2006-01-02") {
			selected = date
		}
		days = append(days, publicDay{Name: label, URL: base + "?day=" + date.Format("2006-01-02")})
	}
	for i := range days {
		days[i].Current = strings.HasSuffix(days[i].URL, selected.Format("2006-01-02"))
	}
	return days, selected
}

// Today contains only unfinished programme; future civil days remain complete.
// Contribution links always identify the displayed airing.
func publicDayProgramme(shows []schedule.Show, now, date time.Time, loc *time.Location, base string, participation bool) []publicShow {
	today := date.In(loc).Format("2006-01-02") == now.In(loc).Format("2006-01-02")
	descriptions := map[string]string{}
	for _, show := range shows {
		descriptions[show.ID] = show.Description
	}
	end := date.AddDate(0, 0, 1)
	cursor := date
	out := []publicShow{}
	music := func(start, finish time.Time) {
		if !finish.After(start) || (today && !finish.After(now)) {
			return
		}
		label := finish.Format("15:04")
		if finish.Equal(end) {
			label = "24:00"
		}
		out = append(out, publicShow{Name: "Continuous music", Time: start.Format("15:04") + "–" + label})
	}
	for _, o := range schedule.Occurrences(shows, date, loc) {
		if !o.End.After(date) || !o.Start.Before(end) {
			continue
		}
		start, finish := o.Start, o.End
		if start.Before(date) {
			start = date
		}
		if finish.After(end) {
			finish = end
		}
		if today && !o.End.After(now) {
			cursor = finish
			continue
		}
		music(cursor, start)
		times := start.Format("15:04") + "–" + finish.Format("15:04")
		if finish.Equal(end) {
			times = start.Format("15:04") + "–24:00"
		}
		query := "?show=" + url.QueryEscape(o.Key) + "&day=" + date.Format("2006-01-02")
		item := publicShow{ID: o.ShowID, Name: o.Title, Presenter: o.DJ.Name, Portrait: publicPresenterPortrait(base, o.DJ.ID, o.DJ.PortraitURL), Time: times, URL: base + "/shows/" + url.PathEscape(o.ShowID) + query}
		item.Description = descriptions[o.ShowID]
		item.Expanded = today && !o.Start.After(now) && o.End.After(now)
		item.NextLabel = "Next airing"
		if !o.Start.After(now) {
			item.NextLabel = "This airing"
		}
		item.Next = o.Start.Format("Mon 2 Jan, 15:04 MST")
		if participation && o.End.After(now) && !o.Start.After(now.Add(7*24*time.Hour)) && (o.AcceptRequests || o.AcceptShoutouts) {
			item.ContributionURL = base + "/shows/" + url.PathEscape(o.ShowID) + "/contribute" + query
		}
		out = append(out, item)
		cursor = finish
	}
	music(cursor, end)
	return out
}

// Uploaded portraits retain the station visibility and scheduled-presenter checks.
func publicPresenterPortrait(base, id, portrait string) string {
	if strings.HasPrefix(portrait, "/djs/") {
		return base + "/presenters/" + url.PathEscape(id) + "/photo"
	}
	return portrait
}

// Default expansion reuses the same details and invitation as an HTMX opening.
func (show publicShow) Details() view {
	return view{PublicShow: show, AudiencePanel: "contribution-" + show.ID}
}
