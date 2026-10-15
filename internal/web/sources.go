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
	v := sourcePageView{pageView: s.baseView(r)}
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
		facets, err := s.d.Catalogue.Facets(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, album := range facets.Albums {
			if album.SourceID == v.Source.ID {
				v.SourceAlbums++
			}
		}
	}
	s.renderPage(w, r, http.StatusOK, "source-page", v)
}

type discoveredSource struct {
	sources.DiscoveredSource
	Host, ExistingID string
}

func (s *Server) findSources(parent context.Context, v *sourcePageView) {
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
	v := sourcePageView{pageView: s.baseView(r)}
	var err error
	v.Sources, err = s.d.Sources.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.findSources(r.Context(), &v)
	s.renderHTML(w, http.StatusOK, "source-discoveries", v)
}

func (s *Server) loadSourceCards(ctx context.Context, v *sourcePageView) error {
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

func (s *Server) sourcesPage(w http.ResponseWriter, r *http.Request) {
	v := sourcePageView{pageView: s.baseView(r)}
	v.Title = "Sources"
	if err := s.loadSourceCards(r.Context(), &v); err != nil {
		s.fail(w, err)
		return
	}
	s.renderPage(w, r, http.StatusOK, "sources-page", v)
}
func (s *Server) newSourcePage(w http.ResponseWriter, r *http.Request) {
	v := sourcePageView{pageView: s.baseView(r)}
	v.Title = "Add source"
	v.Source.Name = r.URL.Query().Get("name")
	v.Source.Endpoint = r.URL.Query().Get("endpoint")
	if r.URL.Query().Get("discover") == "1" {
		var err error
		v.Sources, err = s.d.Sources.List(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		s.findSources(r.Context(), &v)
	}
	s.renderPage(w, r, http.StatusOK, "new-source-page", v)
}
