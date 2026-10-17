package web

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
	"time"
)

func TestOperationalPagesAndAcknowledgement(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stations := persistence.Stations{DB: db}
	st, err := (station.Service{Repo: stations}).Save(ctx, station.Station{Name: "Test station", Timezone: "Europe/London", Locale: "en-GB"})
	if err != nil {
		t.Fatal(err)
	}
	store := persistence.Operations{DB: db}
	now := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	e := operations.Event{ID: "item", StationID: st.ID, Started: now, Ended: now.Add(time.Minute), Kind: "music", Outcome: "completed", Title: "=1+1", Artist: "<script>alert(1)</script>", Composer: "Composer", AudioBytes: 16000, ShowID: "show", Show: "Breakfast", DJID: "dj", DJ: "Alex"}
	if err = store.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	e.ID = "failure"
	e.Kind = "ai"
	e.Outcome = "observed"
	e.Title = ""
	e.IncidentCode = "speech"
	e.Severity = "warning"
	e.Reason = "Speech preparation failed"
	if err = store.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	h, err := New(Dependencies{Stations: stations, Operations: store, Password: "secret", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	base := "/producer/stations/" + st.ID
	auth := map[string]string{"Authorization": "Basic YWRtaW46c2VjcmV0"}
	for _, suffix := range []string{"/timeline", "/incidents", "/upcoming", "/history.csv", "/operations/version"} {
		w := request(h, "GET", base+suffix, nil, nil)
		if w.Code != 401 {
			t.Fatalf("unprotected %s: %d", suffix, w.Code)
		}
		w = request(h, "GET", base+suffix, nil, auth)
		if suffix == "/upcoming" {
			if w.Code != http.StatusFound || w.Header().Get("Location") != base+"#upcoming" {
				t.Fatalf("upcoming bookmark: %d %s", w.Code, w.Header())
			}
			continue
		}
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", suffix, w.Code, w.Body.String())
		}
		if suffix != "/history.csv" && strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
			t.Fatal("unescaped metadata")
		}
	}
	overview := request(h, "GET", base, nil, auth).Body.String()
	for _, redundant := range []string{">Transmission history</a>", ">Up next</a>", ">1 active incident</a>"} {
		if strings.Contains(overview, redundant) {
			t.Fatal("redundant overview shortcuts", overview)
		}
	}
	if !strings.Contains(overview, "1 active incident. Review the Incidents tab.") || !strings.Contains(overview, `class="overview-live-content" hx-target="#main" hx-swap="innerHTML show:window:top"`) {
		t.Fatal("missing actionable notice or safe navigation target", overview)
	}
	w := request(h, "GET", base+"/timeline?kind=music&from=2026-10-25&until=2026-10-25", nil, auth)
	if !strings.Contains(html.UnescapeString(w.Body.String()), "GMT +00:00") || !strings.Contains(w.Body.String(), "Breakfast") {
		t.Fatal("time or snapshot missing", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Recording credits") || strings.Contains(w.Body.String(), "Composer:") {
		t.Fatal("recording cell contains nonessential credits", w.Body.String())
	}
	headers := map[string]string{"Authorization": auth["Authorization"], "HX-Request": "true"}
	w = request(h, "GET", base+"/timeline", nil, headers)
	if strings.Contains(w.Body.String(), "<!doctype") {
		t.Fatal("fragment returned full shell")
	}
	headers["HX-History-Restore-Request"] = "true"
	w = request(h, "GET", base+"/timeline", nil, headers)
	if !strings.Contains(strings.ToLower(w.Body.String()), "<!doctype") {
		t.Fatal("history restore missing shell")
	}
	w = request(h, "GET", base+"/history.csv?kind=music", nil, auth)
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil || len(rows) != 2 || rows[1][3] != "'=1+1" {
		t.Fatalf("unsafe export %+v %v", rows, err)
	}
	w = request(h, "POST", base+"/incidents/failure/acknowledge", url.Values{}, auth)
	if w.Code != 403 {
		t.Fatalf("CSRF not required: %d", w.Code)
	}
	w = request(h, "GET", base+"/incidents", nil, auth)
	token := regexp.MustCompile(`name="csrf" value="([a-f0-9]+)"`).FindStringSubmatch(w.Body.String())[1]
	w = request(h, "POST", base+"/incidents/failure/acknowledge", url.Values{"csrf": {token}}, auth)
	if w.Code != 303 {
		t.Fatalf("ack failed %d %s", w.Code, w.Body.String())
	}
	incidents, _ := store.Incidents(ctx, st.ID, true)
	if len(incidents) != 1 || incidents[0].Acknowledged.IsZero() {
		t.Fatal("ack incorrectly resolved incident")
	}
	w = request(h, "GET", base+"/timeline?from=bad", nil, auth)
	if w.Code != http.StatusBadRequest {
		t.Fatal("invalid date accepted")
	}
}

func TestHealthyOverviewHasNoOperationalShortcutRow(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stations := persistence.Stations{DB: db}
	st, err := (station.Service{Repo: stations}).Save(ctx, station.Station{Name: "Quiet station", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Dependencies{Stations: stations, Operations: persistence.Operations{DB: db}})
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/live"} {
		w := request(h, "GET", "/producer/stations/"+st.ID+suffix, nil, nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), `class="operational-summary"`) || strings.Contains(w.Body.String(), "0 active incidents") {
			t.Fatal("healthy station has operational clutter", w.Code, w.Body.String())
		}
	}
}

