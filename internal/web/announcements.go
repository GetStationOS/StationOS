package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"stationos.local/stationos/internal/announcements"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
	"strconv"
	"strings"
	"sync"
	"time"
)

type announcementRow struct {
	Message          announcements.Message
	State, Last, Due string
}
type announcementOption struct {
	ID, Name string
	Selected bool
}
type announcementPageView struct {
	pageView
	Message                                                                   announcements.Message
	Settings                                                                  announcements.Settings
	Rows                                                                      []announcementRow
	DJs                                                                       []djs.Profile
	Shows, Episodes                                                           []announcementOption
	History                                                                   []announcements.Receipt
	Pagination                                                                pagination
	Base, Error, StartInput, ExpiryInput, TimesInput, PreviewURL, PreviewText string
	Mode                                                                      string
}
type announcementPreview struct {
	Text, MessageID string
	Version         int
	Audio           []byte
	Expires         time.Time
	StationID       string
}
type announcementPreviews struct {
	sync.Mutex
	Items   map[string]announcementPreview
	Running int
}

func (s *Server) announcementView(r *http.Request) (announcementPageView, error) {
	v := announcementPageView{pageView: s.baseView(r), Mode: "list"}
	if s.d.Announcements == nil {
		return v, errors.New("announcements unavailable")
	}
	var err error
	v.Station, err = s.d.Stations.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		return v, err
	}
	v.StationID = v.Station.ID
	v.Section = "announcements"
	v.Title = "Announcements"
	v.Base = "/producer/stations/" + v.Station.ID + "/announcements"
	return v, nil
}
func (s *Server) announcementChoices(r *http.Request, v *announcementPageView) error {
	var err error
	v.DJs, err = s.d.DJs.List(r.Context())
	if err != nil {
		return err
	}
	shows, err := s.d.Schedule.List(r.Context(), v.Station.ID)
	if err != nil {
		return err
	}
	for _, show := range shows {
		v.Shows = append(v.Shows, announcementOption{show.ID, show.Name, slices.Contains(v.Message.ShowIDs, show.ID)})
	}
	loc, err := time.LoadLocation(v.Station.Timezone)
	if err != nil {
		return err
	}
	now := time.Now()
	seen := map[string]bool{}
	for _, o := range schedule.Occurrences(shows, now, loc) {
		if o.End.Before(now) || o.Start.After(now.AddDate(0, 0, 21)) {
			continue
		}
		key := schedule.EpisodeKey(o.Key)
		seen[key] = true
		v.Episodes = append(v.Episodes, announcementOption{key, o.Title + " · " + o.Start.In(loc).Format("Mon 2 Jan, 15:04 MST"), v.Message.EpisodeKey == key})
	}
	if v.Message.EpisodeKey != "" && !seen[v.Message.EpisodeKey] {
		v.Episodes = append(v.Episodes, announcementOption{v.Message.EpisodeKey, "Saved episode · " + v.Message.EpisodeKey, true})
	}
	return nil
}
func (s *Server) announcementPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.announcementView(r)
	if err != nil {
		s.announcementHTTPError(w, r, err)
		return
	}
	switch r.PathValue("message") {
	case "new":
		v.Mode = "form"
		v.Title = "Create announcement"
		now := time.Now()
		v.Message = announcements.Message{StationID: v.Station.ID, Category: "message", Mode: "exact", Status: "draft", Repeat: "once", Starts: now, Expires: now.AddDate(0, 0, 7), IntervalMinutes: 90}
	case "settings":
		v.Mode = "settings"
		v.Title = "Announcement settings"
		v.Settings, err = s.d.Announcements.Settings(r.Context(), v.Station.ID)
	case "":
		var items []announcements.Message
		items, err = s.d.Announcements.List(r.Context(), v.Station.ID)
		if err == nil {
			loc, e := time.LoadLocation(v.Station.Timezone)
			if e != nil {
				s.fail(w, e)
				return
			}
			now := time.Now()
			sort.SliceStable(items, func(i, j int) bool {
				if items[i].Next != items[j].Next {
					return items[i].Next
				}
				return items[i].Title < items[j].Title
			})
			page, size, e := pageRequest(r.URL.Query())
			if e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
			v.Pagination = paging(v.Base, r.URL.Query(), page, size, len(items))
			v.Pagination.Target = "#announcements-results"
			v.Pagination.CountLabel = "announcement"
			v.Pagination.SizeLabel = "Announcements per page"
			for _, m := range items {
				row := announcementRow{Message: m, State: m.State(now), Last: "Never", Due: "—"}
				if !m.LastAired.IsZero() {
					row.Last = m.LastAired.In(loc).Format("2 Jan, 15:04 MST")
				}
				due := m.NextOpportunity(now, loc)
				if !due.IsZero() {
					if due.After(now) {
						row.Due = due.In(loc).Format("2 Jan, 15:04 MST")
					} else {
						row.Due = "Eligible at a suitable break"
					}
				}
				if m.Next {
					row.Due = "Next eligible track boundary"
				}
				v.Rows = append(v.Rows, row)
				for _, at := range []time.Time{m.Starts, m.Expires, due} {
					if at.After(now) {
						old, _ := time.Parse(time.RFC3339Nano, w.Header().Get("X-Live-Deadline"))
						if old.IsZero() || at.Before(old) {
							w.Header().Set("X-Live-Deadline", at.Format(time.RFC3339Nano))
						}
					}
				}
			}
			start := (v.Pagination.Number - 1) * size
			end := min(start+size, len(v.Rows))
			if start < len(v.Rows) {
				v.Rows = v.Rows[start:end]
			} else {
				v.Rows = nil
			}
		}
	default:
		v.Mode = "form"
		v.Message, err = s.d.Announcements.Get(r.Context(), v.Station.ID, r.PathValue("message"))
		v.Title = "Edit announcement"
		if err == nil {
			v.History, err = s.d.Announcements.History(r.Context(), v.Station.ID, v.Message.ID)
			loc, _ := time.LoadLocation(v.Station.Timezone)
			for i := range v.History {
				v.History[i].Started = v.History[i].Started.In(loc)
				v.History[i].Ended = v.History[i].Ended.In(loc)
			}
		}
	}
	if err != nil {
		s.announcementHTTPError(w, r, err)
		return
	}
	switch r.URL.Query().Get("notice") {
	case "saved":
		v.Notice = "Announcement saved."
	case "deleted":
		v.Notice = "Announcement deleted; transmission history retained."
	case "active":
		v.Notice = "Announcement activated."
	case "paused":
		v.Notice = "Announcement paused."
	case "next":
		v.Notice = "Announcement prioritised for the next eligible track boundary."
	case "settings":
		v.Notice = "Announcement settings saved."
	}
	if token := r.URL.Query().Get("preview"); token != "" && v.Mode == "form" {
		s.announcementPreviews.Lock()
		p, ok := s.announcementPreviews.Items[token]
		s.announcementPreviews.Unlock()
		if ok && p.StationID == v.Station.ID && p.MessageID == v.Message.ID && p.Version == v.Message.Version && time.Now().Before(p.Expires) {
			v.PreviewURL = "/producer/stations/" + v.Station.ID + "/announcement-previews/" + token
			v.PreviewText = p.Text
			v.Notice = "Preview prepared. This has not been broadcast."
		} else {
			v.Error = "This preview expired or the message changed. Prepare a new preview."
		}
	}
	s.renderAnnouncement(w, r, 200, &v)
}
func (s *Server) renderAnnouncement(w http.ResponseWriter, r *http.Request, status int, v *announcementPageView) {
	if v.Mode == "form" || v.Mode == "settings" {
		if err := s.announcementChoices(r, v); err != nil {
			s.fail(w, err)
			return
		}
	}
	if v.Mode == "form" {
		loc, _ := time.LoadLocation(v.Station.Timezone)
		if v.StartInput == "" {
			v.StartInput = v.Message.Starts.In(loc).Format("2006-01-02T15:04")
		}
		if v.ExpiryInput == "" {
			v.ExpiryInput = v.Message.Expires.In(loc).Format("2006-01-02T15:04")
		}
		if v.TimesInput == "" {
			v.TimesInput = strings.Join(v.Message.Times, ", ")
		}
	}
	if fragmentTarget(r, "announcements-results") {
		s.renderHTML(w, status, "announcements-results", v)
		return
	}
	if fragmentRequest(r) && status == 422 {
		status = 200
	}
	s.renderPage(w, r, status, "announcements-page", *v)
}
func (s *Server) saveAnnouncement(w http.ResponseWriter, r *http.Request) {
	v, err := s.announcementView(r)
	if err != nil {
		s.announcementHTTPError(w, r, err)
		return
	}
	v.Mode = "form"
	v.Title = "Edit announcement"
	f := r.PostForm
	version, e := strconv.Atoi(f.Get("version"))
	if e != nil {
		http.Error(w, "invalid version", 400)
		return
	}
	id := r.PathValue("message")
	if id == "new" {
		id = announcements.ID()
		v.Title = "Create announcement"
	}
	interval, _ := strconv.Atoi(f.Get("interval"))
	v.StartInput = f.Get("starts")
	v.ExpiryInput = f.Get("expires")
	v.TimesInput = f.Get("times")
	loc, _ := time.LoadLocation(v.Station.Timezone)
	starts, se := time.ParseInLocation("2006-01-02T15:04", v.StartInput, loc)
	expires, ee := time.ParseInLocation("2006-01-02T15:04", v.ExpiryInput, loc)
	m := announcements.Message{ID: id, StationID: v.Station.ID, Title: strings.TrimSpace(f.Get("title")), Category: f.Get("category"), Text: strings.TrimSpace(f.Get("text")), Pronunciation: strings.TrimSpace(f.Get("pronunciation")), Mode: f.Get("mode"), Status: f.Get("status"), Repeat: f.Get("repeat"), DJID: f.Get("dj"), ShowIDs: f["shows"], EpisodeKey: f.Get("episode"), Starts: starts, Expires: expires, IntervalMinutes: interval, Version: version}
	if m.EpisodeKey != "" {
		parts := strings.Split(m.EpisodeKey, ":")
		m.ShowIDs = []string{parts[0]}
	}
	if v.TimesInput != "" {
		for _, s := range strings.Split(v.TimesInput, ",") {
			m.Times = append(m.Times, strings.TrimSpace(s))
		}
	}
	v.Message = m
	if se != nil || ee != nil {
		err = fmt.Errorf("%w: enter valid start and expiry dates", announcements.ErrInvalid)
	} else {
		err = s.validateAnnouncementDJ(r.Context(), m.DJID)
		if err == nil {
			err = s.d.Announcements.Save(r.Context(), m)
		}
	}
	if err != nil {
		v.Error = announcementError(err)
		if version == 0 {
			v.Message.ID = ""
		}
		s.renderAnnouncement(w, r, 422, &v)
		return
	}
	s.redirect(w, r, v.Base+"/"+id+"?notice=saved")
}
func (s *Server) validateAnnouncementDJ(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	_, err := s.d.DJs.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: choose an available presenter", announcements.ErrInvalid)
	}
	return nil
}
func announcementError(err error) string {
	if errors.Is(err, announcements.ErrInvalid) || errors.Is(err, announcements.ErrConflict) {
		return err.Error()
	}
	return "The announcement could not be saved. Your input is retained; please try again."
}
func (s *Server) announcementAction(w http.ResponseWriter, r *http.Request) {
	v, err := s.announcementView(r)
	if err != nil {
		s.announcementHTTPError(w, r, err)
		return
	}
	v.Mode = "form"
	v.Title = "Edit announcement"
	m, err := s.d.Announcements.Get(r.Context(), v.Station.ID, r.PathValue("message"))
	if err != nil {
		s.announcementHTTPError(w, r, err)
		return
	}
	v.Message = m
	version, e := strconv.Atoi(r.PostForm.Get("version"))
	if e != nil || version != m.Version {
		v.Error = announcements.ErrConflict.Error()
		s.renderAnnouncement(w, r, 422, &v)
		return
	}
	action := r.PostForm.Get("action")
	switch action {
	case "active", "paused":
		m.Status = action
		m.Next = false
		err = s.d.Announcements.Save(r.Context(), m)
	case "next":
		if !time.Now().Before(m.Expires) {
			err = fmt.Errorf("%w: extend this message's expiry before announcing it", announcements.ErrInvalid)
		} else {
			m.Next = true
			m.Status = "active"
			err = s.d.Announcements.Save(r.Context(), m)
		}
	case "preview":
		s.previewAnnouncement(w, r, &v)
		return
	default:
		http.Error(w, "unknown action", 400)
		return
	}
	if err != nil {
		v.Error = announcementError(err)
		s.renderAnnouncement(w, r, 422, &v)
		return
	}
	s.redirect(w, r, v.Base+"/"+m.ID+"?notice="+action)
}
func (s *Server) announcementSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.announcementView(r)
	if err != nil {
		s.announcementHTTPError(w, r, err)
		return
	}
	v.Mode = "settings"
	v.Title = "Announcement settings"
	gap, _ := strconv.Atoi(r.PostForm.Get("gap"))
	version, e := strconv.Atoi(r.PostForm.Get("version"))
	if e != nil {
		http.Error(w, "invalid version", 400)
		return
	}
	v.Settings = announcements.Settings{StationID: v.Station.ID, DJID: r.PostForm.Get("dj"), GapMinutes: gap, Version: version}
	err = s.validateAnnouncementDJ(r.Context(), v.Settings.DJID)
	if err == nil {
		err = s.d.Announcements.SaveSettings(r.Context(), v.Settings)
	}
	if err != nil {
		v.Error = announcementError(err)
		s.renderAnnouncement(w, r, 422, &v)
		return
	}
	s.redirect(w, r, v.Base+"?notice=settings")
}
func (s *Server) deleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	v, err := s.announcementView(r)
	if err != nil {
		s.announcementHTTPError(w, r, err)
		return
	}
	v.Mode = "delete"
	v.Title = "Delete announcement"
	v.Message, err = s.d.Announcements.Get(r.Context(), v.Station.ID, r.PathValue("message"))
	if err != nil {
		s.announcementHTTPError(w, r, err)
		return
	}
	if r.Method == "POST" {
		version, e := strconv.Atoi(r.PostForm.Get("version"))
		if e != nil || r.PostForm.Get("confirm") != "yes" {
			http.Error(w, "confirmation required", 400)
			return
		}
		err = s.d.Announcements.Delete(r.Context(), v.Station.ID, v.Message.ID, version)
		if err == nil {
			s.redirect(w, r, v.Base+"?notice=deleted")
			return
		}
		v.Error = announcementError(err)
	}
	if fragmentTarget(r, "dialog-body") {
		s.renderHTML(w, 200, "delete-announcement", v)
		return
	}
	s.renderAnnouncement(w, r, 200, &v)
}
func (s *Server) previewAnnouncement(w http.ResponseWriter, r *http.Request, v *announcementPageView) {
	if s.d.AnnouncementProducer == nil {
		v.Error = "Speech preview is unavailable."
		s.renderAnnouncement(w, r, 422, v)
		return
	}
	s.announcementPreviews.Lock()
	if s.announcementPreviews.Running >= 2 {
		s.announcementPreviews.Unlock()
		v.Error = "Two previews are already preparing. Try again shortly."
		s.renderAnnouncement(w, r, 422, v)
		return
	}
	s.announcementPreviews.Running++
	s.announcementPreviews.Unlock()
	defer func() {
		s.announcementPreviews.Lock()
		s.announcementPreviews.Running--
		s.announcementPreviews.Unlock()
	}()
	djID := v.Message.DJID
	// Use the current DJ when one exists, with the station default as fallback.
	if djID == "" && s.d.Broadcast != nil {
		djID = s.d.Broadcast.State(v.Station.ID).Programme.DJ.ID
	}
	if djID == "" {
		settings, err := s.d.Announcements.Settings(r.Context(), v.Station.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		djID = settings.DJID
	}
	if djID == "" {
		v.Error = "Choose a presenter override or set a default presenter in Announcement settings to preview this message."
		s.renderAnnouncement(w, r, 422, v)
		return
	}
	dj, err := s.d.DJs.Get(r.Context(), djID)
	if err != nil {
		s.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	ctx = production.WithSpeechTranscript(ctx, func(text string) { v.PreviewText = text })
	audio, err := s.d.AnnouncementProducer.Announcement(ctx, v.Message, dj)
	if err != nil || len(audio) == 0 || len(audio) > 8<<20 {
		v.Error = "Speech preview could not be prepared. Check the presenter's speech settings and try again."
		s.renderAnnouncement(w, r, 422, v)
		return
	}
	token := announcements.ID()
	s.announcementPreviews.Lock()
	if s.announcementPreviews.Items == nil {
		s.announcementPreviews.Items = map[string]announcementPreview{}
	}
	for key, p := range s.announcementPreviews.Items {
		if time.Now().After(p.Expires) {
			delete(s.announcementPreviews.Items, key)
		}
	}
	if len(s.announcementPreviews.Items) >= 16 {
		for key := range s.announcementPreviews.Items {
			delete(s.announcementPreviews.Items, key)
			break
		}
	}
	s.announcementPreviews.Items[token] = announcementPreview{Audio: audio, Expires: time.Now().Add(15 * time.Minute), StationID: v.Station.ID, Text: v.PreviewText, MessageID: v.Message.ID, Version: v.Message.Version}
	s.announcementPreviews.Unlock()
	s.redirect(w, r, v.Base+"/"+v.Message.ID+"?preview="+token)
}
func (s *Server) announcementPreviewAudio(w http.ResponseWriter, r *http.Request) {
	s.announcementPreviews.Lock()
	p, ok := s.announcementPreviews.Items[r.PathValue("token")]
	s.announcementPreviews.Unlock()
	if !ok || p.StationID != r.PathValue("id") || time.Now().After(p.Expires) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(p.Audio)
}

func (s *Server) announcementHTTPError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, announcements.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	s.publicError(w, r, err)
}
