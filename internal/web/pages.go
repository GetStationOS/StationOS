package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

type field struct{ Key, Value string }
type pageLink struct {
	Number  int
	URL     string
	Current bool
}
type pagination struct {
	Number, Pages, Size, Start, End, Total int
	First, Previous, Next, Last            string
	Links                                  []pageLink
	Fields                                 []field
	Path                                   string
	ActionURL                              string
}

func pageRequest(values url.Values) (int, int, error) {
	size := 25
	var err error
	if raw := values.Get("per_page"); raw != "" {
		size, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid page size")
		}
	}
	if size != 25 && size != 50 && size != 100 {
		return 0, 0, fmt.Errorf("page size must be 25, 50 or 100")
	}
	page := 1
	if raw := values.Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid page")
		}
	} else if raw := values.Get("offset"); raw != "" {
		offset, e := strconv.Atoi(raw)
		if e != nil || offset < 0 || offset > 1000000 {
			return 0, 0, fmt.Errorf("invalid page")
		}
		page = offset/size + 1
	}
	if page < 1 || page > 1000000/size+1 {
		return 0, 0, fmt.Errorf("invalid page")
	}
	return page, size, nil
}
func paging(path string, values url.Values, page, size, total int) pagination {
	out := pagination{Number: page, Size: size, Total: total, Pages: max(1, (total+size-1)/size), Path: path}
	out.Number = min(out.Number, out.Pages)
	params := url.Values{}
	for _, key := range []string{"q", "genre", "album_choice", "source", "sort", "direction", "playlist", "station", "per_page"} {
		if value := values.Get(key); value != "" {
			params.Set(key, value)
			out.Fields = append(out.Fields, field{key, value})
		}
	}
	link := func(n int) string { params.Set("page", strconv.Itoa(n)); return path + "?" + params.Encode() }
	out.First = link(1)
	out.Last = link(out.Pages)
	if out.Number > 1 {
		out.Previous = link(out.Number - 1)
	}
	if out.Number < out.Pages {
		out.Next = link(out.Number + 1)
	}
	for n := max(1, out.Number-2); n <= min(out.Pages, out.Number+2); n++ {
		out.Links = append(out.Links, pageLink{Number: n, URL: link(n), Current: n == out.Number})
	}
	if total > 0 {
		out.Start = (out.Number-1)*size + 1
		out.End = min(out.Number*size, total)
	}
	return out
}

type albumChoice struct {
	SourceID string `json:"source_id"`
	Name     string `json:"name"`
}

func catalogueQuery(values url.Values) (catalogue.Query, error) {
	q := catalogue.Query{Text: values.Get("q"), Genre: values.Get("genre"), SourceID: values.Get("source"), Sort: values.Get("sort"), Limit: 25}
	switch values.Get("direction") {
	case "", "asc":
	case "desc":
		q.Descending = true
	default:
		return q, fmt.Errorf("invalid sort direction")
	}
	if raw := values.Get("album_choice"); raw != "" {
		var album albumChoice
		if err := json.Unmarshal([]byte(raw), &album); err != nil || album.SourceID == "" || album.Name == "" {
			return q, fmt.Errorf("invalid album filter")
		}
		q.SourceID = album.SourceID
		q.Album = album.Name
	}
	return q, q.Validate()
}

type trackRow struct {
	Item                      content.AudioItem
	Source, Duration, Formats string
	Number                    int
	Available                 bool
}

func duration(value *time.Duration) string {
	if value == nil {
		return "—"
	}
	seconds := int64(*value / time.Second)
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}
func track(item content.AudioItem, sources []sources.Config, number int, available bool) trackRow {
	row := trackRow{Item: item, Number: number, Available: available, Duration: duration(item.Duration), Source: "Unavailable source"}
	for _, source := range sources {
		if source.ID == item.SourceID {
			row.Source = source.Name
			break
		}
	}
	formats := []string{}
	seen := map[string]bool{}
	for _, resource := range item.Resources {
		format := strings.TrimPrefix(resource.MIME, "audio/")
		switch format {
		case "mpeg", "mp3":
			format = "MP3"
		case "flac", "x-flac":
			format = "FLAC"
		case "aac", "aacp", "x-aac":
			format = "AAC"
		default:
			format = strings.ToUpper(format)
		}
		if !seen[format] {
			seen[format] = true
			formats = append(formats, format)
		}
	}
	row.Formats = strings.Join(formats, ", ")
	if row.Formats == "" {
		row.Formats = "—"
	}
	return row
}

