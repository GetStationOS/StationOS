// Package web is the HTTP edge: validation, rendering and application-service calls.
package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/changefeed"
	"stationos.local/stationos/internal/prompts"
	"strconv"
	"strings"

	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/mixer"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

//go:embed templates/*.html static/*
var assets embed.FS

type ImageGenerator interface {
	producer.ImageGenerator
	Available(context.Context) (bool, error)
}

type Dependencies struct {
	Changes         *changefeed.Bus
	Prompts         prompts.Store
	Operations      operations.Store
	Recording       *operations.Recorder
	Images          ImageGenerator
	Assistant       *producer.Service
	Audience        audience.Repository
	Mixer           mixer.Repository
	ArtworkFactory  sources.Factory
	Schedule        schedule.Repository
	DJs             djs.Repository
	Broadcast       *broadcast.Manager
	BroadcastConfig broadcast.Repository
	PublicURL       string
	Stations        station.Repository
	SourceDiscovery sources.Discoverer
	Sources         sources.Repository
	Catalogue       catalogue.Repository
	Playlists       playlists.Repository
	Sync            catalogue.Sync
	Ready           func(context.Context) error
	Logger          *slog.Logger
	Password        string
}
type Server struct {
	audienceRates audienceRateLimiter
	d             Dependencies
	t             *template.Template
	csrf          string
	artworkSlots  chan struct{}
	imageSlots    chan struct{}
	eventSlots    chan struct{}
}

func templateFunctions() template.FuncMap {
	return template.FuncMap{"audienceStateIcon": audienceStateIcon, "historyStateIcon": historyStateIcon, "incidentStateIcon": incidentStateIcon, "heading": heading, "headingLink": headingLink, "scheduleHeading": scheduleHeading, "playlistsHeading": playlistsHeading, "playlistHeading": playlistHeading, "assistantHeading": assistantHeading, "crumb": crumb, "breadcrumbs": breadcrumbs, "stationBreadcrumbs": stationBreadcrumbs, "sourceBreadcrumbs": sourceBreadcrumbs, "publicBreadcrumbs": publicBreadcrumbs, "stationTabs": stationTabs, "sourceTabs": sourceTabs, "playlistTabs": playlistTabs, "assistantTabs": assistantTabs, "formActions": formActions, "operationTypeLabel": operationTypeLabel, "operationOutcomeLabel": operationOutcomeLabel, "operationKinds": func() []string {
		return []string{"music", "speech", "started", "stopped", "handover", "ai", "failure", "recovery", "stall", "listener", "intervention", "unavailable"}
	}, "operationOutcomes": func() []string {
		return []string{"transmitting", "completed", "interrupted", "failed", "unknown", "observed"}
	}, "assetURL": assetURL, "stationPhotoEditor": stationPhotoEditor, "djPhotoEditor": djPhotoEditor, "djIndicators": djIndicators, "presenterStatus": presenterStatus, "producerLogo": producerLogo, "stationTrack": stationTrack, "currentBroadcastTrack": currentBroadcastTrack, "timeLabel": schedule.TimeLabel, "indexDay": func(day int) string { return weekDays[day].Name }}
}

