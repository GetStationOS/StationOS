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
	"net/http"
	"net/url"
	"stationos.local/stationos/internal/broadcast"
	"strings"

	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/mixer"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Dependencies struct {
	Mixer           mixer.Repository
	ArtworkFactory  sources.Factory
	Schedule        schedule.Repository
	DJs             djs.Repository
	Broadcast       *broadcast.Manager
	BroadcastConfig broadcast.Repository
	PublicURL       string
	Stations        station.Repository
	Sources         sources.Repository
	Catalogue       catalogue.Repository
	Playlists       playlists.Repository
	Sync            catalogue.Sync
	Ready           func(context.Context) error
	Logger          *slog.Logger
	Password        string
}
type Server struct {
	d            Dependencies
	t            *template.Template
	csrf         string
	artworkSlots chan struct{}
	eventSlots   chan struct{}
}

func New(d Dependencies) (http.Handler, error) {
	t, err := template.New("pages").Funcs(template.FuncMap{"timeLabel": schedule.TimeLabel, "indexDay": func(day int) string { return weekDays[day].Name }}).ParseFS(assets, "templates/*.html")
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
	s := &Server{d: d, t: t, csrf: hex.EncodeToString(b[:]), artworkSlots: make(chan struct{}, 4), eventSlots: make(chan struct{}, 64)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { s.json(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", s.ready)
	mux.Handle("GET /static/", http.FileServerFS(assets))
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /station", s.page)
	mux.HandleFunc("GET /stations/new", s.page)
	mux.HandleFunc("GET /stations/{id}", s.stationPage)
	mux.HandleFunc("GET /stations/live", s.liveCards)
	mux.HandleFunc("GET /stations/events", s.stationEvents)
	mux.HandleFunc("GET /stations/{id}/player", s.stationEvents)
	mux.HandleFunc("GET /streams/{id}", s.stream)
	mux.HandleFunc("GET /stations/{id}/artwork/{item}", s.stationArtwork)
	mux.HandleFunc("GET /stations/{id}/live", s.liveStation)
	mux.HandleFunc("POST /stations/{id}/broadcast", s.assignBroadcast)
	mux.HandleFunc("GET /stations/{id}/mixer", s.mixerPage)
	mux.HandleFunc("POST /stations/{id}/mixer", s.saveMixer)
	mux.HandleFunc("GET /stations/{id}/settings", s.stationPage)
	mux.HandleFunc("GET /stations/{id}/delete", s.stationPage)
	mux.HandleFunc("POST /stations/{id}/delete", s.deleteStation)
	mux.HandleFunc("GET /stations/{id}/schedule", s.schedulePage)
	mux.HandleFunc("GET /stations/{id}/schedule/new", s.schedulePage)
	mux.HandleFunc("GET /stations/{id}/schedule/{show}", s.schedulePage)
	mux.HandleFunc("GET /stations/{id}/schedule/{show}/delete", s.schedulePage)
	mux.HandleFunc("POST /stations/{id}/schedule", s.saveShow)
	mux.HandleFunc("POST /stations/{id}/schedule/{show}/delete", s.deleteShow)
	mux.HandleFunc("GET /djs", s.djPage)
	mux.HandleFunc("GET /djs/new", s.djPage)
	mux.HandleFunc("GET /djs/{id}", s.djPage)
	mux.HandleFunc("GET /djs/{id}/delete", s.djPage)
	mux.HandleFunc("POST /djs", s.saveDJ)
	mux.HandleFunc("POST /djs/{id}/delete", s.deleteDJ)
	mux.HandleFunc("GET /sources", s.page)
	mux.HandleFunc("GET /sources/new", s.page)
	mux.HandleFunc("GET /sources/{id}", s.sourcePage)
	mux.HandleFunc("GET /sources/{id}/settings", s.sourcePage)
	mux.HandleFunc("GET /content", s.page)
	mux.HandleFunc("GET /api/v1/status", s.status)
	mux.HandleFunc("GET /playlists", s.playlistPage)
	mux.HandleFunc("GET /playlists/new", s.playlistPage)
	mux.HandleFunc("GET /playlists/{id}", s.playlistPage)
	mux.HandleFunc("GET /playlists/{id}/tracks", s.playlistPage)
	mux.HandleFunc("GET /playlists/{id}/settings", s.playlistPage)
	mux.HandleFunc("GET /playlists/{id}/delete", s.playlistPage)
	mux.HandleFunc("POST /playlists/{id}/delete", s.deletePlaylist)
	mux.HandleFunc("POST /playlists", s.savePlaylist)
	mux.HandleFunc("POST /playlists/select", s.selectItems)
	mux.HandleFunc("POST /playlists/{id}/rules", s.addRule)
	mux.HandleFunc("POST /playlists/{id}/selections", s.replaceRules)
	mux.HandleFunc("POST /playlists/{id}/rules/{rule}/remove", s.removeRule)
	mux.HandleFunc("POST /playlists/{id}/remove", s.removeItem)
	mux.HandleFunc("POST /station", s.saveStation)
	mux.HandleFunc("POST /sources", s.saveSource)
	mux.HandleFunc("POST /sources/{id}/sync", s.syncSource)
	return s.protect(mux), nil
}
func (s *Server) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		// Native form POSTs can send Origin: null under no-referrer, even to this server.
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		publicStream := (r.Method == http.MethodGet || r.Method == http.MethodHead) && len(r.URL.Path) > len("/streams/") && strings.HasPrefix(r.URL.Path, "/streams/") && !strings.Contains(strings.TrimPrefix(r.URL.Path, "/streams/"), "/")
		if s.d.Password != "" && r.URL.Path != "/healthz" && r.URL.Path != "/readyz" && !publicStream {
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
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid or oversized form", 400)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(s.csrf)) != 1 {
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
	if err := v.Validate(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	saved, err := (station.Service{Repo: s.d.Stations}).Save(r.Context(), v)
	if errors.Is(err, station.ErrSlugTaken) {
		http.Error(w, err.Error(), 422)
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
	to := "/stations/" + saved.ID
	if v.ID != "" {
		to += "/settings"
	}
	s.redirect(w, r, to+"?notice=station-saved")
}
func (s *Server) saveSource(w http.ResponseWriter, r *http.Request) {
	c := sources.Config{ID: r.PostForm.Get("id"), Name: r.PostForm.Get("name"), Kind: "upnp", Endpoint: r.PostForm.Get("endpoint")}
	if err := c.Validate(); err != nil {
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
	to := "/sources/" + saved.ID
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
	to := "/content"
	if r.PostForm.Get("return_to") == "/sources/"+r.PathValue("id") {
		to = r.PostForm.Get("return_to")
	}
	s.redirect(w, r, to+"?notice=imported")
}