type albumOption struct {
	Value, Name, Source string
	Tracks              int
	Selected, Missing   bool
}
type playlistOption struct {
	Playlist playlists.Playlist
	Station  string
	Default  bool
}
type ruleRow struct {
	Rule   playlists.Rule
	Source string
}
type genreOption struct {
	Name              string
	Selected, Missing bool
}
type stationCard struct {
	Broadcast                                                               broadcast.State
	Station                                                                 station.Station
	Playlists                                                               int
	PlaylistID, PlaylistName, ProgrammeTime, PresenterStatus, FollowingTime string
	CurrentTrack, NextTrack                                                 cardTrack
}
type sourceCard struct {
	Source sources.Config
	Host   string
	Tracks int
}
type view struct {
	AudienceMode, AudienceKind, AudienceKey                  string
	AudienceQuery, AudienceToken, AudienceFilter             string
	AudienceShow                                             schedule.Occurrence
	AudienceName, AudienceText, AudienceTrack, AudienceError string
	AudienceConsent                                          bool
	AudienceShows                                            []schedule.Occurrence
	AudienceTracks                                           []content.AudioItem
	AudienceMoreTracks                                       bool
	AudienceMessages                                         []audience.Message
	AudienceTotal                                            int
	AudienceStatuses                                         []audienceStatusChoice
	AudiencePrevious, AudienceNext, AudienceRefreshURL       string
	Public                                                   bool
	PublicDays                                               []publicDay
	PublicDayLinks                                           []publicDay
	PublicDate                                               string
	AudiencePanel                                            string
	AudienceDay                                              string
	PublicShow                                               publicShow
	AudienceBase                                             string
	PublicCurrent, PublicNext                                cardTrack
	StationRepeatSupplied                                    bool
	StationRepeatInput                                       string
	StationError                                             string
	MixerChannels                                            []mixerChannel
	MusicVolume, DJVolume, MixerError                        string
	Show                                                     schedule.Show
	Shows                                                    []schedule.Show
	ShowError                                                string
	ScheduleMode, ShowStart, ShowEnd                         string
	ScheduleDays                                             []scheduleDay
	ShowDays                                                 map[int]bool
	ScheduleHours                                            []scheduleHour
	ScheduleCards                                            []scheduleCard

	DJ                                                                                                   djs.Profile
	DJCards                                                                                              []djCard
	DJs                                                                                                  []djs.Profile
	DJVoices                                                                                             []djs.Voice
	DJModels                                                                                             []djModel
	DJError                                                                                              string
	DJMode, ExampleVibe                                                                                  string
	DJCustomModel                                                                                        bool
	Broadcast                                                                                            broadcast.State
	BroadcastPlaylist, StreamURL                                                                         string
	BroadcastEnabled                                                                                     bool
	Source                                                                                               sources.Config
	SourceTab, SourceHost                                                                                string
	SourceAlbums                                                                                         int
	SourceCards                                                                                          []sourceCard
	TargetPlaylist                                                                                       playlists.Playlist
	StationPlaylistCount                                                                                 int
	Notice, Section                                                                                      string
	Station                                                                                              station.Station
	StationCards                                                                                         []stationCard
	Genres                                                                                               []genreOption
	Page, Title, CSRF, Query, Genre, Sort, Direction, AlbumChoice, SelectedPlaylist, ReturnTo, StationID string
	Stations                                                                                             []station.Station
	Sources                                                                                              []sources.Config
	Catalogue                                                                                            catalogue.Result
	Facets                                                                                               catalogue.Facets
	Albums                                                                                               []albumOption
	PlaylistOptions                                                                                      []playlistOption
	Playlist                                                                                             playlists.Playlist
	PlaylistStation                                                                                      string
	PlaylistTab                                                                                          string
	Rules                                                                                                []ruleRow
	Rows                                                                                                 []trackRow
	Pagination                                                                                           pagination
	PlaylistResult                                                                                       playlists.Result
	PlaylistDuration                                                                                     string
}