func TestHistoryNotificationsIgnoreProgressAndRespectFilters(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stations := persistence.Stations{DB: db}
	st, err := (station.Service{Repo: stations}).Save(ctx, station.Station{Name: "History", Timezone: "Europe/London"})
	if err != nil {
		t.Fatal(err)
	}
	store := persistence.Operations{DB: db}
	at := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	e := operations.Event{ID: "recording-one", StationID: st.ID, Kind: "music", Outcome: "transmitting", Started: at, Title: "Song one", Artist: "Artist", Show: "Breakfast", ShowID: "monday", DJ: "Alex", DJID: "dj", Playlist: "Morning pool", AudioBytes: 4096, Reason: "Selected from the eligible playlist; selection method was not recorded"}
	if err = store.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	h, err := New(Dependencies{Stations: stations, Operations: store})
	if err != nil {
		t.Fatal(err)
	}
	base := "/producer/stations/" + st.ID
	query := "?kind=music&show=" + url.QueryEscape("name:Breakfast") + "&from=2026-10-16&until=2026-10-16"
	version := func() string {
		t.Helper()
		w := request(h, "GET", base+"/operations/version"+query, nil, nil)
		var result struct{ Version string }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Version == "" {
			t.Fatalf("version: %d %s", w.Code, w.Body.String())
		}
		return result.Version
	}
	renderedVersion := func() string {
		t.Helper()
		w := request(h, "GET", base+"/timeline"+query, nil, nil)
		match := regexp.MustCompile(`data-operations-version="([a-f0-9]+:[0-9]+)"`).FindStringSubmatch(w.Body.String())
		if w.Code != 200 || len(match) != 2 {
			t.Fatal(w.Code, w.Body.String())
		}
		return match[1]
	}
	before := renderedVersion()
	if version() != before {
		t.Fatal("page immediately reports changes")
	}
	e.AudioBytes = 16000 * 100
	if err = store.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	if version() != before || renderedVersion() != before {
		t.Fatal("byte checkpoint reports new history")
	}
	other := e
	other.ID = "speech"
	other.Kind = "speech"
	other.Title = "Spoken introduction"
	if err = store.Save(ctx, other); err != nil {
		t.Fatal(err)
	}
	other.ID = "different-show"
	other.Kind = "music"
	other.Show = "Evening"
	other.Title = "Evening song"
	if err = store.Save(ctx, other); err != nil {
		t.Fatal(err)
	}
	if version() != before {
		t.Fatal("unrelated type/show changed filtered history")
	}
	w := request(h, "GET", base+"/timeline"+query, nil, nil)
	body := html.UnescapeString(w.Body.String())
	if !strings.Contains(body, `class="history-table"`) || !strings.Contains(body, "Morning pool") || !strings.Contains(body, "In progress") {
		t.Fatal("missing producer history", body)
	}
	for _, technical := range []string{"Event ID", "Playlist ID", "Show ID", "DJ ID", "Source / item", "selection method was not recorded", "100.00 s"} {
		if strings.Contains(body, technical) {
			t.Fatal("technical data in producer history", technical)
		}
	}
	if !strings.Contains(body, "kind=music") || !strings.Contains(body, "show=name%3ABreakfast") {
		t.Fatal("update endpoint discarded filters", body)
	}
	e.Outcome = "completed"
	e.Ended = at.Add(2 * time.Minute)
	if err = store.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	if version() == before {
		t.Fatal("completion was not reported")
	}
	if renderedVersion() != version() {
		t.Fatal("refresh repeats update notification")
	}
	// Recurring editions share one named filter and return broadcasts from both days.
	second := e
	second.ID = "recording-two"
	second.ShowID = "tuesday"
	second.Title = "Song two"
	if err = store.Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	w = request(h, "GET", base+"/timeline"+query, nil, nil)
	body = html.UnescapeString(w.Body.String())
	if !strings.Contains(body, `role="img" aria-label="Music"`) || !strings.Contains(body, `role="img" aria-label="Completed"`) {
		t.Fatal("history icons have no accessible labels", body)
	}
	if strings.Count(body, `<option value="name:Breakfast"`) != 1 || !strings.Contains(body, "Song one") || !strings.Contains(body, "Song two") || strings.Contains(body, "Evening song") {
		t.Fatal("recurring show filter is not grouped", body)
	}
	// Archived pages should remain quiet when a newer, unrelated page grows.
	query += "&before=recording-one"
	old := version()
	later := e
	later.ID = "later"
	later.Started = at.Add(time.Hour)
	if err = store.Save(ctx, later); err != nil {
		t.Fatal(err)
	}
	if version() != old {
		t.Fatal("newer event changed archived page")
	}
}

