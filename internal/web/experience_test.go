package web

import (
	"context"
	"encoding/json"
	"net/url"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/sources"
	"strings"
	"testing"
)

func TestStationWorkspaceAndHTMXNavigation(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	token := csrf(t, h)
	w := request(h, "GET", "/producer", nil, nil)
	if !strings.Contains(w.Body.String(), "Your first station starts here") {
		t.Fatal(w.Body.String())
	}
	w = request(h, "POST", "/producer/station", url.Values{"csrf": {token}, "name": {"Jazz <radio>"}, "timezone": {"UTC"}}, map[string]string{"HX-Request": "true"})
	var dest struct{ Path, Target, Swap string }
	if err := json.Unmarshal([]byte(w.Header().Get("HX-Location")), &dest); err != nil {
		t.Fatal(err, w.Header())
	}
	if w.Code != 204 || dest.Target != "#main" || !strings.HasPrefix(dest.Path, "/producer/stations/") || w.Header().Get("HX-Redirect") != "" {
		t.Fatal(w.Code, dest)
	}
	stations, _ := st.List(context.Background())
	path := "/producer/stations/" + stations[0].ID
	w = request(h, "GET", "/producer", nil, nil)
	if !strings.Contains(w.Body.String(), path) || !strings.Contains(w.Body.String(), "Jazz &lt;radio&gt;") || !strings.Contains(w.Body.String(), "Dormant") {
		t.Fatal(w.Body.String())
	}
	w = request(h, "GET", path, nil, map[string]string{"HX-Request": "true"})
	if w.Code != 200 || strings.Contains(w.Body.String(), "<!doctype") || !strings.Contains(w.Body.String(), "Nothing playing") || !strings.Contains(w.Body.String(), "Tracks are selected when listeners connect.") || !strings.Contains(w.Body.String(), "<title>") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", path+"/settings", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="id" value="`+stations[0].ID+`"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", path, nil, map[string]string{"HX-Request": "true", "HX-History-Restore-Request": "true"})
	if !strings.Contains(w.Body.String(), "<!doctype") {
		t.Fatal("history restore needs a complete document")
	}
	if request(h, "GET", "/producer/stations/missing", nil, nil).Code != 404 {
		t.Fatal("missing station")
	}
}
func TestBatchPlaylistCheckboxJourney(t *testing.T) {
	h, st, src, cat := setupWeb(t, "")
	token := csrf(t, h)
	request(h, "POST", "/producer/station", url.Values{"csrf": {token}, "name": {"Mixed radio"}, "timezone": {"UTC"}}, nil)
	stations, _ := st.List(context.Background())
	c, err := (sources.Service{Repo: src}).Save(context.Background(), sources.Config{Name: "Library", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	item := content.AudioItem{ID: content.ID(c.ID, "a"), SourceID: c.ID, SourceItemID: "a", Title: "Track", Genre: "Jazz", Album: "Album", Kind: content.Track, Origin: content.Original}
	selectTestSources(t, cat.DB)
	if err = cat.Replace(context.Background(), c, []content.AudioItem{item}); err != nil {
		t.Fatal(err)
	}
	w := request(h, "POST", "/producer/playlists", url.Values{"csrf": {token}, "station": {stations[0].ID}, "name": {"Mixed"}}, nil)
	location, _ := url.Parse(w.Header().Get("Location"))
	path := location.Path
	choice, _ := json.Marshal(albumChoice{SourceID: c.ID, Name: item.Album})
	w = request(h, "POST", path+"/selections", url.Values{"csrf": {token}, "genre": {"Jazz", "Pop"}, "album": {string(choice)}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", path, nil, nil)
	html := w.Body.String()
	for _, want := range []string{`type="checkbox" name="genre" value="Jazz" checked`, `type="checkbox" name="genre" value="Pop" checked`, `data-selection-form`, `Find an album`, `1 tracks`, `selection kept`} {
		if !strings.Contains(html, want) {
			t.Fatal(want, html)
		}
	}
	w = request(h, "POST", path+"/selections", url.Values{"csrf": {token}, "album": {"not-json"}}, nil)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	w = request(h, "GET", path, nil, nil)
	if !strings.Contains(w.Body.String(), `value="Jazz" checked`) {
		t.Fatal("invalid save lost selections")
	}
	tooMany := url.Values{"csrf": {token}}
	for n := 0; n < 101; n++ {
		tooMany.Add("genre", "Jazz")
	}
	if limit := request(h, "POST", path+"/selections", tooMany, nil); limit.Code != 400 || !strings.Contains(limit.Body.String(), "100 rules") {
		t.Fatal(limit.Code, limit.Body.String())
	}
	w = request(h, "POST", path+"/selections", url.Values{"csrf": {token}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	w = request(h, "GET", path, nil, nil)
	if !strings.Contains(w.Body.String(), "0 tracks") {
		t.Fatal("unchecking all should clear rules")
	}
	w = request(h, "GET", "/producer/content?playlist="+strings.TrimPrefix(path, "/producer/playlists/"), nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Adding tracks to") || !strings.Contains(w.Body.String(), "Back to playlist") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/producer/playlists?station="+stations[0].ID, nil, nil)
	if !strings.Contains(w.Body.String(), "/producer/playlists/new?station="+stations[0].ID) || strings.Contains(w.Body.String(), `name="name"`) || strings.Contains(w.Body.String(), "create-panel") {
		t.Fatal("playlist creation should use a separate form")
	}
	w = request(h, "GET", "/producer/playlists/new?station="+stations[0].ID, nil, nil)
	if !strings.Contains(w.Body.String(), `type="hidden" name="station"`) || !strings.Contains(w.Body.String(), "Station navigation") {
		t.Fatal("station context missing")
	}
}
