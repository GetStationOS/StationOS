package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/sources"
	"strings"
	"time"
)

func (s *Server) sourcePage(w http.ResponseWriter, r *http.Request) {
	v := s.baseView(r)
	var err error
	v.Source, err = s.d.Sources.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, sources.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	v.Title = v.Source.Name
	v.SourceTab = "overview"
	if strings.HasSuffix(r.URL.Path, "/settings") {
		v.SourceTab = "settings"
	}
	endpoint, _ := url.Parse(v.Source.Endpoint)
	v.SourceHost = endpoint.Host
	if v.SourceTab == "overview" {
		v.Catalogue, err = s.d.Catalogue.Search(r.Context(), catalogue.Query{SourceID: v.Source.ID, Limit: 1})
		if err != nil {
			s.fail(w, err)
			return
		}
		v.Facets, err = s.d.Catalogue.Facets(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, album := range v.Facets.Albums {
			if album.SourceID == v.Source.ID {
				v.SourceAlbums++
			}
		}
	}
	s.render(w, r, v)
}

type discoveredSource struct {
	sources.DiscoveredSource
	Host, ExistingID string
}

func (s *Server) findSources(parent context.Context, v *view) {
	v.SourceDiscoveryAttempted = true
	if s.d.SourceDiscovery == nil {
		v.SourceDiscoveryError = "Automatic search is unavailable. Enter your music server’s address below."
		return
	}
	ctx, cancel := context.WithTimeout(parent, 11*time.Second)
	defer cancel()
	found, err := s.d.SourceDiscovery.Discover(ctx)
	if err != nil {
		v.SourceDiscoveryError = "Could not search the local network. Try again, or enter your music server’s address below."
		return
	}
	for _, server := range found {
		if len(v.SourceDiscoveries) >= 24 {
			break
		}
		endpoint, err := url.Parse(server.Endpoint)
		if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.User != nil {
			continue
		}
		row := discoveredSource{DiscoveredSource: server, Host: endpoint.Host}
		for _, saved := range v.Sources {
			if saved.Endpoint == server.Endpoint {
				row.ExistingID = saved.ID
				break
			}
		}
		v.SourceDiscoveries = append(v.SourceDiscoveries, row)
	}
}

func (s *Server) discoverSources(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, "/producer/sources/new?discover=1", http.StatusSeeOther)
		return
	}
	v := s.baseView(r)
	var err error
	v.Sources, err = s.d.Sources.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.findSources(r.Context(), &v)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.t.ExecuteTemplate(w, "source-discoveries", v); err != nil {
		s.d.Logger.Error("Source discovery render failed", "error", err)
	}
}

func (s *Server) loadSourceCards(ctx context.Context, v *view) error {
	var err error
	v.Sources, err = s.d.Sources.List(ctx)
	if err != nil {
		return err
	}
	for _, source := range v.Sources {
		items, err := s.d.Catalogue.Search(ctx, catalogue.Query{SourceID: source.ID, Limit: 1})
		if err != nil {
			return err
		}
		endpoint, _ := url.Parse(source.Endpoint)
		v.SourceCards = append(v.SourceCards, sourceCard{Source: source, Host: endpoint.Host, Tracks: items.Total})
	}
	return nil
}
