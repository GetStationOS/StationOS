package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

func timingScheduleFixture(t *testing.T) http.Handler {
	t.Helper()
	h, st, _, _ := setupWeb(t, "")
	ctx := context.Background()
	loc, _ := time.LoadLocation("Europe/London")
	now := time.Now().In(loc)
	if err := st.Save(ctx, station.Station{ID: "timing", Name: "Timing edit verification", Timezone: "Europe/London", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	if err := (persistence.Playlists{DB: st.DB}).Save(ctx, playlists.Playlist{ID: "pool", StationID: "timing", Name: "80s"}); err != nil {
		t.Fatal(err)
	}
	shows := persistence.Schedule{DB: st.DB}
	show := schedule.Show{ID: "80s", StationID: "timing", Name: "The 80s Collection", DJID: "default-pop", PlaylistID: "pool", Day: (int(now.Weekday()) + 6) % 7, Start: 0, End: 0}
	if err := shows.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	saved, err := shows.Get(ctx, "timing", "80s")
	if err != nil {
		t.Fatal(err)
	}
	var active schedule.Occurrence
	for _, o := range schedule.Occurrences([]schedule.Show{saved}, now, loc) {
		if !now.Before(o.Start) && now.Before(o.End) {
			active = o
		}
	}
	repo := persistence.Episodes{DB: st.DB}
	if err := repo.Update(ctx, active, func(e *episodes.Episode) error {
		e.Aired = []episodes.Track{{ID: "aired", AiredAt: now.Add(-time.Minute), Seconds: 180}}
		e.Status = "ready"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	saved.End = 600
	if err := shows.Save(ctx, saved); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestScheduleShowsReconciledEndAndTimingChange(t *testing.T) {
	h := timingScheduleFixture(t)
	for _, headers := range []map[string]string{nil, {"HX-Request": "true"}} {
		response := request(h, "GET", "/producer/stations/timing/schedule", nil, headers)
		body := response.Body.String()
		if response.Code != 200 || !strings.Contains(body, "Timing changed") || !strings.Contains(body, "00:00–10:00") || strings.Contains(body, "00:00–00:00") {
			t.Fatal("schedule omitted changed timing", response.Code, body)
		}
	}
}