func TestHistoryCardPaginationPreservesFilters(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stations := persistence.Stations{DB: db}
	st, err := (station.Service{Repo: stations}).Save(ctx, station.Station{Name: "Paged station", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	store := persistence.Operations{DB: db}
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for n := 0; n < 60; n++ {
		if err = store.Save(ctx, operations.Event{ID: fmt.Sprintf("page-event-%02d", n), StationID: st.ID, Started: at, Kind: "music", Outcome: "completed", Title: fmt.Sprintf("Song %02d", n), Show: "Breakfast", DJID: "dj", DJ: "Alex"}); err != nil {
			t.Fatal(err)
		}
	}
	h, err := New(Dependencies{Stations: stations, Operations: store})
	if err != nil {
		t.Fatal(err)
	}
	base := "/producer/stations/" + st.ID + "/timeline"
	query := "?q=Song&kind=music&outcome=completed&show=name%3ABreakfast&dj=dj&from=2026-10-16&until=2026-10-16&per_page=25"
	w := request(h, "GET", base+query, nil, nil)
	body := html.UnescapeString(w.Body.String())
	if w.Code != 200 || strings.Count(body, `class="pagination-bar"`) != 1 || !strings.Contains(body, `class="history-card"`) || !strings.Contains(body, "1–25 of 60") || !strings.Contains(body, `aria-label="Events per page"`) {
		t.Fatal("missing history card/pager", w.Code, body)
	}
	match := regexp.MustCompile(`href="([^"]+)" aria-label="Next page"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatal("missing next page")
	}
	next, err := url.Parse(match[1])
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"page": "2", "per_page": "25", "q": "Song", "kind": "music", "outcome": "completed", "show": "name:Breakfast", "dj": "dj", "from": "2026-10-16", "until": "2026-10-16", "anchor": "page-event-59"} {
		if next.Query().Get(key) != value {
			t.Fatalf("lost %s: %s", key, next)
		}
	}
	w = request(h, "GET", match[1], nil, nil)
	body = html.UnescapeString(w.Body.String())
	if w.Code != 200 || !strings.Contains(body, "26–50 of 60") || !strings.Contains(body, "Song 34") || strings.Contains(body, "Song 59") || !strings.Contains(body, `aria-label="Previous page"`) {
		t.Fatal("incorrect second page", w.Code, body)
	}
	w = request(h, "GET", base+query+"&page=999", nil, nil)
	if w.Code != 200 || !strings.Contains(html.UnescapeString(w.Body.String()), "51–60 of 60") {
		t.Fatal("page not clamped", w.Code, w.Body.String())
	}
	w = request(h, "GET", base+"?per_page=50&kind=music", nil, nil)
	if w.Code != 200 || !strings.Contains(html.UnescapeString(w.Body.String()), "1–50 of 60") {
		t.Fatal("page size not applied")
	}
	w = request(h, "GET", base+"?page=0", nil, nil)
	if w.Code != 400 {
		t.Fatal("invalid page accepted", w.Code)
	}
}
