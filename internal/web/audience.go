package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/schedule"
)

func publicAudienceWrite(r *http.Request) bool {
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.Method != http.MethodPost || len(p) < 3 || p[0] != "radio" || p[1] == "" {
		return false
	}
	if len(p) == 3 {
		return p[2] == "requests" || p[2] == "shoutouts"
	}
	return len(p) == 5 && p[2] == "shows" && p[3] != "" && (p[4] == "requests" || p[4] == "shoutouts")
}
func (s *Server) listenerHash(purpose, id string) string {
	h := hmac.New(sha256.New, []byte(s.csrf))
	h.Write([]byte(purpose + ":" + id))
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Server) listenerToken(r *http.Request) string {
	c, err := r.Cookie("stationos_listener")
	if err != nil || len(c.Value) != 64 {
		return ""
	}
	if _, err = hex.DecodeString(c.Value); err != nil {
		return ""
	}
	return s.listenerHash("form", c.Value)
}
func (s *Server) listenerCookie(w http.ResponseWriter, r *http.Request) error {
	if s.listenerToken(r) != "" {
		return nil
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	c := &http.Cookie{Name: "stationos_listener", Value: hex.EncodeToString(b[:]), Path: "/radio/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil || strings.HasPrefix(s.d.PublicURL, "https://"), MaxAge: 86400}
	http.SetCookie(w, c)
	r.AddCookie(c)
	return nil
}
func (s *Server) audienceService() audience.Service {
	return audience.Service{Stations: s.d.Stations, Shows: s.d.Schedule, Pool: s.d.BroadcastConfig, Repo: s.d.Audience}
}
func audienceKind(r *http.Request) audience.Kind {
	if strings.HasSuffix(r.URL.Path, "/requests") {
		return audience.Request
	}
	return audience.Shoutout
}

// Full navigation and history restoration render the station; targeted HTMX
// requests render only the contribution step, leaving playback untouched.
func (s *Server) audienceForm(w http.ResponseWriter, r *http.Request) {
	if err := s.listenerCookie(w, r); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.audienceFormView(r)
	if err != nil {
		s.publicError(w, r, err)
		return
	}
	s.renderAudienceForm(w, r, http.StatusOK, v)
}
func (s *Server) audienceFormView(r *http.Request) (publicPageView, error) {
	if s.d.Audience == nil || s.d.Schedule == nil || s.d.BroadcastConfig == nil {
		return publicPageView{}, audience.ErrClosed
	}
	v, err := s.publicView(r)
	if err != nil {
		return v, err
	}
	v.AudienceMode = "choose"
	v.AudienceKind = string(audienceKind(r))
	v.AudienceToken = s.listenerToken(r)
	v.AudienceKey = r.URL.Query().Get("show")
	v.AudienceQuery = strings.TrimSpace(r.URL.Query().Get("q"))
	// A legacy direct form without a show first asks which show to contribute to.
	requests, err := s.audienceService().Opportunities(r.Context(), v.Station.ID, audience.Request)
	if err != nil {
		return v, err
	}
	shoutouts, err := s.audienceService().Opportunities(r.Context(), v.Station.ID, audience.Shoutout)
	if err != nil {
		return v, err
	}
	seen := map[string]bool{}
	for _, group := range [][]schedule.Occurrence{requests, shoutouts} {
		for _, show := range group {
			if !seen[show.Key] {
				v.AudienceShows = append(v.AudienceShows, show)
				seen[show.Key] = true
			}
			if show.Key == v.AudienceKey && (r.PathValue("show") == "" || show.ShowID == r.PathValue("show")) {
				v.AudienceShow = show
			}
		}
	}
	if v.AudienceKey == "" {
		return v, nil
	}
	if v.AudienceShow.Key == "" {
		v.AudienceMode = "closed"
		return v, nil
	}
	v.AudienceMode = "types"
	if strings.HasSuffix(r.URL.Path, "/contribute") {
		return v, nil
	}
	kind := audienceKind(r)
	if kind == audience.Request && !v.AudienceShow.AcceptRequests || kind == audience.Shoutout && !v.AudienceShow.AcceptShoutouts {
		v.AudienceMode = "closed"
		return v, nil
	}
	v.AudienceMode = "form"
	if r.URL.Query().Get("sent") == "yes" {
		v.AudienceMode = "thanks"
		return v, nil
	}
	if kind == audience.Request {
		pool, e := s.d.BroadcastConfig.Pool(r.Context(), v.AudienceShow.PlaylistID)
		if e != nil {
			return v, e
		}
		words := strings.Fields(strings.ToLower(v.AudienceQuery))
		if len(words) > 0 {
			for _, track := range pool {
				text := strings.ToLower(track.Title + " " + track.Artist + " " + track.AlbumArtist)
				matches := true
				for _, word := range words {
					if !strings.Contains(text, word) {
						matches = false
						break
					}
				}
				if !matches {
					continue
				}
				if len(v.AudienceTracks) == 8 {
					v.AudienceMoreTracks = true
					break
				}
				v.AudienceTracks = append(v.AudienceTracks, track)
			}
		}
	}
	return v, nil
}
func (s *Server) audienceFormError(w http.ResponseWriter, r *http.Request, status int, message string) {
	// Rebuild from the submitted occurrence rather than choosing another show.
	query := r.URL.Query()
	query.Set("show", r.PostForm.Get("show"))
	query.Set("day", r.PostForm.Get("day"))
	r.URL.RawQuery = query.Encode()
	v, err := s.audienceFormView(r)
	if err != nil {
		s.publicError(w, r, err)
		return
	}
	v.AudienceError = message
	v.AudienceName = r.PostForm.Get("name")
	v.AudienceText = r.PostForm.Get("message")
	v.AudienceTrack = r.PostForm.Get("track")
	v.AudienceConsent = r.PostForm.Get("consent") == "yes"
	// Preserve a selected recording even if it was outside the last search page.
	if v.AudienceTrack != "" && v.AudienceShow.Key != "" {
		pool, e := s.d.BroadcastConfig.Pool(r.Context(), v.AudienceShow.PlaylistID)
		if e != nil {
			s.fail(w, e)
			return
		}
		found := false
		for _, track := range v.AudienceTracks {
			if track.ID == v.AudienceTrack {
				found = true
			}
		}
		if !found {
			for _, track := range pool {
				if track.ID == v.AudienceTrack {
					v.AudienceTracks = append(v.AudienceTracks, track)
					break
				}
			}
		}
	}
	if r.Header.Get("HX-Request") == "true" {
		status = http.StatusOK
	}
	s.renderAudienceForm(w, r, status, v)
}
func (s *Server) submitAudience(w http.ResponseWriter, r *http.Request) {
	if s.d.Audience == nil {
		http.Error(w, "Audience participation unavailable", 503)
		return
	}
	st, err := s.publicStation(r)
	if err != nil {
		s.publicError(w, r, err)
		return
	}
	if id := r.PathValue("show"); id != "" {
		target, err := s.audienceService().Target(r.Context(), st.ID, r.PostForm.Get("show"), audienceKind(r))
		if err != nil || target.ShowID != id {
			s.audienceFormError(w, r, 422, audience.ErrClosed.Error())
			return
		}
	}
	if r.PostForm.Get("consent") != "yes" {
		s.audienceFormError(w, r, 422, "Please confirm that your name and message may be read on air.")
		return
	}
	if !s.allowAudienceAddress(r) {
		s.audienceFormError(w, r, 429, audience.ErrLimited.Error())
		return
	}
	c, err := r.Cookie("stationos_listener")
	if err != nil {
		http.Error(w, "Reload the form", 403)
		return
	}
	_, err = s.audienceService().Submit(r.Context(), st.ID, r.PostForm.Get("show"), s.listenerHash("sender", c.Value), r.PostForm.Get("name"), r.PostForm.Get("message"), r.PostForm.Get("track"), audienceKind(r))
	if err != nil {
		switch {
		case errors.Is(err, audience.ErrLimited):
			s.audienceFormError(w, r, 429, err.Error())
		case errors.Is(err, audience.ErrInvalid), errors.Is(err, audience.ErrClosed), errors.Is(err, audience.ErrDuplicate):
			s.audienceFormError(w, r, 422, err.Error())
		default:
			s.fail(w, err)
		}
		return
	}
	destination := r.URL.Path + "?sent=yes&show=" + url.QueryEscape(r.PostForm.Get("show")) + "&day=" + url.QueryEscape(r.PostForm.Get("day"))
	panel := "contribution-panel"
	if id := r.PathValue("show"); id != "" {
		panel = "contribution-" + id
	}
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == panel {
		location, _ := json.Marshal(map[string]any{"path": destination, "target": "#" + panel, "swap": "innerHTML show:none", "push": false})
		w.Header().Set("HX-Location", string(location))
		w.WriteHeader(http.StatusOK)
		return
	}
	s.redirect(w, r, destination)
}

type audienceStatusChoice struct {
	Value, Label string
	Selected     bool
}

func audienceFilters(query url.Values) ([]audienceStatusChoice, string, url.Values) {
	choices := []audienceStatusChoice{{Value: "pending", Label: "Awaiting AI review"}, {Value: "review", Label: "Needs producer review"}, {Value: "approved", Label: "Approved"}, {Value: "used", Label: "Broadcast"}, {Value: "rejected", Label: "Dismissed"}, {Value: "expired", Label: "Expired"}}
	selected := map[string]bool{}
	if !query.Has("status") && query.Get("filtered") != "1" {
		selected["pending"], selected["review"], selected["approved"] = true, true, true
	}
	for _, value := range query["status"] {
		if value == "active" {
			selected["pending"], selected["review"], selected["approved"] = true, true, true
		} else {
			selected[value] = true
		}
	}
	values := url.Values{"filtered": {"1"}}
	var statuses []string
	for i := range choices {
		choices[i].Selected = selected[choices[i].Value]
		if choices[i].Selected {
			statuses = append(statuses, choices[i].Value)
			values.Add("status", choices[i].Value)
		}
	}
	filter := strings.Join(statuses, ",")
	if filter == "" {
		filter = "none"
	}
	return choices, filter, values
}

func (s *Server) audiencePage(w http.ResponseWriter, r *http.Request) {
	if s.d.Audience == nil {
		http.Error(w, "Audience participation unavailable", 503)
		return
	}
	v := audienceQueueView{pageView: s.baseView(r)}
	var err error
	v.Station, err = s.d.Stations.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.publicError(w, r, err)
		return
	}
	v.StationID = v.Station.ID
	v.Section = "audience"
	v.Title = "Audience"
	if s.d.Broadcast != nil {
		v.Broadcast = s.d.Broadcast.State(v.Station.ID)
	}
	choices, status, filters := audienceFilters(r.URL.Query())
	v.AudienceStatuses = choices
	v.AudienceFilter = status
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	offset = max(0, offset)

	snapshotTime := time.Now()
	v.AudienceMessages, v.AudienceTotal, err = s.d.Audience.List(r.Context(), v.Station.ID, status, offset, 50, snapshotTime)
	if err != nil {
		s.fail(w, err)
		return
	}
	base := r.URL.Path + "?" + filters.Encode() + "&offset="
	v.AudienceRefreshURL = base + strconv.Itoa(offset)
	if expiry, ok := s.d.Audience.(interface {
		NextExpiry(context.Context, string, time.Time) (time.Time, error)
	}); ok {
		at, err := expiry.NextExpiry(r.Context(), v.Station.ID, snapshotTime)
		if err != nil {
			s.fail(w, err)
			return
		}
		if !at.IsZero() {
			w.Header().Set("X-Live-Deadline", at.Format(time.RFC3339Nano))
		}
	}
	if offset > 0 {
		v.AudiencePrevious = base + strconv.Itoa(max(0, offset-50))
	}
	if offset+50 < v.AudienceTotal {
		v.AudienceNext = base + strconv.Itoa(offset+50)
	}
	if fragmentTarget(r, "audience-queue-results") {
		s.renderHTML(w, http.StatusOK, "audience-queue-results", v)
		return
	}
	s.renderPage(w, r, http.StatusOK, "audience-queue-page", v)
}
func (s *Server) decideAudience(w http.ResponseWriter, r *http.Request) {
	if s.d.Audience == nil {
		http.Error(w, "Audience participation unavailable", 503)
		return
	}
	status := audience.Rejected
	if r.PostForm.Get("action") == "approve" {
		status = audience.Approved
	} else if r.PostForm.Get("action") != "dismiss" {
		http.Error(w, "Choose approve or dismiss", 400)
		return
	}
	err := s.d.Audience.Decide(r.Context(), r.PathValue("id"), r.PathValue("message"), status, "Producer decision", time.Now())
	if err != nil {
		if errors.Is(err, audience.ErrConflict) {
			http.Error(w, err.Error(), 409)
		} else {
			s.fail(w, err)
		}
		return
	}
	_, _, filters := audienceFilters(r.PostForm)
	destination := "/producer/stations/" + url.PathEscape(r.PathValue("id")) + "/audience?" + filters.Encode()
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "audience-queue-results" {
		location, _ := json.Marshal(map[string]any{"path": destination, "target": "#audience-queue-results", "swap": "outerHTML show:none", "push": false})
		w.Header().Set("HX-Location", string(location))
		w.WriteHeader(http.StatusOK)
		return
	}
	s.redirect(w, r, destination)
}

// Bound anonymous traffic without retaining addresses or trusting proxy headers.
// The wider address limit accommodates shared households; the durable cookie
// limit is stricter. Old buckets are discarded after ten minutes.
type audienceRateBucket struct {
	start time.Time
	count int
}
type audienceRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]audienceRateBucket
}

func (s *Server) allowAudienceAddress(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	key := s.listenerHash("address", host)
	now := time.Now()
	rates := &s.audienceRates
	rates.mu.Lock()
	defer rates.mu.Unlock()
	if rates.buckets == nil {
		rates.buckets = map[string]audienceRateBucket{}
	}
	for id, bucket := range rates.buckets {
		if now.Sub(bucket.start) >= 10*time.Minute {
			delete(rates.buckets, id)
		}
	}
	bucket, exists := rates.buckets[key]
	if !exists {
		if len(rates.buckets) >= 4096 {
			return false
		}
		bucket.start = now
	}
	if bucket.count >= 60 {
		return false
	}
	bucket.count++
	rates.buckets[key] = bucket
	return true
}
