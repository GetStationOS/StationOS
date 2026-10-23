package web

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/schedule"
)

func TestPublicProgrammeCivilDaysAndOccurrenceLinks(t *testing.T) {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, loc)
	tabs, date := publicScheduleDays(now, loc, "2026-10-25", "/radio/test")
	if len(tabs) != 7 || tabs[0].Name != "Today" || tabs[1].Name != "Tomorrow" || !tabs[1].Current || date.Day() != 25 || date.AddDate(0, 0, 1).Sub(date) != 25*time.Hour {
		t.Fatal(tabs, date)
	}
	_, fallback := publicScheduleDays(now, loc, "2026-12-01", "/radio/test")
	if fallback.Day() != 24 {
		t.Fatal("out-of-window date", fallback)
	}
	shows := []schedule.Show{
		{ID: "night", StationID: "s", Name: "Late show", Day: 5, Start: 1380, End: 120, Revision: 3, AcceptShoutouts: true, DJ: djs.Profile{Name: "Night DJ"}},
		{ID: "morning", StationID: "s", Name: "Sunday requests", Day: 6, Start: 540, End: 600, Revision: 2, AcceptRequests: true},
		{ID: "evening", StationID: "s", Name: "Evening", Day: 6, Start: 1320, End: 60, Revision: 1},
	}
	rows := publicDayProgramme(shows, now, date, loc, "/radio/test", true)
	if len(rows) != 5 || rows[0].Name != "Late show" || rows[0].Time != "00:00–02:00" || rows[1].Time != "02:00–09:00" || rows[2].Name != "Sunday requests" || rows[3].Time != "10:00–22:00" || rows[4].Time != "22:00–24:00" {
		t.Fatal(rows)
	}
	if !strings.Contains(rows[0].ContributionURL, "night%3A5%3A2026-10-19%3A3") || !strings.Contains(rows[2].ContributionURL, "morning%3A6%3A2026-10-19%3A2") || !strings.Contains(rows[2].ContributionURL, "day=2026-10-25") || rows[4].ContributionURL != "" {
		t.Fatal("wrong airing or unsupported contribution", rows)
	}
	later := time.Date(2026, 10, 25, 11, 0, 0, 0, loc)
	ended := publicDayProgramme(shows, later, date, loc, "/radio/test", true)
	if len(ended) != 2 || ended[0].Name != "Continuous music" || ended[0].Time != "10:00–22:00" || ended[1].Name != "Evening" || ended[1].Expanded {
		t.Fatal("today retained ended shows", ended)
	}
	empty := publicDayProgramme(nil, now, date, loc, "/radio/test", true)
	if len(empty) != 1 || empty[0].Time != "00:00–24:00" || empty[0].Name != "Continuous music" {
		t.Fatal(empty)
	}
}

func TestPublicTodayHidesCompletedAndExpandsCurrent(t *testing.T) {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, 10, 14, 0, 0, 0, 0, loc)
	shows := []schedule.Show{
		{ID: "overnight", Name: "Night", Day: 1, Start: 1380, End: 60},
		{ID: "breakfast", Name: "Breakfast", Day: 2, Start: 60, End: 540},
		{ID: "current", Name: "Morning", Description: "The full show description", Day: 2, Start: 540, End: 720, AcceptRequests: true},
		{ID: "later", Name: "Afternoon", Day: 2, Start: 720, End: 960},
	}
	midnight := publicDayProgramme(shows, date.Add(30*time.Minute), date, loc, "/radio/s", true)
	if !midnight[0].Expanded || midnight[0].ID != "overnight" {
		t.Fatal("overnight current show not opened", midnight)
	}
	rows := publicDayProgramme(shows, date.Add(10*time.Hour), date, loc, "/radio/s", true)
	if len(rows) != 3 || rows[0].ID != "current" || !rows[0].Expanded || rows[1].Expanded || rows[0].Description != "The full show description" || !strings.Contains(rows[0].ContributionURL, "/shows/current/contribute?") {
		t.Fatal(rows)
	}
	boundary := publicDayProgramme(shows, date.Add(12*time.Hour), date, loc, "/radio/s", true)
	if boundary[0].ID != "later" || !boundary[0].Expanded {
		t.Fatal("boundary is not end-exclusive", boundary)
	}
	future := publicDayProgramme(shows, date.AddDate(0, 0, -1), date, loc, "/radio/s", true)
	if len(future) != 5 {
		t.Fatal("future schedule was truncated", future)
	}
	for _, row := range future {
		if row.Expanded {
			t.Fatal("future show opened", row)
		}
	}
}

func TestPublicCurrentShowRendersExpandedAndCloseable(t *testing.T) {
	tmpl, err := template.New("public").Funcs(templateFunctions()).ParseFS(assets, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	var html bytes.Buffer
	current := publicShow{ID: "current", Name: "Morning", Description: "Current show details", URL: "/radio/s/shows/current?show=airing&day=2026-10-14", ContributionURL: "/radio/s/shows/current/contribute?show=airing&day=2026-10-14", Expanded: true}
	v := publicPageView{AudienceDay: "2026-10-14", PublicDays: []publicDay{{Name: "Today", Shows: []publicShow{current}}}}
	if err = tmpl.ExecuteTemplate(&html, "public-programme-day", v); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-show-loaded="true"`, `data-open-show aria-expanded="true"`, `class="public-show-body" tabindex="-1">`, "Current show details", `id="contribution-current"`, `hx-swap="innerHTML show:none"`, "Be part of the show", `/shows/current/contribute?show=airing&amp;day=2026-10-14`} {
		if !strings.Contains(html.String(), want) {
			t.Fatal("missing default expansion or scoped invitation", want, html.String())
		}
	}
}

func TestCancelledDatedEpisodeIsContinuousMusicOnPublicProgramme(t *testing.T) {
	loc := time.UTC
	date := time.Date(2026, 10, 19, 0, 0, 0, 0, loc)
	now := date.Add(8 * time.Hour)
	shows := []schedule.Show{{ID: "cancelled", StationID: "s", Name: "Cancelled airing", Day: 0, Start: 540, End: 600}}
	cancelled := map[string]bool{}
	for _, o := range schedule.Occurrences(shows, date, loc) {
		if o.Start.Equal(date.Add(9 * time.Hour)) {
			cancelled[schedule.EpisodeKey(o.Key)] = true
		}
	}
	rows := publicDayProgramme(shows, now, date, loc, "/radio/s", true, cancelled)
	if len(rows) != 1 || rows[0].Name != "Continuous music" {
		t.Fatal(rows)
	}
	next := publicDayProgramme(shows, now, date.AddDate(0, 0, 7), loc, "/radio/s", true, cancelled)
	found := false
	for _, row := range next {
		if row.Name == "Cancelled airing" {
			found = true
		}
	}
	if !found {
		t.Fatal("cancelled the recurring template", next)
	}
}
