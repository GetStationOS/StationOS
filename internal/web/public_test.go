package web

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

func TestPublicDirectoryVisibilityAndAuthentication(t *testing.T) {
	h, repo, _, _ := setupWeb(t, "secret")
	ctx := context.Background()
	for _, visibility := range []station.Visibility{station.Listed, station.Unlisted, station.Private} {
		if err := repo.Save(ctx, station.Station{ID: string(visibility), Name: "Station " + string(visibility), StreamSlug: string(visibility), Description: "<script>unsafe</script>", Timezone: "UTC", Visibility: visibility}); err != nil {
			t.Fatal(err)
		}
	}
	home := request(h, "GET", "/", nil, nil)
	if home.Code != 200 || !strings.Contains(home.Body.String(), "Station listed") || strings.Contains(home.Body.String(), "Station unlisted") || strings.Contains(home.Body.String(), "Station private") {
		t.Fatal(home.Code, home.Body.String())
	}
	if !strings.Contains(home.Body.String(), "&lt;script&gt;unsafe&lt;/script&gt;") {
		t.Fatal("description not escaped")
	}
	for _, slug := range []string{"listed", "unlisted"} {
		page := request(h, "GET", "/radio/"+slug, nil, nil)
		if page.Code != 200 || !strings.Contains(page.Body.String(), "/streams/"+slug) {
			t.Fatal(page.Code, page.Body.String())
		}
		for _, forbidden := range []string{"name=\"csrf\"", "Create station", "/producer/playlists", "/producer/sources", "/producer/djs"} {
			if strings.Contains(page.Body.String(), forbidden) {
				t.Fatal("public page exposes management", forbidden)
			}
		}
	}
	for _, path := range []string{"/radio/private", "/radio/private/live", "/radio/private/player", "/radio/private/artwork/a", "/radio/private/presenters/default-pop/photo", "/station-logos/private.jpg"} {
		if w := request(h, "GET", path, nil, nil); w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	for _, path := range []string{"/producer", "/producer/stations/listed/settings", "/producer/sources", "/producer/djs", "/producer/stations/events", "/producer/api/v1/status", "/stations/listed"} {
		if w := request(h, "GET", path, nil, nil); w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
	for _, path := range []string{"/static/app.js", "/static/app.css", "/static/htmx.min.js"} {
		if w := request(h, "GET", path, nil, nil); w.Code != 200 {
			t.Fatal(path, w.Code)
		}
	}
	if w := request(h, "POST", "/radio/listed", url.Values{}, nil); w.Code != 401 {
		t.Fatal("public writes bypass auth", w.Code)
	}
}

func TestPublicPublicationFormAndStableAddress(t *testing.T) {
	h, repo, _, _ := setupWeb(t, "")
	token := csrf(t, h)
	fields := url.Values{"csrf": {token}, "name": {"Public station"}, "timezone": {"Europe/London"}, "visibility": {"listed"}}
	if w := request(h, "POST", "/producer/station", fields, nil); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	stations, err := repo.List(context.Background())
	if err != nil || len(stations) != 1 {
		t.Fatal(stations, err)
	}
	st := stations[0]
	if st.Publication() != station.Listed {
		t.Fatal(st)
	}
	fields.Set("id", st.ID)
	fields.Set("name", "Renamed station")
	if w := request(h, "POST", "/producer/station", fields, nil); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	renamed, err := repo.Get(context.Background(), st.ID)
	if err != nil || renamed.StreamSlug != st.StreamSlug {
		t.Fatal(renamed, err)
	}
	fields.Set("visibility", "invalid")
	if w := request(h, "POST", "/producer/station", fields, nil); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	unchanged, _ := repo.Get(context.Background(), st.ID)
	if unchanged.Publication() != station.Listed {
		t.Fatal("invalid form changed visibility")
	}
	fields.Del("visibility")
	if w := request(h, "POST", "/producer/station", fields, nil); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	unchanged, _ = repo.Get(context.Background(), st.ID)
	if unchanged.Publication() != station.Listed {
		t.Fatal("older form unpublished station")
	}
	page := request(h, "GET", "/radio/"+st.StreamSlug, nil, map[string]string{"HX-Request": "true"})
	if strings.Contains(page.Body.String(), "<!doctype") || !strings.Contains(page.Body.String(), "<title>Renamed station") {
		t.Fatal("invalid public fragment", page.Body.String())
	}
	restored := request(h, "GET", "/radio/"+st.StreamSlug, nil, map[string]string{"HX-Request": "true", "HX-History-Restore-Request": "true"})
	if !strings.Contains(restored.Body.String(), "<!doctype") {
		t.Fatal("history lost shell")
	}
}

func TestPublicObserversNeverStartBroadcastAndPublicationRevokesAccess(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "public.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := persistence.Stations{DB: db}
	src := persistence.Sources{DB: db}
	cat := persistence.Catalogue{DB: db}
	st := station.Station{ID: "radio", Name: "Public radio", Timezone: "UTC", StreamSlug: "public-radio", Visibility: station.Listed}
	if err := repo.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	cfg := persistence.Broadcast{DB: repo.DB}
	m := broadcast.New(cfg, tickingEncoder{}, slog.New(slog.NewTextHandler(io.Discard, nil)), broadcast.Options{})
	defer m.Close()
	h, err := New(Dependencies{Stations: repo, Sources: src, Catalogue: cat, Broadcast: m, Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	client := http.Client{Timeout: 10 * time.Second}
	ts := httptest.NewServer(h)
	defer ts.Close()
	for _, path := range []string{"/radio-events", "/radio/public-radio/live", "/radio/public-radio/player"} {
		if w := request(h, "HEAD", path, nil, nil); w.Code != 200 || w.Body.Len() != 0 {
			t.Fatal("HEAD observer did not finish without data", path, w.Code)
		}
		response, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatal(path, response.StatusCode)
		}
		scanner := bufio.NewScanner(response.Body)
		if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "event: ") {
			t.Fatal("missing initial event", scanner.Err())
		}
		if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "data: ") {
			t.Fatal("missing event data")
		}
		var html string
		if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &html); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(html, "/producer/stations/") {
			t.Fatal("public event has protected artwork")
		}
		response.Body.Close()
	}
	if state := m.State(st.ID); state.Listeners != 0 || state.Status != "Dormant" {
		t.Fatal("observer started audio", state)
	}
	if w := request(h, "HEAD", "/streams/public-radio", nil, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	// Keep a metadata observer connected while publication is revoked.
	response, err := client.Get(ts.URL + "/radio/public-radio/player")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if scanner.Text() == "" {
			break
		}
	}
	st.Visibility = station.Private
	if err := repo.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	m.Changed()
	if !scanner.Scan() || scanner.Text() != "event: unavailable" {
		t.Fatal("public observer did not close on revocation", scanner.Text(), scanner.Err())
	}
	for _, path := range []string{"/radio/public-radio", "/radio/public-radio/player", "/radio/public-radio/live", "/streams/public-radio", "/station-logos/radio.jpg"} {
		w := request(h, "HEAD", path, nil, nil)
		if w.Code != 404 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("HEAD", "/streams/public-radio", nil)
	r.SetBasicAuth("admin", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("operator cannot preview private stream", w.Code)
	}
	preview := request(h, "HEAD", "/producer/streams/public-radio", nil, nil)
	if preview.Code != 401 {
		t.Fatal("private preview bypassed management auth", preview.Code)
	}
	openProducer, err := New(Dependencies{Stations: repo, Broadcast: m})
	if err != nil {
		t.Fatal(err)
	}
	if w := request(openProducer, "HEAD", "/producer/streams/public-radio", nil, nil); w.Code != 200 {
		t.Fatal("optional-login producer cannot preview", w.Code)
	}
	if w := request(openProducer, "HEAD", "/streams/public-radio", nil, nil); w.Code != 404 {
		t.Fatal("private public feed available", w.Code)
	}
}

func TestLegacyManagementBookmarksRedirect(t *testing.T) {
	h, _, _, _ := setupWeb(t, "")
	w := request(h, "GET", "/sources?notice=saved", nil, nil)
	if w.Code != 307 || w.Header().Get("Location") != "/producer/sources?notice=saved" {
		t.Fatal(w.Code, w.Header())
	}
}

func TestPublicShowPresenterPortraitSources(t *testing.T) {
	h, stations, _, _ := setupWeb(t, "secret")
	ctx := context.Background()
	targetDate := time.Now().UTC().AddDate(0, 0, 1)
	targetDay := (int(targetDate.Weekday()) + 6) % 7
	if err := stations.Save(ctx, station.Station{ID: "s", Name: "Radio", StreamSlug: "radio", Timezone: "UTC", Visibility: station.Listed}); err != nil {
		t.Fatal(err)
	}
	if err := (persistence.Playlists{DB: stations.DB}).Save(ctx, playlists.Playlist{ID: "p", StationID: "s", Name: "Pool"}); err != nil {
		t.Fatal(err)
	}
	if err := (persistence.Schedule{DB: stations.DB}).Save(ctx, schedule.Show{ID: "show", StationID: "s", Name: "Show", Description: "Afternoon favourites", DJID: "default-pop", PlaylistID: "p", Day: targetDay, Start: 0, End: 60}); err != nil {
		t.Fatal(err)
	}
	for _, portrait := range []string{"/static/djs/alex-bright.jpg", "https://example.com/portrait.jpg", "/djs/default-pop/photo?v=" + strings.Repeat("a", 64)} {
		if _, err := stations.DB.Exec(`UPDATE djs SET portrait_url=? WHERE id='default-pop'`, portrait); err != nil {
			t.Fatal(err)
		}
		want := portrait
		if strings.HasPrefix(portrait, "/djs/") {
			want = "/radio/radio/presenters/default-pop/photo"
		}
		programme := request(h, "GET", "/radio/radio?day="+targetDate.Format("2006-01-02"), nil, nil)
		if programme.Code != 200 || !strings.Contains(programme.Body.String(), `class="schedule-presenter-photo" src="`+want+`"`) {
			t.Fatal(programme.Code, programme.Body.String())
		}
		if strings.Contains(programme.Body.String(), ">Open stream</a>") {
			t.Fatal("redundant stream action")
		}
		page := request(h, "GET", "/radio/radio/shows/show", nil, map[string]string{"HX-Request": "true", "HX-Target": "show-details-show"})
		if page.Code != 200 || !strings.Contains(page.Body.String(), `src="`+want+`"`) || !strings.Contains(page.Body.String(), "has-presenter-photo") || !strings.Contains(page.Body.String(), "Afternoon favourites") {
			t.Fatal(page.Code, page.Body.String())
		}
	}
	photo := []byte{0xff, 0xd8, 0xff, 0xd9}
	if _, err := stations.DB.Exec(`INSERT INTO dj_photos(dj_id,data) VALUES('default-pop',?)`, photo); err != nil {
		t.Fatal(err)
	}
	got := request(h, "GET", "/radio/radio/presenters/default-pop/photo", nil, nil)
	if got.Code != 200 || got.Header().Get("Content-Type") != "image/jpeg" || got.Body.String() != string(photo) {
		t.Fatal(got.Code, got.Body.String())
	}
	if got := request(h, "GET", "/radio/radio/presenters/default-classical/photo", nil, nil); got.Code != 404 {
		t.Fatal("unscheduled portrait exposed", got.Code)
	}
	st, _ := stations.Get(ctx, "s")
	st.Visibility = station.Private
	if err := stations.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	if got := request(h, "GET", "/radio/radio/presenters/default-pop/photo", nil, nil); got.Code != 404 {
		t.Fatal("private portrait exposed", got.Code)
	}
}
