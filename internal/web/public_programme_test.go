package web

import (
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
	if !strings.Contains(rows[0].ContributionURL, "night%3A2026-10-24T22%3A00%3A00Z%3A3") || !strings.Contains(rows[2].ContributionURL, "morning%3A2026-10-25T09%3A00%3A00Z%3A2") || !strings.Contains(rows[2].ContributionURL, "day=2026-10-25") || rows[4].ContributionURL != "" {
		t.Fatal("wrong airing or unsupported contribution", rows)
	}
	later := time.Date(2026, 10, 25, 11, 0, 0, 0, loc)
	ended := publicDayProgramme(shows, later, date, loc, "/radio/test", true)
	if ended[0].ContributionURL != "" || ended[2].ContributionURL != "" {
		t.Fatal("ended shows accepted contributions for next week", ended)
	}
	empty := publicDayProgramme(nil, now, date, loc, "/radio/test", true)
	if len(empty) != 1 || empty[0].Time != "00:00–24:00" || empty[0].Name != "Continuous music" {
		t.Fatal(empty)
	}
}
