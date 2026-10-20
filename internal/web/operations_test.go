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
	for _, suffix := range []string{"/timeline", "/incidents", "/upcoming", "/history.csv", "/operations/version", "/operations/indicator"} {
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
	if !strings.Contains(overview, "1 incident needs review. Review the Incidents tab.") || !strings.Contains(overview, `class="overview-live-content" hx-target="#main" hx-swap="innerHTML show:window:top"`) {
		t.Fatal("missing actionable notice or safe navigation target", overview)
	}
	w := request(h, "GET", base+"/timeline?kind=music&from=2026-10-25&until=2026-10-25", nil, auth)
	if !strings.Contains(html.UnescapeString(w.Body.String()), "GMT +00:00") || !strings.Contains(w.Body.String(), "Breakfast") {
		t.Fatal("time or snapshot missing", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Review the Incidents tab.") || !strings.Contains(w.Body.String(), `aria-label="1 active incident"`) {
		t.Fatal("timeline must use menu indicator instead of incident banner", w.Body.String())
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
	if !strings.Contains(w.Body.String(), `class="incident-table"`) || !strings.Contains(w.Body.String(), `class="incident-menu-indicator"`) || !strings.Contains(w.Body.String(), `aria-label="1 active incident"`) {
		t.Fatal("missing incident table or menu indicator", w.Body.String())
	}
	partial := request(h, "GET", base+"/incidents", nil, map[string]string{"Authorization": auth["Authorization"], "HX-Request": "true", "HX-Target": "incident-results"})
	if partial.Code != 200 || !strings.Contains(partial.Body.String(), `id="incident-results"`) || strings.Contains(partial.Body.String(), `aria-label="Station navigation"`) {
		t.Fatal("incident refresh replaced workspace", partial.Code, partial.Body.String())
	}
	token := regexp.MustCompile(`name="csrf" value="([a-f0-9]+)"`).FindStringSubmatch(w.Body.String())[1]
	w = request(h, "POST", base+"/incidents/failure/acknowledge", url.Values{"csrf": {token}}, auth)
	if w.Code != 303 {
		t.Fatalf("ack failed %d %s", w.Code, w.Body.String())
	}
	incidents, _ := store.Incidents(ctx, st.ID, true)
	if len(incidents) != 1 || incidents[0].Acknowledged.IsZero() {
		t.Fatal("ack incorrectly resolved incident")
	}
	indicator := request(h, "GET", base+"/operations/indicator", nil, auth)
	if strings.TrimSpace(indicator.Body.String()) != "" {
		t.Fatal("reviewed incident still flagged", indicator.Body.String())
	}
	for _, suffix := range []string{"", "/live"} {
		reviewed := request(h, "GET", base+suffix, nil, auth)
		if reviewed.Code != 200 || strings.Contains(reviewed.Body.String(), `class="operational-summary"`) {
			t.Fatal("reviewed incident still warns on overview", suffix, reviewed.Code, reviewed.Body.String())
		}
	}
	e.ID, e.Kind, e.Recover = "speech-recovered", "recovery", true
	if err = store.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	indicator = request(h, "GET", base+"/operations/indicator", nil, auth)
	if strings.TrimSpace(indicator.Body.String()) != "" {
		t.Fatal("recovered incident still flagged", indicator.Body.String())
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
	if !strings.Contains(body, `role="img" aria-label="Music"`) || !strings.Contains(body, `aria-label="Completed" title="Completed"`) {
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
	if w.Code != 200 || strings.Count(body, `class="pagination-bar"`) != 1 || !strings.Contains(body, `class="panel results-panel history-card"`) || !strings.Contains(body, "1–25 of 60") || !strings.Contains(body, `aria-label="Events per page"`) {
		t.Fatal("missing history card/pager", w.Code, body)
	}
	partial := request(h, "GET", base+query, nil, map[string]string{"HX-Request": "true", "HX-Target": "history-results"})
	fragment := partial.Body.String()
	if partial.Code != 200 || !strings.Contains(fragment, `id="history-results"`) || !strings.Contains(fragment, `data-operations-refresh disabled`) || !strings.Contains(fragment, `data-operations-version=`) || strings.Contains(fragment, `class="operations-filters`) || strings.Contains(fragment, `<html`) {
		t.Fatal("refresh must return only results and a fresh polling baseline", partial.Code, fragment)
	}
	match := regexp.MustCompile(`href="([^"]+)"[^>]*aria-label="Next page"`).FindStringSubmatch(body)
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
	next.Fragment = "" // Browsers do not send URL fragments to the server.
	w = request(h, "GET", next.String(), nil, nil)
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

func TestProducerHistoryTimeRange(t *testing.T) {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, start, end, wantStart, wantEnd string
	}{
		{"same day", "2026-10-17T19:00:00+01:00", "2026-10-17T19:03:30+01:00", "19:00:00", "19:03:30"},
		{"midnight", "2026-10-17T23:58:00+01:00", "2026-10-18T00:02:00+01:00", "23:58:00", "18 Oct 00:02:00"},
		{"clock change", "2026-10-25T01:58:00+01:00", "2026-10-25T01:02:00Z", "01:58:00 BST", "01:02:00 GMT"},
		{"no end", "2026-10-17T19:00:00+01:00", "", "19:00:00", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, _ := time.Parse(time.RFC3339, tc.start)
			var end time.Time
			if tc.end != "" {
				end, _ = time.Parse(time.RFC3339, tc.end)
			}
			v := producerEvent(operations.Event{Kind: "music", Started: start, Ended: end}, loc)
			if v.StartTime != tc.wantStart || v.EndTime != tc.wantEnd {
				t.Fatalf("range %q → %q; want %q → %q", v.StartTime, v.EndTime, tc.wantStart, tc.wantEnd)
			}
		})
	}
}

func TestProducerObservedEventStatus(t *testing.T) {
	for _, tc := range []struct {
		kind, severity, label, style string
		recover                      bool
	}{
		{"failure", "warning", "Error", "error", false},
		{"unavailable", "critical", "Error", "error", false},
		{"ai", "warning", "DJ preparation unavailable", "error", false},
		{"stall", "warning", "Warning", "warning", false},
		{"recovery", "warning", "Recovered", "recovered", true},
		{"started", "", "Station activity", "observed", false},
	} {
		v := producerEvent(operations.Event{Kind: tc.kind, Outcome: "observed", Severity: tc.severity, Recover: tc.recover}, time.UTC)
		if v.OutcomeLabel != tc.label || v.OutcomeStyle != tc.style || (tc.label == "Recovered" && v.OutcomeIcon != "completed") || (tc.label == "Error" && v.OutcomeIcon != "error") || (tc.label == "Warning" && v.OutcomeIcon != "warning") {
			t.Fatalf("%s: label %q style %q", tc.kind, v.OutcomeLabel, v.OutcomeStyle)
		}
	}
}

func TestIncidentTablePagination(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stations := persistence.Stations{DB: db}
	st, err := (station.Service{Repo: stations}).Save(ctx, station.Station{Name: "Incidents", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	store := persistence.Operations{DB: db}
	for n := 0; n < 32; n++ {
		err = store.Save(ctx, operations.Event{ID: fmt.Sprintf("incident-page-%02d", n), StationID: st.ID, Kind: "failure", Outcome: "observed", IncidentCode: fmt.Sprintf("source-%02d", n), Started: time.Date(2026, 10, 17, 12, n, 0, 0, time.UTC), Severity: "warning", Reason: fmt.Sprintf("Source unavailable %02d", n)})
		if err != nil {
			t.Fatal(err)
		}
	}
	h, err := New(Dependencies{Stations: stations, Operations: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query, summary string
		rows           int
	}{{"", "Showing 1–25 of 32", 25}, {"?page=2", "Showing 26–32 of 32", 7}} {
		w := request(h, "GET", "/producer/stations/"+st.ID+"/incidents"+tc.query, nil, nil)
		body := html.UnescapeString(w.Body.String())
		if w.Code != 200 || !strings.Contains(body, tc.summary) || strings.Count(body, `<tr id="incident-`) != tc.rows || strings.Count(body, `class="pagination-bar"`) != 1 || !strings.Contains(body, `title="Mark as reviewed"`) {
			t.Fatalf("incident pagination: %d %s", w.Code, body)
		}
	}
}

func TestIncidentViewsAndReviewPreserveHistory(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stations := persistence.Stations{DB: db}
	st, err := (station.Service{Repo: stations}).Save(ctx, station.Station{Name: "Review", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	store := persistence.Operations{DB: db}
	for _, id := range []string{"active", "old"} {
		if err = store.Save(ctx, operations.Event{ID: id, StationID: st.ID, Kind: "failure", Outcome: "observed", Started: time.Now(), IncidentCode: id, Severity: "critical", Reason: id + " source unavailable"}); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.Save(ctx, operations.Event{ID: "recovered", StationID: st.ID, Kind: "recovery", Outcome: "observed", Started: time.Now(), IncidentCode: "old", Recover: true}); err != nil {
		t.Fatal(err)
	}
	h, err := New(Dependencies{Stations: stations, Operations: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	base := "/producer/stations/" + st.ID + "/incidents"
	w := request(h, "GET", base, nil, nil)
	body := w.Body.String()
	if !strings.Contains(body, "active source unavailable") || strings.Contains(body, "old source unavailable") || strings.Contains(body, "/delete") {
		t.Fatal("default must show only active incidents", body)
	}
	token := regexp.MustCompile(`name="csrf" value="([a-f0-9]+)"`).FindStringSubmatch(body)[1]
	w = request(h, "POST", base+"/active/acknowledge", url.Values{"csrf": {token}, "return_to": {base + "?review=needed&per_page=50"}}, nil)
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "review=needed") {
		t.Fatal("lost review filter", w.Code, w.Header())
	}
	for _, tc := range []struct {
		query       string
		active, old bool
	}{{"", true, false}, {"?review=needed", false, true}, {"?review=all", true, true}} {
		w = request(h, "GET", base+tc.query, nil, nil)
		body = w.Body.String()
		if w.Code != 200 || strings.Contains(body, "active source unavailable") != tc.active || strings.Contains(body, "old source unavailable") != tc.old {
			t.Fatalf("wrong incidents for %s: %d %s", tc.query, w.Code, body)
		}
	}
	indicator := request(h, "GET", "/producer/stations/"+st.ID+"/operations/indicator", nil, nil)
	if strings.TrimSpace(indicator.Body.String()) != "" {
		t.Fatal("reviewed incident still warns in menu", indicator.Body.String())
	}
	rows, err := store.Incidents(ctx, st.ID, false)
	if err != nil || len(rows) != 2 {
		t.Fatal("incident history changed", rows, err)
	}
	history, err := store.Events(ctx, operations.Filter{StationID: st.ID})
	if err != nil || len(history) != 3 {
		t.Fatal("transmission history changed", history, err)
	}
	w = request(h, "GET", base+"?review=invalid", nil, nil)
	if w.Code != 400 {
		t.Fatal("invalid review filter accepted")
	}
}

func TestSelectionFeedbackIsVisibleRoutineActivity(t *testing.T) {
	event := producerEvent(operations.Event{Kind: "selection", Outcome: "observed", Title: "DJ selected · Cheryl", Reason: "The DJ choice passed playlist and timing checks and was queued next.", Started: time.Now()}, time.UTC)
	if event.TypeLabel != "Track selection" || event.Notes == "" || event.OutcomeStyle == "warning" || event.OutcomeStyle == "error" {
		t.Fatal(event)
	}
}

func TestRoutineHandoverIsNeutralIncludingOlderWarningRecords(t *testing.T) {
	for _, kind := range []string{"handover", "started", "stopped"} {
		row := producerEvent(operations.Event{Kind: kind, Outcome: "observed", Severity: "warning", Started: time.Now()}, time.UTC)
		if row.OutcomeStyle == "warning" || row.OutcomeIcon == "warning" || row.Severity != "info" {
			t.Fatal("routine activity presented as warning", row)
		}
	}
}

func TestActualHandoverErrorRetainsErrorState(t *testing.T) {
	row := producerEvent(operations.Event{Kind: "handover", Outcome: "observed", Severity: "critical", Started: time.Now()}, time.UTC)
	if row.OutcomeStyle != "error" || row.OutcomeIcon != "error" {
		t.Fatal("genuine error was hidden", row)
	}
}

func TestCurationRecoveryNamesOneSuccessfulSelection(t *testing.T) {
	for _, code := range []string{"curation", "episode-plan:show:2026-10-20T09:00:00Z"} {
		event := operations.Event{Kind: "recovery", Recover: true, Outcome: "observed", IncidentCode: code, Reason: "Episode planning recovered; a validated running order is ready.", Started: time.Now()}
		row := producerEvent(event, time.UTC)
		if row.TypeLabel != "DJ selection succeeded" || row.OutcomeLabel != "Selection succeeded" || row.Reason != "One track choice passed validation. Queue readiness is shown in the episode view." {
			t.Fatal(row)
		}
		if row.Notes != row.Reason {
			t.Fatal("timeline retained misleading readiness note", row)
		}
		if !row.Recover || row.IncidentCode != code {
			t.Fatal("recovery semantics changed", row)
		}
	}
	row := producerEvent(operations.Event{Kind: "recovery", Recover: true, Outcome: "observed", IncidentCode: "audio", Reason: "Audio delivery resumed", Started: time.Now()}, time.UTC)
	if row.TypeLabel != "Recovered" || row.Reason != "Audio delivery resumed" {
		t.Fatal("unrelated recovery changed", row)
	}
}

func TestShowPreparationSuccessDoesNotImplyRecoveryOrAirtime(t *testing.T) {
	event := operations.Event{Kind: "recovery", Recover: true, Outcome: "observed", IncidentCode: "preparation", Reason: "DJ show preparation recovered", Started: time.Now()}
	row := producerEvent(event, time.UTC)
	if row.TypeLabel != "DJ presentation prepared" || row.OutcomeLabel != "Prepared" || row.Notes != "Show presentation preparation completed." {
		t.Fatal(row)
	}
	if !row.Recover || row.IncidentCode != "preparation" {
		t.Fatal("successful preparation must still clear its earlier incident", row)
	}
}

func TestOnlyAudioTransmissionsShowEndTime(t *testing.T) {
	start := time.Date(2026, 10, 20, 10, 43, 22, 0, time.UTC)
	for _, kind := range []string{"music", "speech", "selection", "recovery", "ai", "started", "handover", "failure"} {
		row := producerEvent(operations.Event{Kind: kind, Started: start, Ended: start.Add(time.Second)}, time.UTC)
		audio := kind == "music" || kind == "speech"
		if (row.EndTime != "") != audio {
			t.Fatalf("%s end time %q", kind, row.EndTime)
		}
		if row.StartTime != "10:43:22" {
			t.Fatal(row.StartTime)
		}
	}
}

func TestTimelineSeparatesActivityIconsFromOutcomeIcons(t *testing.T) {
	for _, tc := range []struct {
		event                   operations.Event
		activity, outcome, tone string
	}{
		{operations.Event{Kind: "selection", Title: "DJ selected · Song", Outcome: "observed"}, "speech", "completed", "success"},
		{operations.Event{Kind: "recovery", IncidentCode: "curation", Recover: true, Outcome: "observed", Severity: "warning"}, "speech", "completed", "success"},
		{operations.Event{Kind: "recovery", IncidentCode: "preparation", Recover: true, Outcome: "observed"}, "speech", "completed", "success"},
		{operations.Event{Kind: "recovery", IncidentCode: "speech", Recover: true, Outcome: "observed"}, "speech", "completed", "success"},
		{operations.Event{Kind: "ai", IncidentCode: "speech", Outcome: "observed"}, "speech", "error", "error"},
		{operations.Event{Kind: "ai", IncidentCode: "speech", Outcome: "observed", Reason: "DJ speech was not ready at the track boundary; music continues"}, "speech", "warning", "warning"},
		{operations.Event{Kind: "selection", Title: "Automatic selection · Song", Outcome: "observed"}, "automatic", "completed", "success"},
		{operations.Event{Kind: "handover", Outcome: "observed"}, "handover", "completed", "success"},
		{operations.Event{Kind: "handover", Outcome: "observed", Severity: "critical"}, "handover", "error", "error"},
	} {
		row := producerEvent(tc.event, time.UTC)
		state := historyStateIcon(row)
		if row.TypeIcon != tc.activity || state.Icon != tc.outcome || state.Tone != tc.tone {
			t.Fatal(tc.event, row, state)
		}
	}
}
