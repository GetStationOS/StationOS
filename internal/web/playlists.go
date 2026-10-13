package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"stationos.local/stationos/internal/playlists"
)

func (s *Server) playlistService() playlists.Service {
	return playlists.Service{Repo: s.d.Playlists, Stations: s.d.Stations}
}
func (s *Server) playlistError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, playlists.ErrInUse):
		http.Error(w, err.Error(), 409)
	case errors.Is(err, playlists.ErrNotFound):
		http.Error(w, "playlist not found", 404)
	case errors.Is(err, playlists.ErrInvalid), errors.Is(err, playlists.ErrTooLarge), errors.Is(err, playlists.ErrUnavailable):
		http.Error(w, err.Error(), 400)
	default:
		s.fail(w, err)
	}
}
func (s *Server) playlistPage(w http.ResponseWriter, r *http.Request) {
	if s.d.Playlists == nil {
		http.NotFound(w, r)
		return
	}
	v, err := s.baseView(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.Title = "Playlists"
	if r.URL.Path == "/producer/playlists/new" {
		v.Title = "Add playlist"
	}
	v.StationID = r.URL.Query().Get("station")
	v.Section = "playlists"
	if v.StationID != "" {
		v.Station, err = s.d.Stations.Get(r.Context(), v.StationID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	id := r.PathValue("id")
	if id != "" {
		v.Playlist, err = s.d.Playlists.Get(r.Context(), id)
		if err != nil {
			s.playlistError(w, err)
			return
		}
		v.Title = v.Playlist.Name
		v.PlaylistTab = "selections"
		if strings.HasSuffix(r.URL.Path, "/tracks") {
			v.PlaylistTab = "tracks"
		}
		if strings.HasSuffix(r.URL.Path, "/settings") {
			v.PlaylistTab = "details"
		}
		if strings.HasSuffix(r.URL.Path, "/delete") {
			v.PlaylistTab = "delete"
			v.Title = "Delete playlist"
		}
		v.Station, err = s.d.Stations.Get(r.Context(), v.Playlist.StationID)
		if err != nil {
			s.fail(w, err)
			return
		}
		v.StationID = v.Station.ID
		for _, station := range v.Stations {
			if station.ID == v.Playlist.StationID {
				v.PlaylistStation = station.Name
				break
			}
		}
		rules, err := s.d.Playlists.Rules(r.Context(), id)
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, rule := range rules {
			row := ruleRow{Rule: rule}
			for _, source := range v.Sources {
				if source.ID == rule.SourceID {
					row.Source = source.Name
					break
				}
			}
			v.Rules = append(v.Rules, row)
		}
		v.prepareSelections(rules)
		page, size, err := pageRequest(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		v.PlaylistResult, err = s.d.Playlists.Resolve(r.Context(), id, (page-1)*size, size)
		if err != nil {
			s.playlistError(w, err)
			return
		}
		v.Pagination = paging(r.URL.Path, r.URL.Query(), page, size, v.PlaylistResult.Total)
		v.Pagination.ActionURL = "/producer/content?playlist=" + id
		if page != v.Pagination.Number {
			v.PlaylistResult, err = s.d.Playlists.Resolve(r.Context(), id, (v.Pagination.Number-1)*size, size)
			if err != nil {
				s.playlistError(w, err)
				return
			}
		}
		v.PlaylistDuration = duration(&v.PlaylistResult.Duration)
		v.ReturnTo = r.URL.RequestURI()
		for n, entry := range v.PlaylistResult.Entries {
			v.Rows = append(v.Rows, track(entry.Item, v.Sources, (v.Pagination.Number-1)*size+n+1, entry.Available))
		}
	}
	if v.StationID != "" && s.d.BroadcastConfig != nil {
		v.BroadcastPlaylist, err = s.d.BroadcastConfig.Assignment(r.Context(), v.StationID)
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	s.render(w, r, v)
}
func (s *Server) savePlaylist(w http.ResponseWriter, r *http.Request) {
	p := playlists.Playlist{ID: r.PostForm.Get("id"), StationID: r.PostForm.Get("station"), Name: r.PostForm.Get("name"), Description: r.PostForm.Get("description")}
	saved, err := s.playlistService().Save(r.Context(), p)
	if err != nil {
		s.playlistError(w, err)
		return
	}
	s.redirect(w, r, withNotice(playlistReturn(r.PostForm.Get("return_to"), saved.ID), "playlist-saved"))
}

// Return destinations are restricted to catalogue and this playlist's own page.
func playlistReturn(raw, id string) string {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(raw, "\r\n") {
		return "/producer/playlists/" + id
	}
	if u.Path == "/producer/content" || u.Path == "/producer/playlists/"+id || u.Path == "/producer/playlists/"+id+"/tracks" || u.Path == "/producer/playlists/"+id+"/settings" {
		return u.String()
	}
	return "/producer/playlists/" + id
}
func (s *Server) selectItems(w http.ResponseWriter, r *http.Request) {
	id := r.PostForm.Get("playlist")
	if err := s.playlistService().Select(r.Context(), id, r.PostForm["item"], true); err != nil {
		s.playlistError(w, err)
		return
	}
	destination, _ := url.Parse(playlistReturn(r.PostForm.Get("return_to"), id))
	if destination.Path == "/producer/content" {
		query := destination.Query()
		query.Set("playlist", id)
		query.Set("notice", "tracks-added")
		destination.RawQuery = query.Encode()
	}
	s.redirect(w, r, destination.String())
}
func (s *Server) addRule(w http.ResponseWriter, r *http.Request) {
	rule := playlists.Rule{Kind: r.PostForm.Get("kind"), Value: r.PostForm.Get("value")}
	if rule.Kind == "album" {
		var choice albumChoice
		if err := json.Unmarshal([]byte(r.PostForm.Get("album")), &choice); err != nil {
			http.Error(w, "invalid album selection", 400)
			return
		}
		rule.Value = choice.Name
		rule.SourceID = choice.SourceID
	}
	id := r.PathValue("id")
	if err := s.playlistService().AddRule(r.Context(), id, rule); err != nil {
		s.playlistError(w, err)
		return
	}
	s.redirect(w, r, "/producer/playlists/"+id)
}
func (s *Server) removeRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.d.Playlists.RemoveRule(r.Context(), id, r.PathValue("rule")); err != nil {
		s.playlistError(w, err)
		return
	}
	s.redirect(w, r, "/producer/playlists/"+id)
}
func (s *Server) removeItem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.playlistService().Select(r.Context(), id, []string{r.PostForm.Get("item")}, false); err != nil {
		s.playlistError(w, err)
		return
	}
	s.redirect(w, r, withNotice(playlistReturn(r.PostForm.Get("return_to"), id), "track-removed"))
}
func withNotice(destination, message string) string {
	u, _ := url.Parse(destination)
	q := u.Query()
	q.Set("notice", message)
	u.RawQuery = q.Encode()
	return u.String()
}
func (v *view) prepareSelections(rules []playlists.Rule) {
	genres := map[string]bool{}
	albums := map[string]bool{}
	for _, rule := range rules {
		if rule.Kind == "genre" {
			genres[rule.Value] = true
		} else {
			token, _ := json.Marshal(albumChoice{SourceID: rule.SourceID, Name: rule.Value})
			albums[string(token)] = true
		}
	}
	for _, genre := range v.Facets.Genres {
		v.Genres = append(v.Genres, genreOption{Name: genre, Selected: genres[genre]})
		delete(genres, genre)
	}
	for genre := range genres {
		v.Genres = append(v.Genres, genreOption{Name: genre, Selected: true, Missing: true})
	}
	for n := range v.Albums {
		v.Albums[n].Selected = albums[v.Albums[n].Value]
		delete(albums, v.Albums[n].Value)
	}
	for token := range albums {
		var choice albumChoice
		_ = json.Unmarshal([]byte(token), &choice)
		v.Albums = append(v.Albums, albumOption{Value: token, Name: choice.Name, Source: "Not in current catalogue", Selected: true, Missing: true})
	}
}
func (s *Server) replaceRules(w http.ResponseWriter, r *http.Request) {
	rules := []playlists.Rule{}
	for _, genre := range r.PostForm["genre"] {
		rules = append(rules, playlists.Rule{Kind: "genre", Value: genre})
	}
	for _, token := range r.PostForm["album"] {
		var choice albumChoice
		if err := json.Unmarshal([]byte(token), &choice); err != nil {
			http.Error(w, "invalid album selection", 400)
			return
		}
		rules = append(rules, playlists.Rule{Kind: "album", Value: choice.Name, SourceID: choice.SourceID})
	}
	if len(rules) > playlists.MaxRules {
		http.Error(w, "Choose up to 100 genres and albums in total, then save again.", 400)
		return
	}
	if err := s.playlistService().ReplaceRules(r.Context(), r.PathValue("id"), rules); err != nil {
		s.playlistError(w, err)
		return
	}
	s.redirect(w, r, "/producer/playlists/"+r.PathValue("id")+"?notice=selections-saved")
}

func (s *Server) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "confirm playlist deletion", 400)
		return
	}
	p, err := s.d.Playlists.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.playlistError(w, err)
		return
	}
	if err = s.d.Playlists.Delete(r.Context(), p.ID); err != nil {
		s.playlistError(w, err)
		return
	}
	if s.d.Broadcast != nil {
		s.d.Broadcast.StopPlaylist(p.ID)
	}
	s.redirect(w, r, "/producer/playlists?station="+p.StationID+"&notice=playlist-deleted")
}