func (s *Server) baseView(r *http.Request) (view, error) {
	v := view{Page: r.URL.Path, CSRF: s.csrf}
	v.BroadcastEnabled = s.d.Broadcast != nil

	v.Notice = map[string]string{"station-deleted": "Station deleted.", "playlist-deleted": "Playlist deleted.", "station-saved": "Station saved.", "playlist-saved": "Playlist saved.", "selections-saved": "Selections saved.", "tracks-added": "Tracks added to the playlist.", "track-removed": "Track excluded from this playlist.", "source-saved": "Source saved.", "imported": "Library refreshed."}[r.URL.Query().Get("notice")]
	var err error
	v.Stations, err = s.d.Stations.List(r.Context())
	if err != nil {
		return v, err
	}
	v.Sources, err = s.d.Sources.List(r.Context())
	if err != nil {
		return v, err
	}
	v.Facets, err = s.d.Catalogue.Facets(r.Context())
	if err != nil {
		return v, err
	}
	for _, album := range v.Facets.Albums {
		token, _ := json.Marshal(albumChoice{SourceID: album.SourceID, Name: album.Name})
		v.Albums = append(v.Albums, albumOption{Value: string(token), Name: album.Name, Source: album.SourceName, Tracks: album.Tracks})
	}
	if s.d.Playlists != nil {
		items, err := s.d.Playlists.List(r.Context(), "")
		if err != nil {
			return v, err
		}
		for _, p := range items {
			name := "Unknown station"
			for _, station := range v.Stations {
				if station.ID == p.StationID {
					name = station.Name
					break
				}
			}
			v.PlaylistOptions = append(v.PlaylistOptions, playlistOption{Playlist: p, Station: name})
		}
	}
	for _, st := range v.Stations {
		card := stationCard{Station: st}
		card.Broadcast = broadcast.State{Status: "Dormant"}
		if s.d.Broadcast != nil {
			card.Broadcast = s.d.Broadcast.State(st.ID)
		}
		for _, p := range v.PlaylistOptions {
			if p.Playlist.StationID == st.ID {
				card.Playlists++
			}
		}
		if s.d.BroadcastConfig != nil {
			card.PlaylistID, err = s.d.BroadcastConfig.Assignment(r.Context(), st.ID)
			if err != nil {
				return v, err
			}
		}
		for i := range v.PlaylistOptions {
			if v.PlaylistOptions[i].Playlist.ID == card.PlaylistID {
				v.PlaylistOptions[i].Default = true
			}
		}
		if card.Broadcast.Current.ID != "" && card.Broadcast.Programme.PlaylistID != "" {
			card.PlaylistID = card.Broadcast.Programme.PlaylistID
		}
		for _, p := range v.PlaylistOptions {
			if p.Playlist.ID == card.PlaylistID {
				card.PlaylistName = p.Playlist.Name
				break
			}
		}
		if card.PlaylistName == "" && card.Broadcast.Current.ID != "" {
			card.PlaylistName = card.Broadcast.Programme.PlaylistName
		}
		card.ProgrammeTime, card.PresenterStatus = programmeDetails(card.Broadcast, st.Timezone, time.Now())
		if !card.Broadcast.Following.Start.IsZero() {
			loc, e := time.LoadLocation(st.Timezone)
			if e != nil {
				loc = time.UTC
			}
			card.FollowingTime = card.Broadcast.Following.Start.In(loc).Format("Mon 15:04")
		}
		card.NextTrack = stationTrack(st.ID, "Up next", "Not queued", card.Broadcast.Next)
		v.StationCards = append(v.StationCards, card)
	}
	return v, nil
}
func (s *Server) render(w http.ResponseWriter, r *http.Request, v view) {
	for _, p := range v.PlaylistOptions {
		if v.StationID == "" || p.Playlist.StationID == v.StationID {
			v.StationPlaylistCount++
		}
	}
	name := "layout"
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true" {
		name = "fragment"
	}
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true" && v.AudienceMode == "queue" && r.Header.Get("HX-Target") == "audience-queue-results" {
		name = "audience-queue-results"
	}
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "dialog-body" {
		if v.ScheduleMode == "delete" {
			name = "delete-show"
		} else if v.DJMode == "delete" {
			name = "delete-dj"
		} else if v.PlaylistTab == "delete" {
			name = "delete-playlist"
		} else if v.Section == "delete" {
			name = "delete-station"
		}
	}
	if v.Public {
		name = "public-layout"
		if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true" {
			name = "public-fragment"
			if r.Header.Get("HX-Target") == v.AudiencePanel {
				name = "audience-form"
			}
			if r.Header.Get("HX-Target") == "audience-track-options-"+v.AudiencePanel {
				name = "audience-tracks"
			}
		}
	}
	var body strings.Builder
	if err := s.t.ExecuteTemplate(&body, name, v); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(body.String())); err != nil {
		s.d.Logger.Error("HTML write failed", "error", err)
	}
}
func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	v, err := s.baseView(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	switch r.URL.Path {
	case "/producer":
		v.Title = "Your stations"
	case "/producer/station", "/producer/stations/new":
		v.Title = "Create a station"
	case "/producer/sources/new":
		v.Title = "Add source"
	case "/producer/sources":
		v.Title = "Sources"
	case "/producer/content":
		v.Title = "Browse tracks"
	}
	q, err := catalogueQuery(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	page, size, err := pageRequest(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	q.Limit = size
	q.Offset = (page - 1) * size
	v.Catalogue, err = s.d.Catalogue.Search(r.Context(), q)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.Pagination = paging(r.URL.Path, r.URL.Query(), page, size, v.Catalogue.Total)
	if v.Pagination.Number != page {
		q.Offset = (v.Pagination.Number - 1) * size
		v.Catalogue, err = s.d.Catalogue.Search(r.Context(), q)
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	v.Query = q.Text
	v.Genre = q.Genre
	v.Sort = q.Sort
	v.Direction = r.URL.Query().Get("direction")
	v.AlbumChoice = r.URL.Query().Get("album_choice")
	v.SelectedPlaylist = r.URL.Query().Get("playlist")
	if r.URL.Path == "/producer/content" && v.SelectedPlaylist != "" && s.d.Playlists != nil {
		v.TargetPlaylist, err = s.d.Playlists.Get(r.Context(), v.SelectedPlaylist)
		if err != nil {
			s.playlistError(w, err)
			return
		}
		v.Station, err = s.d.Stations.Get(r.Context(), v.TargetPlaylist.StationID)
		if err != nil {
			s.fail(w, err)
			return
		}
		v.StationID = v.Station.ID
		v.Section = "playlists"
	}
	if r.URL.Path == "/producer/sources" {
		for _, source := range v.Sources {
			items, err := s.d.Catalogue.Search(r.Context(), catalogue.Query{SourceID: source.ID, Limit: 1})
			if err != nil {
				s.fail(w, err)
				return
			}
			endpoint, _ := url.Parse(source.Endpoint)
			v.SourceCards = append(v.SourceCards, sourceCard{Source: source, Host: endpoint.Host, Tracks: items.Total})
		}
	}
	v.ReturnTo = r.URL.RequestURI()
	for n, item := range v.Catalogue.Items {
		v.Rows = append(v.Rows, track(item, v.Sources, q.Offset+n+1, true))
	}
	s.render(w, r, v)
}