func New(d Dependencies) (http.Handler, error) {
	t, err := template.New("pages").Funcs(templateFunctions()).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	var b [32]byte
	if _, err = rand.Read(b[:]); err != nil {
		return nil, err
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	s := &Server{d: d, t: t, csrf: hex.EncodeToString(b[:]), artworkSlots: make(chan struct{}, 4), imageSlots: make(chan struct{}, 1), eventSlots: make(chan struct{}, 64)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { s.json(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", s.ready)
	mux.Handle("GET /static/", http.FileServerFS(assets))
	mux.HandleFunc("GET /producer", s.page)
	mux.HandleFunc("GET /producer/assistant/prompts", s.promptsPage)
	mux.HandleFunc("GET /producer/assistant/prompts/{id}/edit", s.promptPage)
	mux.HandleFunc("POST /producer/assistant/prompts/{id}/edit", s.promptPage)
	mux.HandleFunc("GET /producer/assistant", s.assistantPage)
	mux.HandleFunc("GET /producer/assistant/providers/{id}/delete", s.deleteAIPage)
	mux.HandleFunc("GET /producer/assistant/providers/{id}/models", s.discoverModels)
	mux.HandleFunc("GET /producer/assistant/endpoints", s.assistantSettings)
	mux.HandleFunc("POST /producer/assistant/endpoints", s.assistantSettings)
	mux.HandleFunc("GET /producer/assistant/settings", s.assistantSettings)
	mux.HandleFunc("POST /producer/assistant/settings", s.assistantSettings)
	mux.HandleFunc("POST /producer/assistant/conversations", s.newConversation)
	mux.HandleFunc("POST /producer/assistant/messages", s.assistantSend)
	mux.HandleFunc("GET /producer/assistant/{id}/messages", s.subscription("assistant", "assistant-messages", s.assistantMessages))
	mux.HandleFunc("POST /producer/assistant/{id}/stop", s.assistantStop)
	mux.HandleFunc("POST /producer/assistant/{id}/continue", s.assistantContinue)
	mux.HandleFunc("GET /producer/{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/producer", 308) })
	mux.HandleFunc("GET /{$}", s.publicPage)
	mux.HandleFunc("GET /radio-events", s.publicDirectoryEvents)
	mux.HandleFunc("GET /radio/{slug}", s.publicPage)
	mux.HandleFunc("GET /radio/{slug}/shows/{show}", s.publicPage)
	mux.HandleFunc("GET /radio/{slug}/shows/{show}/contribute", s.audienceForm)
	mux.HandleFunc("GET /radio/{slug}/shows/{show}/requests", s.audienceForm)
	mux.HandleFunc("POST /radio/{slug}/shows/{show}/requests", s.submitAudience)
	mux.HandleFunc("GET /radio/{slug}/shows/{show}/shoutouts", s.audienceForm)
	mux.HandleFunc("POST /radio/{slug}/shows/{show}/shoutouts", s.submitAudience)
	mux.HandleFunc("GET /radio/{slug}/contribute", s.audienceForm)
	mux.HandleFunc("GET /radio/{slug}/requests", s.audienceForm)
	mux.HandleFunc("POST /radio/{slug}/requests", s.submitAudience)
	mux.HandleFunc("GET /radio/{slug}/shoutouts", s.audienceForm)
	mux.HandleFunc("POST /radio/{slug}/shoutouts", s.submitAudience)
	mux.HandleFunc("GET /producer/stations/{id}/audience", s.subscription("audience", "audience-queue-results", s.audiencePage))
	mux.HandleFunc("POST /producer/stations/{id}/audience/{message}", s.decideAudience)
	mux.HandleFunc("GET /radio/{slug}/live", s.publicLive)
	mux.HandleFunc("GET /radio/{slug}/player", s.publicPlayerEvents)
	mux.HandleFunc("GET /radio/{slug}/artwork/{item}", s.publicArtwork)
	mux.HandleFunc("GET /radio/{slug}/presenters/{id}/photo", s.publicDJPhoto)
	mux.HandleFunc("GET /djs/{id}/photo", s.djPhoto)
	for _, path := range []string{"/stations", "/station", "/sources", "/djs", "/content", "/playlists", "/api"} {
		redirect := func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/producer"+r.URL.RequestURI(), http.StatusTemporaryRedirect)
		}
		mux.HandleFunc(path, redirect)
		mux.HandleFunc(path+"/", redirect)
	}
	mux.HandleFunc("GET /producer/station", s.page)
	mux.HandleFunc("GET /producer/stations/new", s.page)
	mux.HandleFunc("GET /producer/stations/{id}", s.stationPage)
	mux.HandleFunc("GET /producer/stations/{id}/timeline", s.operationsPage)
	mux.HandleFunc("GET /producer/stations/{id}/upcoming", s.upcomingOverview)
	mux.HandleFunc("GET /producer/stations/{id}/incidents", s.operationsPage)
	mux.HandleFunc("GET /producer/stations/{id}/history.csv", s.operationsPage)
	mux.HandleFunc("GET /producer/stations/{id}/operations/version", s.subscription("operations", "operations-version", s.operationsVersion))
	mux.HandleFunc("GET /producer/stations/{id}/operations/indicator", s.subscription("operations", "incident-menu-indicator", s.incidentIndicator))
	mux.HandleFunc("POST /producer/stations/{id}/incidents/{incident}/acknowledge", s.acknowledgeIncident)
	mux.HandleFunc("GET /producer/stations/live", s.liveCards)
	mux.HandleFunc("GET /producer/stations/events", s.stationEvents)
	mux.HandleFunc("GET /producer/stations/{id}/player", s.stationEvents)
	mux.HandleFunc("GET /streams/{id}", s.stream)
	mux.HandleFunc("GET /producer/streams/{id}", s.stream)
	mux.HandleFunc("GET /producer/station-logos/{image}", s.stationLogo)
	mux.HandleFunc("GET /station-logos/{image}", s.stationLogo)
	mux.HandleFunc("GET /producer/stations/{id}/artwork/{item}", s.stationArtwork)
	mux.HandleFunc("GET /producer/stations/{id}/live", s.subscription("operations", "station-live", s.liveStation))
	mux.HandleFunc("POST /producer/stations/{id}/broadcast", s.assignBroadcast)
	mux.HandleFunc("GET /producer/stations/{id}/mixer", s.mixerPage)
	mux.HandleFunc("POST /producer/stations/{id}/mixer", s.saveMixer)
	mux.HandleFunc("GET /producer/stations/{id}/settings", s.stationPage)
	mux.HandleFunc("GET /producer/stations/{id}/delete", s.stationPage)
	mux.HandleFunc("POST /producer/stations/{id}/delete", s.deleteStation)
	mux.HandleFunc("GET /producer/stations/{id}/schedule", s.schedulePage)
	mux.HandleFunc("GET /producer/stations/{id}/schedule/new", s.schedulePage)
	mux.HandleFunc("GET /producer/stations/{id}/schedule/{show}", s.schedulePage)
	mux.HandleFunc("GET /producer/stations/{id}/schedule/{show}/delete", s.schedulePage)
	mux.HandleFunc("POST /producer/stations/{id}/schedule", s.saveShow)
	mux.HandleFunc("POST /producer/stations/{id}/schedule/{show}/delete", s.deleteShow)
	mux.HandleFunc("GET /producer/djs", s.djPage)
	mux.HandleFunc("GET /producer/djs/new", s.djPage)
	mux.HandleFunc("GET /producer/djs/{id}", s.djPage)
	mux.HandleFunc("GET /producer/djs/{id}/photo", s.djPhoto)
	mux.HandleFunc("GET /producer/djs/{id}/delete", s.djPage)
	mux.HandleFunc("POST /producer/djs", s.saveDJ)
	mux.HandleFunc("POST /producer/djs/{id}/delete", s.deleteDJ)
	mux.HandleFunc("GET /producer/sources", s.page)
	mux.HandleFunc("GET /producer/sources/new", s.page)
	mux.HandleFunc("GET /producer/sources/discover", s.discoverSources)
	mux.HandleFunc("GET /producer/sources/{id}", s.sourcePage)
	mux.HandleFunc("GET /producer/sources/{id}/settings", s.sourcePage)
	mux.HandleFunc("GET /producer/content", s.page)
	mux.HandleFunc("GET /producer/api/v1/status", s.status)
	mux.HandleFunc("GET /producer/playlists", s.playlistPage)
	mux.HandleFunc("GET /producer/playlists/new", s.playlistPage)
	mux.HandleFunc("GET /producer/playlists/{id}", s.playlistPage)
	mux.HandleFunc("GET /producer/playlists/{id}/tracks", s.playlistPage)
	mux.HandleFunc("GET /producer/playlists/{id}/settings", s.playlistPage)
	mux.HandleFunc("GET /producer/playlists/{id}/delete", s.playlistPage)
	mux.HandleFunc("POST /producer/playlists/{id}/delete", s.deletePlaylist)
	mux.HandleFunc("POST /producer/playlists", s.savePlaylist)
	mux.HandleFunc("POST /producer/playlists/{id}/default", s.defaultPlaylist)
	mux.HandleFunc("POST /producer/playlists/select", s.selectItems)
	mux.HandleFunc("POST /producer/playlists/{id}/rules", s.addRule)
	mux.HandleFunc("POST /producer/playlists/{id}/selections", s.replaceRules)
	mux.HandleFunc("POST /producer/playlists/{id}/rules/{rule}/remove", s.removeRule)
	mux.HandleFunc("POST /producer/playlists/{id}/remove", s.removeItem)
	mux.HandleFunc("POST /producer/playlists/{id}/restore", s.restoreItem)
	mux.HandleFunc("POST /producer/playlists/{id}/refresh", s.refreshPlaylist)
	mux.HandleFunc("POST /producer/station", s.saveStation)
	mux.HandleFunc("POST /producer/sources", s.saveSource)
	mux.HandleFunc("POST /producer/sources/{id}/sync", s.syncSource)
	return s.protect(mux), nil
}
func (s *Server) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		// Native form POSTs can send Origin: null under no-referrer, even to this server.
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' https: blob: data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		publicStream := (r.Method == http.MethodGet || r.Method == http.MethodHead) && len(r.URL.Path) > len("/streams/") && strings.HasPrefix(r.URL.Path, "/streams/") && !strings.Contains(strings.TrimPrefix(r.URL.Path, "/streams/"), "/")
		publicLogo := (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasPrefix(r.URL.Path, "/station-logos/") && !strings.Contains(strings.TrimPrefix(r.URL.Path, "/station-logos/"), "/")
		if s.d.Password != "" && r.URL.Path != "/healthz" && r.URL.Path != "/readyz" && !publicStream && !publicLogo && !publicRead(r) && !publicAudienceWrite(r) {
			user, pass, ok := r.BasicAuth()
			want := sha256.Sum256([]byte(s.d.Password))
			got := sha256.Sum256([]byte(pass))
			if !ok || user != "admin" || subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="StationOS"`)
				http.Error(w, "authentication required", 401)
				return
			}
		}
		if r.Method == http.MethodPost {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				scheme := "http"
				if r.TLS != nil {
					scheme = "https"
				}
				validOrigin := err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
				// TLS may terminate at the proxy. Only the explicitly configured
				// public origin is trusted; forwarded headers cannot grant access.
				allowedOrigin := validOrigin && ((u.Scheme == scheme && u.Host == r.Host) || origin == strings.TrimSuffix(s.d.PublicURL, "/"))
				if !allowedOrigin {
					http.Error(w, "cross-origin write rejected", 403)
					return
				}
			}
			kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			var formErr error
			if (r.URL.Path == "/producer/djs" || r.URL.Path == "/producer/station") && kind == "multipart/form-data" {
				r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
				formErr = r.ParseMultipartForm(6 << 20)
				if r.MultipartForm != nil {
					defer r.MultipartForm.RemoveAll()
				}
			} else {
				limit := int64(1 << 20)
				if publicAudienceWrite(r) {
					limit = 8 << 10
				}
				r.Body = http.MaxBytesReader(w, r.Body, limit)
				formErr = r.ParseForm()
			}
			if formErr != nil {
				http.Error(w, "The form is too large or unreadable. Choose a photo smaller than 5 MB.", 400)
				return
			}

			token := s.csrf
			if publicAudienceWrite(r) {
				token = s.listenerToken(r)
			}
			if token == "" || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(token)) != 1 {
				http.Error(w, "invalid form token; reload the page", 403)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) json(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.d.Logger.Error("response write failed", "error", err)
	}
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.d.Ready(r.Context()); err != nil {
		s.json(w, 503, map[string]string{"status": "unavailable"})
		return
	}
	s.json(w, 200, map[string]string{"status": "ready"})
}
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if err := s.d.Ready(r.Context()); err != nil {
		s.json(w, 503, map[string]any{"error": map[string]string{"code": "unavailable", "message": "database unavailable"}})
		return
	}
	items, err := s.d.Catalogue.Search(r.Context(), catalogue.Query{Limit: 1})
	if err != nil {
		s.apiError(w, err)
		return
	}
	playout := "unavailable"
	if s.d.Broadcast != nil {
		playout = "listener_driven"
	}
	s.json(w, 200, map[string]any{"status": "ready", "playout": playout, "catalogue_items": items.Total})
}
func (s *Server) apiError(w http.ResponseWriter, err error) {
	s.d.Logger.Error("HTTP API failure", "error", err)
	s.json(w, 500, map[string]any{"error": map[string]string{"code": "internal_error", "message": "request could not be completed"}})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.d.Logger.Error("HTTP request failed", "error", err)
	http.Error(w, "request could not be completed; check server logs", 500)
}
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Method == http.MethodPost && s.d.Broadcast != nil {
		s.d.Broadcast.Changed()
	}
	if r.Header.Get("HX-Request") == "true" {
		location, _ := json.Marshal(map[string]string{"path": to, "target": "#main", "swap": "innerHTML show:none"})
		w.Header().Set("HX-Location", string(location))
		w.WriteHeader(204)
		return
	}
	http.Redirect(w, r, to, 303)
}
func (s *Server) saveStation(w http.ResponseWriter, r *http.Request) {
	v := station.Station{ID: r.PostForm.Get("id"), Name: r.PostForm.Get("name"), Description: r.PostForm.Get("description"), Timezone: r.PostForm.Get("timezone"), Locale: r.PostForm.Get("locale"), StreamSlug: r.PostForm.Get("stream_slug")}
	if v.ID != "" {
		existing, err := s.d.Stations.Get(r.Context(), v.ID)
		if errors.Is(err, station.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		v.LogoVersion = existing.LogoVersion
		v.RepeatMinutes = existing.RepeatMinutes
		v.Visibility = existing.Visibility
	}
	if input, supplied := r.PostForm["repeat_minutes"]; supplied {
		minutes, err := strconv.Atoi(input[0])
		if err != nil || minutes < 1 || minutes > station.MaxRepeatMinutes {
			s.stationFormError(w, r, v, errors.New("minimum time between plays must be between 1 and 10080 minutes"))
			return
		}
		v.RepeatMinutes = minutes
	}
	if r.PostForm.Has("visibility") {
		v.Visibility = station.Visibility(r.PostForm.Get("visibility"))
		if v.Visibility == "" {
			s.stationFormError(w, r, v, errors.New("choose listed, unlisted or private visibility"))
			return
		}
	}
	change, err := s.readUploadedImage(r, "logo")
	if change != nil {
		v.Logo = &station.LogoChange{Data: change.Data, Clear: change.Clear}
	}
	if err == nil && change == nil && r.PostForm.Get("generated_logo") != "" {
		var data []byte
		data, err = decodeImageDraft(r.PostForm.Get("generated_logo"))
		if err == nil {
			v.Logo = &station.LogoChange{Data: data}
		}
	}
	if err == nil {
		err = v.Validate()
	}
	if err != nil {
		s.stationFormError(w, r, v, err)
		return
	}
	if r.PostForm.Get("generate_logo") == "yes" {
		s.generateStationLogo(w, r, v)
		return
	}
	saved, err := (station.Service{Repo: s.d.Stations}).Save(r.Context(), v)
	if errors.Is(err, station.ErrSlugTaken) {
		s.stationFormError(w, r, v, err)
		return
	}
	if errors.Is(err, station.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if saved.Publication() == station.Private && s.d.Broadcast != nil {
		s.d.Broadcast.Stop(saved.ID)
	}
	to := "/producer/stations/" + saved.ID
	if v.ID != "" {
		to += "/settings"
	}
	s.redirect(w, r, to+"?notice=station-saved")
}
func (s *Server) saveSource(w http.ResponseWriter, r *http.Request) {
	c := sources.Config{ID: r.PostForm.Get("id"), Name: r.PostForm.Get("name"), Kind: "upnp", Endpoint: r.PostForm.Get("endpoint")}
	if err := c.Validate(); err != nil {
		if c.ID == "" {
			v := sourcePageView{pageView: s.baseView(r)}
			v.Page, v.Title, v.Source, v.SourceError = "/producer/sources/new", "Add source", c, err.Error()
			s.renderPage(w, r, http.StatusOK, "new-source-page", v)
			return
		}
		http.Error(w, err.Error(), 400)
		return
	}
	saved, err := (sources.Service{Repo: s.d.Sources}).Save(r.Context(), c)
	if errors.Is(err, sources.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, sources.ErrConflict) {
		http.Error(w, "source changed; reload and retry", 409)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	to := "/producer/sources/" + saved.ID
	if c.ID != "" {
		to += "/settings"
	}
	s.redirect(w, r, to+"?notice=source-saved")
}
func (s *Server) syncSource(w http.ResponseWriter, r *http.Request) {
	_, err := s.d.Sync.Run(r.Context(), r.PathValue("id"))
	if errors.Is(err, sources.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "source import failed; existing catalogue preserved; check server logs", 502)
		return
	}
	to := "/producer/content"
	if r.PostForm.Get("return_to") == "/producer/sources/"+r.PathValue("id") {
		to = r.PostForm.Get("return_to")
	}
	s.redirect(w, r, to+"?notice=imported")
}