func (s *Server) defaultPlaylist(w http.ResponseWriter, r *http.Request) {
	if s.d.BroadcastConfig == nil || s.d.Playlists == nil {
		http.Error(w, "broadcast unavailable", http.StatusServiceUnavailable)
		return
	}
	p, err := s.d.Playlists.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.playlistError(w, err)
		return
	}
	playlistID, notice := p.ID, "broadcast-saved"
	switch r.PostForm.Get("action") {
	case "set":
	case "clear":
		current, err := s.d.BroadcastConfig.Assignment(r.Context(), p.StationID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if current != p.ID {
			s.defaultPlaylistResponse(w, r, p, "")
			return
		}
		playlistID, notice = "", "default-cleared"
	default:
		http.Error(w, "Choose set or clear for the station default.", http.StatusBadRequest)
		return
	}
	if err := s.d.BroadcastConfig.Assign(r.Context(), p.StationID, playlistID); err != nil {
		s.playlistError(w, err)
		return
	}
	if s.d.Broadcast != nil {
		s.d.Broadcast.Stop(p.StationID)
	}
	s.defaultPlaylistResponse(w, r, p, notice)
}

func (s *Server) defaultPlaylistResponse(w http.ResponseWriter, r *http.Request, p playlists.Playlist, notice string) {
	if r.Header.Get("HX-Request") != "true" {
		to := "/producer/playlists/" + p.ID + "/settings"
		if notice != "" {
			to += "?notice=" + notice
		}
		s.redirect(w, r, to)
		return
	}
	assignment, err := s.d.BroadcastConfig.Assignment(r.Context(), p.StationID)
	if err != nil {
		s.fail(w, err)
		return
	}
	v := view{Playlist: p, CSRF: s.csrf, BroadcastPlaylist: assignment}
	var body bytes.Buffer
	if err := s.t.ExecuteTemplate(&body, "playlist-default", v); err != nil {
		s.fail(w, err)
		return
	}
	if s.d.Broadcast != nil && notice != "" {
		s.d.Broadcast.Changed()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(body.Bytes()); err != nil {
		s.d.Logger.Error("default playlist rendering failed", "error", err)
	}
}
