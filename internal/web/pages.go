package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/playlists"
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
	DJIndicators                                           []djIndicator
	Broadcast                                              broadcast.State
	Station                                                station.Station
	Playlists                                              int
	PlaylistID, PlaylistName, ProgrammeTime, FollowingTime string
	CurrentTrack, NextTrack                                cardTrack
}
type sourceCard struct {
	Source sources.Config
	Host   string
	Tracks int
}

func (s *Server) baseView(r *http.Request) pageView {
	v := pageView{Page: r.URL.Path, CSRF: s.csrf}
	v.BroadcastEnabled = s.d.Broadcast != nil

	v.Notice = map[string]string{"station-deleted": "Station deleted.", "playlist-deleted": "Playlist deleted.", "station-saved": "Station saved.", "playlist-saved": "Playlist saved.", "selections-saved": "Selections saved.", "tracks-added": "Tracks added to the playlist.", "track-removed": "Track excluded from this playlist.", "source-saved": "Source saved.", "imported": "Library refreshed."}[r.URL.Query().Get("notice")]
	return v
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/producer":
		v := stationDirectoryView{pageView: s.baseView(r)}
		v.Title = "Your stations"
		if err := s.loadStationCards(r, &v); err != nil {
			s.fail(w, err)
			return
		}
		s.renderPage(w, r, http.StatusOK, "stations-page", v)
	case "/producer/station", "/producer/stations/new":
		v := stationPageView{pageView: s.baseView(r)}
		v.Title = "Create a station"
		s.renderPage(w, r, http.StatusOK, "new-station-page", v)
	case "/producer/sources":
		s.sourcesPage(w, r)
	case "/producer/sources/new":
		s.newSourcePage(w, r)
	case "/producer/content":
		s.cataloguePage(w, r)
	}
}

func (s *Server) cataloguePage(w http.ResponseWriter, r *http.Request) {
	v := cataloguePageView{pageView: s.baseView(r)}
	v.Title = "Browse tracks"
	var err error
	if err = s.loadCatalogueChoices(r.Context(), &v.catalogueChoices); err != nil {
		s.fail(w, err)
		return
	}
	v.Sources, err = s.d.Sources.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if err = s.loadPlaylistOptions(r.Context(), &v.playlistChoices, station.Station{}); err != nil {
		s.fail(w, err)
		return
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
	v.ReturnTo = r.URL.RequestURI()
	for n, item := range v.Catalogue.Items {
		v.Rows = append(v.Rows, track(item, v.Sources, q.Offset+n+1, true))
	}
	s.renderPage(w, r, http.StatusOK, "catalogue-page", v)
}
