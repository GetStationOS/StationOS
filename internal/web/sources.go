package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/sources"
	"strconv"
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
		page, _, e := pageRequest(r.URL.Query())
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		var albums []catalogue.Album
		for _, album := range facets.Albums {
			if album.SourceID == v.Source.ID {
				v.SourceAlbums++
				albums = append(albums, album)
			}
		}

		v.Pagination = paging(r.URL.Path, nil, page, 12, len(albums))
		v.Pagination.Sizes = nil
		v.Pagination.CountLabel = "album"
		v.Pagination.Target = "#source-album-results"
		for _, album := range albums[(v.Pagination.Number-1)*12 : min(v.Pagination.Number*12, len(albums))] {
			result, e := s.d.Catalogue.Search(r.Context(), catalogue.Query{SourceID: v.Source.ID, Album: album.Name, Limit: 1})
			if e != nil {
				s.fail(w, e)
				return
			}
			card := sourceAlbumCard{Name: album.Name, Tracks: album.Tracks, URL: "/producer/sources/" + v.Source.ID + "/album?" + url.Values{"album": {album.Name}, "browse_page": {strconv.Itoa(v.Pagination.Number)}}.Encode()}
			if len(result.Items) > 0 {
				card.Item = result.Items[0]
				card.Artwork = sourceCoverURL(v.Source.ID, card.Item)
			}
			v.AlbumCards = append(v.AlbumCards, card)
		}
	}
	if fragmentTarget(r, "source-album-results") {
		s.renderHTML(w, 200, "source-album-results", v)
		return
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

func (s *Server) deleteSourcePage(w http.ResponseWriter, r *http.Request) {
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
	v.Title = "Remove source"
	if fragmentTarget(r, "dialog-body") {
		s.renderHTML(w, http.StatusOK, "delete-source", v)
		return
	}
	s.renderPage(w, r, http.StatusOK, "delete-source-page", v)
}

func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request) {
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "confirm source removal", http.StatusBadRequest)
		return
	}
	err := s.d.Sources.Delete(r.Context(), r.PathValue("id"))
	if errors.Is(err, sources.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.redirect(w, r, "/producer/sources?notice=source-deleted")
}

type sourceAlbumCard struct {
	Name, URL, Artwork string
	Tracks             int
	Item               content.AudioItem
}

func sourceCoverURL(source string, item content.AudioItem) string {
	if item.Artwork == "" {
		return ""
	}
	return "/producer/sources/" + source + "/artwork/" + item.ID
}
func (s *Server) sourceAlbumPage(w http.ResponseWriter, r *http.Request) {
	v := sourcePageView{pageView: s.baseView(r), SourceTab: "overview", AlbumName: r.URL.Query().Get("album")}
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
	if v.AlbumName == "" || len(v.AlbumName) > 4096 {
		http.Error(w, "Choose an album", 400)
		return
	}
	page, size, err := pageRequest(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	q := catalogue.Query{SourceID: v.Source.ID, Album: v.AlbumName, Limit: size, Offset: (page - 1) * size}
	v.Catalogue, err = s.d.Catalogue.Search(r.Context(), q)
	if err != nil {
		s.fail(w, err)
		return
	}
	if v.Catalogue.Total == 0 {
		http.NotFound(w, r)
		return
	}
	v.Title = v.AlbumName
	v.Pagination = paging(r.URL.Path, r.URL.Query(), page, size, v.Catalogue.Total)
	v.Pagination.Target = "#source-album-tracks"
	v.Pagination.CountLabel = "track"
	if v.Pagination.Number != page {
		q.Offset = (v.Pagination.Number - 1) * size
		v.Catalogue, err = s.d.Catalogue.Search(r.Context(), q)
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	first, err := s.d.Catalogue.Search(r.Context(), catalogue.Query{SourceID: v.Source.ID, Album: v.AlbumName, Limit: 1})
	if err != nil {
		s.fail(w, err)
		return
	}
	v.AlbumItem = first.Items[0]
	v.AlbumURL = sourceCoverURL(v.Source.ID, v.AlbumItem)
	v.AlbumBack = "/producer/sources/" + v.Source.ID
	if n, e := strconv.Atoi(r.URL.Query().Get("browse_page")); e == nil && n > 1 {
		v.AlbumBack += "?page=" + strconv.Itoa(n)
	}
	for i, item := range v.Catalogue.Items {
		v.AlbumRows = append(v.AlbumRows, track(item, []sources.Config{v.Source}, q.Offset+i+1, true))
	}
	if fragmentTarget(r, "source-album-tracks") {
		s.renderHTML(w, 200, "source-album-tracks", v)
		return
	}
	s.renderPage(w, r, 200, "source-album-page", v)
}
func (s *Server) sourceArtwork(w http.ResponseWriter, r *http.Request) {
	result, err := s.d.Catalogue.Search(r.Context(), catalogue.Query{SourceID: r.PathValue("id"), Limit: 1, ItemID: r.PathValue("item")})
	if err != nil || len(result.Items) == 0 {
		http.NotFound(w, r)
		return
	}
	s.serveItemArtwork(w, r, result.Items[0])
}
