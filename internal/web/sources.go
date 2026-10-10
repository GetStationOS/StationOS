package web

import (
	"errors"
	"net/http"
	"net/url"
	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/sources"
	"strings"
)

func (s *Server) sourcePage(w http.ResponseWriter, r *http.Request) {
	v, err := s.baseView(r)
	if err != nil {
		s.fail(w, err)
		return
	}
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
	v.Catalogue, err = s.d.Catalogue.Search(r.Context(), catalogue.Query{SourceID: v.Source.ID, Limit: 1})
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, album := range v.Facets.Albums {
		if album.SourceID == v.Source.ID {
			v.SourceAlbums++
		}
	}
	s.render(w, r, v)
}
