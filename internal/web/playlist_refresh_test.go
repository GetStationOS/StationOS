package web

import (
	"context"
	"encoding/json"
	"net/url"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
	"time"
)

func TestPlaylistRefreshAndExclusionJourney(t *testing.T) {
	h, stations, sourceRepo, cat := setupWeb(t, "")
	ctx := context.Background()
	if err := stations.Save(ctx, station.Station{ID: "s", Name: "Radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	source, err := (sources.Service{Repo: sourceRepo}).Save(ctx, sources.Config{Name: "Library", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	length := 3 * time.Minute
	a := content.AudioItem{ID: content.ID(source.ID, "a"), SourceID: source.ID, SourceItemID: "a", Title: "Alpha", Genre: "Jazz", Album: "Album", Duration: &length, Kind: content.Track, Origin: content.Original}
	selectTestSources(t, cat.DB)
	if err = cat.Replace(ctx, source, []content.AudioItem{a}); err != nil {
		t.Fatal(err)
	}
	repo := persistence.Playlists{DB: stations.DB}
	svc := playlists.Service{Repo: repo, Stations: stations}
	pool, err := svc.Save(ctx, playlists.Playlist{StationID: "s", Name: "Jazz"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, pool.ID, playlists.Rule{Kind: "genre", Value: "Jazz"}); err != nil {
		t.Fatal(err)
	}
	path := "/producer/playlists/" + pool.ID
	token := csrf(t, h)
	trackPage := request(h, "GET", path+"/tracks", nil, nil).Body.String()
	if !strings.Contains(trackPage, path+"/refresh") || !strings.Contains(trackPage, "Exclude Alpha") {
		t.Fatal(trackPage)
	}
	removed := request(h, "POST", path+"/remove", url.Values{"csrf": {token}, "item": {a.ID}, "return_to": {path + "/tracks?page=1&per_page=50"}}, nil)
	if removed.Code != 303 {
		t.Fatal(removed.Code, removed.Body.String())
	}
	excluded := request(h, "GET", removed.Header().Get("Location"), nil, nil).Body.String()
	if !strings.Contains(excluded, `class="playlist-track-excluded"`) || !strings.Contains(excluded, "Re-include Alpha") || !strings.Contains(excluded, "0 tracks") {
		t.Fatal(excluded)
	}
	restore := request(h, "POST", path+"/restore", url.Values{"csrf": {token}, "item": {a.ID}, "return_to": {path + "/tracks?per_page=50"}}, nil)
	restored := request(h, "GET", restore.Header().Get("Location"), nil, nil).Body.String()
	if strings.Contains(restored, `class="playlist-track-excluded"`) || !strings.Contains(restored, "Exclude Alpha") {
		t.Fatal(restored)
	}
	// Search retains excluded recordings, and mutations return one row plus
	// independently updated totals without a main-content navigation or notice.
	searched := request(h, "GET", path+"/tracks?q=Alpha&per_page=50", nil, map[string]string{"HX-Request": "true", "HX-Target": "playlist-track-results"})
	if searched.Code != 200 || !strings.Contains(searched.Body.String(), "Exclude Alpha") || strings.Contains(searched.Body.String(), "<h1") {
		t.Fatal(searched.Code, searched.Body.String())
	}
	for _, action := range []string{"remove", "restore"} {
		changed := request(h, "POST", path+"/"+action, url.Values{"csrf": {token}, "item": {a.ID}, "return_to": {path + "/tracks?q=Alpha&per_page=50"}}, map[string]string{"HX-Request": "true", "HX-Target": "playlist-track-" + a.ID})
		body := changed.Body.String()
		if changed.Code != 200 || changed.Header().Get("HX-Location") != "" || !strings.Contains(body, `hx-swap-oob="innerHTML"`) || strings.Contains(body, `id="playlist-track-results"`) || strings.Contains(body, `data-event-notice`) {
			t.Fatal(changed.Code, body)
		}
		if strings.Contains(body, `class="playlist-track-excluded"`) != (action == "remove") {
			t.Fatal("row exclusion state did not update", body)
		}
		if !strings.Contains(body, "q=Alpha") {
			t.Fatal("lost search", body)
		}
	}
	b := a
	b.ID = content.ID(source.ID, "b")
	b.SourceItemID = "b"
	b.Title = "Beta"
	b.Album = "New album"
	selectTestSources(t, cat.DB)
	if err = cat.Replace(ctx, source, []content.AudioItem{a, b}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"1 new track · 1 new album", "No new albums or tracks"} {
		response := request(h, "POST", path+"/refresh", url.Values{"csrf": {token}, "return_to": {path + "/tracks?per_page=50"}}, map[string]string{"HX-Request": "true"})
		var location struct{ Path string }
		if err = json.Unmarshal([]byte(response.Header().Get("HX-Location")), &location); err != nil {
			t.Fatal(err, response.Body.String())
		}
		if !strings.Contains(location.Path, "per_page=50") {
			t.Fatal("page size lost", location.Path)
		}
		page := request(h, "GET", location.Path, nil, nil).Body.String()
		if !strings.Contains(page, expected) {
			t.Fatal(expected, page)
		}
	}
	c := a
	c.ID = content.ID(source.ID, "c")
	c.SourceItemID = "c"
	c.Title = "Gamma"
	c.Album = "Latest album"
	selectTestSources(t, cat.DB)
	if err = cat.Replace(ctx, source, []content.AudioItem{a, b, c}); err != nil {
		t.Fatal(err)
	}
	saved := request(h, "POST", path+"/selections", url.Values{"csrf": {token}, "genre": {"Jazz"}}, nil)
	if saved.Code != 303 {
		t.Fatal(saved.Code, saved.Body.String())
	}
	savedPage := request(h, "GET", saved.Header().Get("Location"), nil, nil).Body.String()
	if !strings.Contains(savedPage, "Selections saved. Playlist refreshed: 1 new track · 1 new album.") {
		t.Fatal("save did not report its refresh", savedPage)
	}
	if denied := request(h, "POST", path+"/refresh", url.Values{}, nil); denied.Code != 403 {
		t.Fatal("refresh bypassed CSRF", denied.Code)
	}
	if missing := request(h, "POST", "/producer/playlists/missing/refresh", url.Values{"csrf": {token}}, nil); missing.Code != 404 {
		t.Fatal(missing.Code)
	}
}

func TestActionNoticesUseDismissibleStatusWithoutTopBanner(t *testing.T) {
	h, _, _, _ := setupWeb(t, "")
	w := request(h, "GET", "/producer?notice=station-deleted", nil, nil)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `data-event-notice`) || !strings.Contains(body, `Dismiss notification`) || strings.Contains(body, `class="success notice"`) {
		t.Fatal(w.Code, body)
	}
	if !strings.Contains(body, `data-feedback-message`) || !strings.Contains(body, `Dismiss error`) {
		t.Fatal("global errors cannot be dismissed", body)
	}
}
