package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/sources"
	"strconv"
	"strings"
)

func (s *Server) playlistFilter(w http.ResponseWriter, r *http.Request) {
	v := playlistPageView{pageView: s.baseView(r), PlaylistTab: "selections"}
	p, err := s.d.Playlists.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.playlistError(w, err)
		return
	}
	v.Playlist = p
	v.StationID = p.StationID
	v.PlaylistID = p.ID
	v.Section = "playlists"
	v.Title = p.Name
	v.PlaylistResult, err = s.d.Playlists.Resolve(r.Context(), p.ID, 0, 1)
	if err != nil {
		s.playlistError(w, err)
		return
	}
	v.PlaylistDuration = duration(&v.PlaylistResult.Duration)
	v.Sources, err = s.d.Sources.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	v.Station, err = s.d.Stations.Get(r.Context(), p.StationID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err = s.loadCatalogueChoices(r.Context(), &v.catalogueChoices); err != nil {
		s.fail(w, err)
		return
	}
	rules, err := s.d.Playlists.Rules(r.Context(), p.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.RuleID = r.PathValue("rule")
	found := false
	for _, rule := range rules {
		if rule.ID == v.RuleID && rule.Kind == "filter" {
			v.Filter, err = playlists.ParseFilter(rule.Value)
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.playlistError(w, err)
		return
	}
	if r.Method == http.MethodPost {
		v.Filter = playlists.Filter{Genre: strings.TrimSpace(r.PostForm.Get("genre")), Artist: strings.TrimSpace(r.PostForm.Get("artist")), Composer: strings.TrimSpace(r.PostForm.Get("composer")), SourceID: r.PostForm.Get("source_id")}
		v.YearFromInput = r.PostForm.Get("year_from")
		v.YearToInput = r.PostForm.Get("year_to")
		for _, year := range []struct {
			raw  string
			dest *int
		}{{v.YearFromInput, &v.Filter.YearFrom}, {v.YearToInput, &v.Filter.YearTo}} {
			if year.raw != "" {
				n, e := strconv.Atoi(year.raw)
				if e != nil || n < 1 || n > 9999 {
					v.RuleError = "Enter years from 1 to 9999."
				} else {
					*year.dest = n
				}
			}
		}
		if token := r.PostForm.Get("album"); token != "" {
			var choice albumChoice
			if json.Unmarshal([]byte(token), &choice) != nil || choice.Name == "" || choice.SourceID == "" {
				v.RuleError = "Choose an album from the catalogue."
			} else {
				if v.Filter.SourceID != "" && v.Filter.SourceID != choice.SourceID {
					v.RuleError = "Choose an album belonging to the selected source."
				}
				v.Filter.Album = choice.Name
				if v.Filter.SourceID == "" {
					v.Filter.SourceID = choice.SourceID
				}
			}
		}
		if e := v.Filter.Validate(); e != nil && v.RuleError == "" {
			v.RuleError = e.Error()
		}
		if v.RuleError == "" {
			raw, _ := json.Marshal(v.Filter)
			rule := playlists.Rule{Kind: "filter", Value: string(raw)}
			for n := range rules {
				if rules[n].ID == v.RuleID {
					rules[n] = rule
				}
			}
			err = s.playlistService().ReplaceRules(r.Context(), p.ID, rules)
			if err == nil {
				s.redirect(w, r, withNotice("/producer/playlists/"+p.ID, "selections-saved"))
				return
			}
			if !errors.Is(err, playlists.ErrInvalid) && !errors.Is(err, playlists.ErrUnavailable) && !errors.Is(err, playlists.ErrTooLarge) {
				s.playlistError(w, err)
				return
			}
			v.RuleError = err.Error()
		}
	} else {
		if v.Filter.YearFrom > 0 {
			v.YearFromInput = strconv.Itoa(v.Filter.YearFrom)
		}
		if v.Filter.YearTo > 0 {
			v.YearToInput = strconv.Itoa(v.Filter.YearTo)
		}
	}
	// Keep saved/missing album conditions available while editing and after validation errors.
	if v.Filter.Album != "" {
		token, _ := json.Marshal(albumChoice{SourceID: v.Filter.SourceID, Name: v.Filter.Album})
		if r.Method == http.MethodPost && r.PostForm.Get("album") != "" {
			token = []byte(r.PostForm.Get("album"))
		}
		matched := false
		for n := range v.Albums {
			if v.Albums[n].Value == string(token) {
				v.Albums[n].Selected = true
				matched = true
			}
		}
		if !matched {
			v.Albums = append(v.Albums, albumOption{Value: string(token), Name: v.Filter.Album, Source: "Not in current catalogue", Selected: true})
		}
	}
	if v.Filter.SourceID != "" {
		found := false
		for _, source := range v.Sources {
			if source.ID == v.Filter.SourceID {
				found = true
			}
		}
		if !found {
			v.Sources = append(v.Sources, sources.Config{ID: v.Filter.SourceID, Name: "Unavailable source · selection kept"})
		}
	}
	status := http.StatusOK
	if v.RuleError != "" {
		status = http.StatusUnprocessableEntity
	}
	s.renderPage(w, r, status, "playlist-filter-page", v)
}
