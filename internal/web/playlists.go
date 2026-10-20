package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/station"
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
	v := playlistPageView{pageView: s.baseView(r)}
	var err error
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
		v.PlaylistID = v.Playlist.ID
		if v.PlaylistTab == "selections" {
			v.Sources, err = s.d.Sources.List(r.Context())
			v.Sources = stationSources(v.Sources, v.Station)
			if err != nil {
				s.fail(w, err)
				return
			}
			if err = s.loadCatalogueChoices(r.Context(), &v.catalogueChoices, v.StationID); err != nil {
				s.fail(w, err)
				return
			}
			rules, err := s.d.Playlists.Rules(r.Context(), id)
			if err != nil {
				s.fail(w, err)
				return
			}
			v.prepareSelections(rules)
		}
		if v.PlaylistTab == "tracks" {
			v.Sources, err = s.d.Sources.List(r.Context())
			v.Sources = stationSources(v.Sources, v.Station)
			if err != nil {
				s.fail(w, err)
				return
			}
		}
		if v.PlaylistTab != "delete" {
			page, size, err := pageRequest(r.URL.Query())
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			totalRows := 0
			load := func(number int) error {
				if v.PlaylistTab == "tracks" {
					v.TrackSearch = strings.TrimSpace(r.URL.Query().Get("q"))
					if len(v.TrackSearch) > 300 {
						return playlists.ErrInvalid
					}
					review, err := s.d.Playlists.SearchReview(r.Context(), id, v.TrackSearch, (number-1)*size, size)
					v.PlaylistResult = review.Result
					v.ExcludedTracks = review.Excluded
					totalRows = review.Rows
					return err
				}
				var err error
				v.PlaylistResult, err = s.d.Playlists.Resolve(r.Context(), id, (number-1)*size, size)
				totalRows = v.PlaylistResult.Total
				return err
			}
			err = load(page)
			if err != nil {
				s.playlistError(w, err)
				return
			}
			v.Pagination = paging(r.URL.Path, r.URL.Query(), page, size, totalRows)
			v.Pagination.ActionURL = "/producer/content?playlist=" + id
			if v.PlaylistTab == "tracks" {
				v.Pagination.Target = "#playlist-track-results"
			}
			if page != v.Pagination.Number {
				err = load(v.Pagination.Number)
				if err != nil {
					s.playlistError(w, err)
					return
				}
			}
			v.PlaylistDuration = duration(&v.PlaylistResult.Duration)
			returnURL := *r.URL
			returnQuery := returnURL.Query()
			for _, key := range []string{"row_item", "notice", "new_tracks", "new_albums", "selections_saved"} {
				returnQuery.Del(key)
			}
			returnURL.RawQuery = returnQuery.Encode()
			v.ReturnTo = returnURL.RequestURI()
			if r.URL.Query().Get("notice") == "playlist-refreshed" {
				tracks, tErr := strconv.Atoi(r.URL.Query().Get("new_tracks"))
				albums, aErr := strconv.Atoi(r.URL.Query().Get("new_albums"))
				if tErr == nil && aErr == nil && tracks >= 0 && tracks <= playlists.MaxItems && albums >= 0 && albums <= tracks {
					if tracks == 0 {
						v.PlaylistRefreshNotice = "Playlist is up to date. No new albums or tracks."
					} else {
						trackWord, albumWord := "tracks", "albums"
						if tracks == 1 {
							trackWord = "track"
						}
						if albums == 1 {
							albumWord = "album"
						}
						v.PlaylistRefreshNotice = fmt.Sprintf("Playlist refreshed: %d new %s · %d new %s.", tracks, trackWord, albums, albumWord)
					}
				}
				if r.URL.Query().Get("selections_saved") == "1" && v.PlaylistRefreshNotice != "" {
					v.PlaylistRefreshNotice = "Selections saved. " + v.PlaylistRefreshNotice
				}
			}
			if v.PlaylistTab == "tracks" {
				for n, entry := range v.PlaylistResult.Entries {
					row := track(entry.Item, v.Sources, (v.Pagination.Number-1)*size+n+1, entry.Available)
					row.Excluded = entry.Excluded
					v.Rows = append(v.Rows, row)
				}
			}
		}
	}
	if id == "" {
		if v.Station.ID != "" {
			v.Stations = []station.Station{v.Station}
		}
		if r.URL.Path == "/producer/playlists" {
			if err = s.loadPlaylistOptions(r.Context(), &v.playlistChoices, v.Station); err != nil {
				s.fail(w, err)
				return
			}
			if err = s.markDefaultPlaylists(r.Context(), &v.playlistChoices, v.Station); err != nil {
				s.fail(w, err)
				return
			}
		} else if v.StationID == "" {
			v.Stations, err = s.d.Stations.List(r.Context())
			if err != nil {
				s.fail(w, err)
				return
			}
		}
	}
	if v.PlaylistTab == "details" && v.StationID != "" && s.d.BroadcastConfig != nil {
		v.BroadcastPlaylist, err = s.d.BroadcastConfig.Assignment(r.Context(), v.StationID)
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	name := "playlists-page"
	if id != "" {
		name = "playlist-selections-page"
		switch v.PlaylistTab {
		case "details":
			name = "playlist-details-page"
		case "tracks":
			name = "playlist-tracks-page"
		case "delete":
			name = "playlist-delete-page"
		}
	} else if r.URL.Path == "/producer/playlists/new" {
		name = "new-playlist-page"
	}
	if v.PlaylistTab == "delete" && fragmentTarget(r, "dialog-body") {
		s.renderHTML(w, http.StatusOK, "delete-playlist", v)
		return
	}
	if v.PlaylistTab == "tracks" && fragmentTarget(r, "playlist-track-row") {
		selected := r.URL.Query().Get("row_item")
		for _, row := range v.Rows {
			if row.Item.ID == selected {
				v.Rows = []trackRow{row}
				s.renderHTML(w, http.StatusOK, "playlist-track-row-response", v)
				return
			}
		}
		http.Error(w, "Track no longer appears in these results. Refresh the search.", http.StatusConflict)
		return
	}
	if v.PlaylistTab == "tracks" && fragmentTarget(r, "playlist-track-results") {
		s.renderHTML(w, http.StatusOK, "playlist-tracks-results", v)
		return
	}
	s.renderPage(w, r, http.StatusOK, name, v)
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
	s.playlistSelectionResponse(w, r, id)
}
func withNotice(destination, message string) string {
	u, _ := url.Parse(destination)
	q := u.Query()
	q.Set("notice", message)
	u.RawQuery = q.Encode()
	return u.String()
}
func (v *playlistPageView) prepareSelections(rules []playlists.Rule) {
	for _, rule := range rules {
		if rule.Kind == "filter" {
			label := rule.Label()
			for _, source := range v.Sources {
				label = strings.ReplaceAll(label, "Source: "+source.ID, "Source: "+source.Name)
			}
			v.SavedRestrictions = append(v.SavedRestrictions, ruleRow{Rule: rule, Source: label})
		}
	}

	for _, spec := range []struct {
		kind, name string
		values     []string
	}{
		{"artist", "Artists", v.Facets.Artists}, {"composer", "Composers", v.Facets.Composers}, {"year", "Years", v.Facets.Years}, {"decade", "Decades", v.Facets.Decades},
	} {
		group := ruleChoiceGroup{Kind: spec.kind, Name: spec.name}
		chosen := map[string]playlists.Rule{}
		for _, rule := range rules {
			if rule.Kind == spec.kind {
				chosen[rule.Value] = rule
			}
		}
		for _, value := range spec.values {
			_, ok := chosen[value]
			label := value
			if spec.kind == "decade" {
				label += "s"
			}
			group.Options = append(group.Options, ruleChoice{Name: label, Value: value, Selected: ok})
			delete(chosen, value)
		}
		keys := []string{}
		for value := range chosen {
			keys = append(keys, value)
		}
		sort.Strings(keys)
		for _, value := range keys {
			rule := chosen[value]
			label := rule.Label()
			for _, source := range v.Sources {
				label = strings.ReplaceAll(label, "Source: "+source.ID, "Source: "+source.Name)
			}
			option := ruleChoice{Name: label, Value: value, Selected: true, Missing: true}
			group.Options = append(group.Options, option)
		}
		v.RuleGroups = append(v.RuleGroups, group)
	}
	genres := map[string]bool{}
	albums := map[string]bool{}
	for _, rule := range rules {
		if rule.Kind == "genre" {
			genres[rule.Value] = true
		} else if rule.Kind == "album" {
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
	if len(r.PostForm["filter"]) > 0 {
		http.Error(w, "Edit or remove saved advanced restrictions separately.", 400)
		return
	}
	// Legacy restrictions are never silently erased by the simpler selections form.
	saved, err := s.d.Playlists.Rules(r.Context(), r.PathValue("id"))
	if err != nil {
		s.playlistError(w, err)
		return
	}
	rules := []playlists.Rule{}
	for _, rule := range saved {
		if rule.Kind == "filter" {
			rules = append(rules, rule)
		}
	}
	for _, kind := range []string{"artist", "composer", "year", "decade"} {
		for _, value := range r.PostForm[kind] {
			rules = append(rules, playlists.Rule{Kind: kind, Value: value})
		}
	}
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
		http.Error(w, "Choose up to 100 rules in total, then save again.", 400)
		return
	}
	report, err := s.playlistService().ReplaceRulesAndRefresh(r.Context(), r.PathValue("id"), rules)
	if err != nil {
		s.playlistError(w, err)
		return
	}
	s.redirect(w, r, playlistRefreshReturn("/producer/playlists/"+r.PathValue("id"), report, true))
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
	v := playlistPageView{pageView: pageView{CSRF: s.csrf}, Playlist: p, BroadcastPlaylist: assignment}
	if s.d.Broadcast != nil && notice != "" {
		s.d.Broadcast.Changed()
	}
	s.renderHTML(w, http.StatusOK, "playlist-default", v)
}

func (s *Server) restoreItem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.playlistService().Select(r.Context(), id, []string{r.PostForm.Get("item")}, true); err != nil {
		s.playlistError(w, err)
		return
	}
	s.playlistSelectionResponse(w, r, id)
}
func (s *Server) refreshPlaylist(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	report, err := s.d.Playlists.Refresh(r.Context(), id)
	if err != nil {
		s.playlistError(w, err)
		return
	}
	s.redirect(w, r, playlistRefreshReturn(playlistReturn(r.PostForm.Get("return_to"), id), report, false))
}
func playlistRefreshReturn(raw string, report playlists.RefreshResult, saved bool) string {
	destination, _ := url.Parse(raw)
	query := destination.Query()
	query.Set("notice", "playlist-refreshed")
	query.Set("new_tracks", strconv.Itoa(report.NewTracks))
	query.Set("new_albums", strconv.Itoa(report.NewAlbums))
	query.Del("selections_saved")
	if saved {
		query.Set("selections_saved", "1")
	}
	destination.RawQuery = query.Encode()
	return destination.String()
}

// Row changes retain the exact search/page and update the independent heading
// totals out of band. Ordinary form submissions retain their normal GET fallback.
func (s *Server) playlistSelectionResponse(w http.ResponseWriter, r *http.Request, id string) {
	destination := playlistReturn(r.PostForm.Get("return_to"), id)
	if fragmentRequest(r) && strings.HasPrefix(r.Header.Get("HX-Target"), "playlist-track-") {
		if s.d.Broadcast != nil {
			s.d.Broadcast.Changed()
		}
		u, _ := url.Parse(destination)
		if u.Path != "/producer/playlists/"+id+"/tracks" {
			s.redirect(w, r, destination)
			return
		}
		query := u.Query()
		query.Del("notice")
		query.Set("row_item", r.PostForm.Get("item"))
		u.RawQuery = query.Encode()
		next := r.Clone(r.Context())
		next.Method = http.MethodGet
		next.URL = u
		next.Header = r.Header.Clone()
		next.Header.Set("HX-Target", "playlist-track-row")
		s.playlistPage(w, next)
		return
	}
	s.redirect(w, r, destination)
}
