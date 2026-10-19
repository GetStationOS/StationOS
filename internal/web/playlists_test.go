package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/sources"
)

func TestPlaylistWebJourney(t *testing.T) {
	h, st, src, cat := setupWeb(t, "")
	token := csrf(t, h)
	ctx := context.Background()
	request(h, "POST", "/producer/station", url.Values{"csrf": {token}, "name": {"Jazz station"}, "timezone": {"UTC"}}, nil)
	stations, _ := st.List(ctx)
	c, err := (sources.Service{Repo: src}).Save(ctx, sources.Config{Name: "Shared library", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	d := 3 * time.Minute
	a := content.AudioItem{ID: content.ID(c.ID, "track"), SourceID: c.ID, SourceItemID: "track", Title: "Jazz & <title>", Artist: "Artist", Album: "Album & One", Genre: "Jazz", Duration: &d, Kind: content.Track, Origin: content.Original, Resources: []content.Resource{{URL: "http://server/track", MIME: "audio/flac"}}}
	selectTestSources(t, cat.DB)
	if err = cat.Replace(ctx, c, []content.AudioItem{a}); err != nil {
		t.Fatal(err)
	}
	w := request(h, "POST", "/producer/playlists", url.Values{"csrf": {token}, "station": {stations[0].ID}, "name": {"Jazz rotation"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	location, _ := url.Parse(w.Header().Get("Location"))
	path := location.Path
	if !strings.HasPrefix(path, "/producer/playlists/") {
		t.Fatal(path)
	}
	w = request(h, "POST", path+"/rules", url.Values{"csrf": {token}, "kind": {"genre"}, "value": {"Jazz"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	choice, _ := json.Marshal(albumChoice{SourceID: c.ID, Name: a.Album})
	w = request(h, "POST", path+"/rules", url.Values{"csrf": {token}, "kind": {"album"}, "album": {string(choice)}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", path+"/tracks", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "1 tracks") || !strings.Contains(w.Body.String(), "Jazz &amp; &lt;title&gt;") {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Count(w.Body.String(), `class="pagination-bar"`) != 0 || strings.Contains(w.Body.String(), `data-selection-form`) || strings.Contains(w.Body.String(), `/move`) || strings.Contains(w.Body.String(), `<th>#</th>`) {
		t.Fatal("tracks tab mixes editing tasks or repeats pagination")
	}
	w = request(h, "GET", path, nil, nil)
	if !strings.Contains(w.Body.String(), `data-selection-form`) || strings.Contains(w.Body.String(), `<table>`) || strings.Contains(w.Body.String(), `name="description"`) {
		t.Fatal("selection tab mixes tasks")
	}
	w = request(h, "GET", path+"/settings", nil, map[string]string{"HX-Request": "true"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="description"`) || strings.Contains(w.Body.String(), `data-selection-form`) || strings.Contains(w.Body.String(), `<table>`) {
		t.Fatal("details tab mixes tasks")
	}
	w = request(h, "POST", "/producer/playlists", url.Values{"csrf": {token}, "id": {strings.TrimPrefix(path, "/producer/playlists/")}, "station": {stations[0].ID}, "name": {"Jazz rotation"}, "return_to": {path + "/settings"}}, nil)
	if w.Code != 303 || w.Header().Get("Location") != path+"/settings?notice=playlist-saved" {
		t.Fatal("details save lost tab", w.Header())
	}
	w = request(h, "GET", "/producer/content?genre=Jazz", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "FLAC") || !strings.Contains(w.Body.String(), "3:00") || !strings.Contains(w.Body.String(), "Shared library") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", path+"/remove", url.Values{"csrf": {token}, "item": {a.ID}, "return_to": {path + "/tracks?page=2&per_page=50"}}, nil)
	removed, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || removed.Path != path+"/tracks" || removed.Query().Get("page") != "2" || removed.Query().Get("per_page") != "50" {
		t.Fatal("exclusion lost tab or paging", w.Code, w.Header())
	}
	w = request(h, "GET", path, nil, nil)
	if !strings.Contains(w.Body.String(), "0 tracks") {
		t.Fatal("genre exclusion failed")
	}
	w = request(h, "POST", "/producer/playlists/select", url.Values{"csrf": {token}, "playlist": {strings.TrimPrefix(path, "/producer/playlists/")}, "item": {a.ID}, "return_to": {"https://evil.example"}}, nil)
	if w.Code != 303 || w.Header().Get("Location") != path {
		t.Fatal(w.Code, w.Header())
	}
	w = request(h, "POST", "/producer/playlists/select", url.Values{"csrf": {token}, "playlist": {strings.TrimPrefix(path, "/producer/playlists/")}, "item": {a.ID}, "return_to": {"/producer/content?q=Jazz&page=2"}}, nil)
	u, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || u.Query().Get("playlist") != strings.TrimPrefix(path, "/producer/playlists/") || u.Query().Get("q") != "Jazz" || u.Query().Get("page") != "2" {
		t.Fatal(w.Code, u)
	}
	w = request(h, "GET", path, nil, map[string]string{"HX-Request": "true"})
	if w.Code != 200 || strings.Contains(w.Body.String(), "<!doctype") || !strings.Contains(w.Body.String(), "1 tracks") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/producer/playlists", url.Values{"csrf": {token}, "station": {"missing"}, "name": {"Bad"}}, nil)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	w = request(h, "POST", path+"/move", url.Values{"csrf": {token}, "item": {a.ID}, "direction": {"1"}}, nil)
	if w.Code != 404 {
		t.Fatal("obsolete ordering endpoint still available", w.Code)
	}
	w = request(h, "GET", "/producer/playlists/missing", nil, nil)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
func TestPaginationKeepsFiltersAndClampsPages(t *testing.T) {
	values := url.Values{"q": {"Jazz & Blues"}, "genre": {"Jazz"}, "sort": {"artist"}, "direction": {"desc"}, "per_page": {"25"}, "playlist": {"station-list"}}
	p := paging("/producer/content", values, 3, 25, 731)
	if p.Pages != 30 || p.Start != 51 || p.End != 75 || p.Number != 3 {
		t.Fatal(p)
	}
	u, err := url.Parse(p.Next)
	if err != nil || u.Query().Get("q") != "Jazz & Blues" || u.Query().Get("page") != "4" || u.Query().Get("playlist") != "station-list" || u.Query().Get("direction") != "desc" {
		t.Fatal(u, err)
	}
	p = paging("/producer/content", values, 99, 25, 731)
	if p.Number != 30 || p.End != 731 || p.Next != "" {
		t.Fatal(p)
	}
	for _, v := range []url.Values{{"page": {"0"}}, {"per_page": {"7"}}, {"page": {"bad"}}, {"offset": {"-1"}}} {
		if _, _, err := pageRequest(v); err == nil {
			t.Fatal("invalid paging accepted", v)
		}
	}
	if _, err := catalogueQuery(url.Values{"sort": {"sql injection"}}); err == nil {
		t.Fatal("unrecognised sort accepted")
	}
	if _, err := catalogueQuery(url.Values{"album_choice": {"bad"}}); err == nil {
		t.Fatal("malformed album accepted")
	}
	if playlistReturn("//evil.example/path", "one") != "/producer/playlists/one" {
		t.Fatal("open redirect")
	}
}

func TestPaginationWindowAndSizeChoices(t *testing.T) {
	p := paging("/producer/content", url.Values{"q": {"Jazz"}, "sort": {"artist"}, "per_page": {"25"}}, 10, 25, 1000)
	want := []int{1, 0, 9, 10, 11, 0, 40}
	if len(p.Links) != len(want) {
		t.Fatalf("links: %+v", p.Links)
	}
	for i, n := range want {
		if p.Links[i].Number != n || p.Links[i].Current != (n == 10) {
			t.Fatalf("link %d: %+v", i, p.Links[i])
		}
	}
	for _, choice := range p.Sizes {
		u, err := url.Parse(choice.URL)
		if err != nil || u.Query().Get("q") != "Jazz" || u.Query().Get("sort") != "artist" || u.Query().Get("page") != "1" || u.Query().Get("per_page") != fmt.Sprint(choice.Number) {
			t.Fatalf("size choice: %+v, %v", choice, err)
		}
	}
}
