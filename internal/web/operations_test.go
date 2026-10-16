package web

import (
	"context"
	"encoding/csv"
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
	if !strings.Contains(html.UnescapeString(w.Body.String()), "GMT +00:00") || !strings.Contains(w.Body.String(), "Breakfast") || !strings.Contains(w.Body.String(), "Composer") {
		t.Fatal("time or snapshot missing", w.Body.String())
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
	if err != nil || len(rows) != 2 || rows[1][5] != "'=1+1" {
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
