package web

import (
	"context"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

func TestScheduleEpisodeReadinessInlineRunningOrderAndProducerEdits(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	ctx := context.Background()
	token := csrf(t, h)
	if err := st.Save(ctx, station.Station{ID: "s", Name: "Station", Timezone: "Europe/London", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	if err := (persistence.Playlists{DB: st.DB}).Save(ctx, playlists.Playlist{ID: "pool", StationID: "s", Name: "Pool"}); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/London")
	tomorrow := time.Now().In(loc).AddDate(0, 0, 1)
	day := (int(tomorrow.Weekday()) + 6) % 7
	show := schedule.Show{ID: "show", StationID: "s", Name: "Night drives", DJID: "default-pop", PlaylistID: "pool", Day: day, Start: 540, End: 600, MusicDirection: "Vary the performers"}
	shows := persistence.Schedule{DB: st.DB}
	if err := shows.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	show, _ = shows.Get(ctx, "s", "show")
	var o schedule.Occurrence
	for _, candidate := range schedule.Occurrences([]schedule.Show{show}, tomorrow, loc) {
		if candidate.Start.YearDay() == tomorrow.YearDay() && candidate.Start.Year() == tomorrow.Year() {
			o = candidate
			break
		}
	}
	store := persistence.Episodes{DB: st.DB}
	if err := store.Update(ctx, o, func(e *episodes.Episode) error {
		e.Status = "ready"
		e.Theme = "City lights"
		e.Plan = []episodes.Track{{ID: "a", Title: "First recording", Artist: "One", Seconds: 240}, {ID: "b", Title: "Second recording", Artist: "Two", Seconds: 240}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	week := schedule.Monday(tomorrow, loc).Format("2006-01-02")
	path := "/producer/stations/s/schedule?week=" + week
	w := request(h, "GET", path, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "episode-ready") || !strings.Contains(w.Body.String(), "Ready") {
		t.Fatal(w.Code, w.Body.String())
	}
	match := regexp.MustCompile(`href="([^"]*episode=show[^"]*)"`).FindStringSubmatch(w.Body.String())
	if len(match) != 2 {
		t.Fatal("missing episode link")
	}
	actual, err := url.Parse(html.UnescapeString(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	if actual.Query().Get("start") != o.Start.Format(time.RFC3339) {
		t.Fatal("episode timestamp double encoded", actual.String())
	}
	clicked := request(h, "GET", actual.RequestURI(), nil, nil)
	if clicked.Code != 200 || !strings.Contains(clicked.Body.String(), `id="episode-running-order"`) {
		t.Fatal("schedule click failed", clicked.Code, clicked.Body.String())
	}
	if strings.Contains(w.Body.String(), `id="episode-running-order"`) {
		t.Fatal("episode expanded without selection")
	}
	selected := path + "&episode=show&start=" + url.QueryEscape(o.Start.Format(time.RFC3339))
	w = request(h, "GET", selected, nil, map[string]string{"HX-Request": "true"})
	for _, want := range []string{`id="episode-running-order"`, "First recording", "Second recording", "City lights", `aria-expanded="true"`, `name="notes"`, `name="locked"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatal("missing", want, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "<!doctype") {
		t.Fatal("HTMX lost shell")
	}
	before, _ := store.Get(ctx, o.Key)
	editPath := "/producer/stations/s/schedule/show/episode?start=" + url.QueryEscape(o.Start.Format(time.RFC3339))
	form := url.Values{"csrf": {token}, "version": {strconv.Itoa(before.Version)}, "notes": {"More discoveries"}, "locked": {"b"}, "excluded": {"a"}}
	w = request(h, "POST", editPath, form, map[string]string{"HX-Request": "true"})
	if w.Code != 204 || !strings.Contains(w.Header().Get("HX-Location"), "episode=show") {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	after, _ := store.Get(ctx, o.Key)
	if after.Notes != "More discoveries" || !after.Plan[1].Locked || len(after.Excluded) != 1 || after.Status != "attention" {
		t.Fatal(after)
	}
	w = request(h, "POST", editPath, form, nil)
	if w.Code != 422 {
		t.Fatal("stale edit overwrote state", w.Code)
	}
	w = request(h, "GET", path, nil, nil)
	if !strings.Contains(w.Body.String(), "episode-attention") {
		t.Fatal(w.Body.String())
	}
	// Merely viewing a later episode must neither create it nor inherit this plan.
	nextStart := schedule.WallTime(tomorrow.AddDate(0, 0, 7), show.Start, false)
	w = request(h, "GET", "/producer/stations/s/schedule?week="+nextStart.Format("2006-01-02")+"&episode=show&start="+url.QueryEscape(nextStart.Format(time.RFC3339)), nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "City lights") || !strings.Contains(w.Body.String(), "Unplanned") {
		t.Fatal(w.Code, w.Body.String())
	}
}
