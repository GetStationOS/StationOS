package web

import (
	"context"
	"net/url"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
)

func TestScheduleFormsCalendarAndScopedWrites(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	ctx := context.Background()
	token := csrf(t, h)
	for _, id := range []string{"s", "other"} {
		if err := st.Save(ctx, station.Station{ID: id, Name: id, Timezone: "Europe/London", Locale: "en"}); err != nil {
			t.Fatal(err)
		}
		if err := (persistence.Playlists{DB: st.DB}).Save(ctx, playlists.Playlist{ID: id + "-pool", StationID: id, Name: "Pool"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/producer/stations/s/schedule", "/producer/stations/s/schedule/new"} {
		w := request(h, "GET", path, nil, nil)
		if strings.HasSuffix(path, "/new") && (!strings.Contains(w.Body.String(), "DJ music prompt (optional)") || strings.Contains(w.Body.String(), "Rotation format")) {
			t.Fatal("show agent prompt form", w.Body.String())
		}
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Schedule") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	form := url.Values{"csrf": {token}, "name": {"Morning"}, "day": {"0"}, "start": {"09:00"}, "end": {"10:00"}, "dj": {"default-pop"}, "playlist": {"s-pool"}}
	w := request(h, "POST", "/producer/stations/s/schedule", form, map[string]string{"HX-Request": "true"})
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	var id string
	st.DB.QueryRow(`SELECT id FROM station_shows`).Scan(&id)
	w = request(h, "GET", "/producer/stations/s/schedule", nil, map[string]string{"HX-Request": "true"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "weekly-calendar") || !strings.Contains(w.Body.String(), "Alex Bright") || strings.Contains(w.Body.String(), "<!doctype") {
		t.Fatal(w.Code, w.Body.String())
	}
	form.Set("start", "09:15")
	w = request(h, "POST", "/producer/stations/s/schedule", form, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "overlaps") {
		t.Fatal(w.Code, w.Body.String())
	}
	form.Set("playlist", "other-pool")
	w = request(h, "POST", "/producer/stations/s/schedule", form, nil)
	if w.Code != 422 {
		t.Fatal("foreign playlist accepted", w.Code)
	}
	w = request(h, "GET", "/producer/stations/other/schedule/"+id, nil, nil)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = request(h, "GET", "/producer/stations/s/schedule/"+id+"/delete", nil, map[string]string{"HX-Request": "true", "HX-Target": "dialog-body"})
	if w.Code != 200 || strings.Contains(w.Body.String(), "<!doctype") || !strings.Contains(w.Body.String(), "dialog-heading") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/producer/stations/s/schedule/"+id+"/delete", url.Values{"csrf": {token}, "confirm": {"yes"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
}

func TestMultiDayShowCreationIsAtomicAndRetainsSelections(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	ctx := context.Background()
	if err := st.Save(ctx, station.Station{ID: "s", Name: "Station", Timezone: "Europe/London", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	repo := persistence.Playlists{DB: st.DB}
	if err := repo.Save(ctx, playlists.Playlist{ID: "pool", StationID: "s", Name: "Pool"}); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"csrf": {csrf(t, h)}, "name": {"Weekday breakfast"}, "day": {"0", "1", "2", "3", "4"}, "start": {"09:00"}, "end": {"10:00"}, "dj": {"default-pop"}, "playlist": {"pool"}}
	w := request(h, "POST", "/producer/stations/s/schedule", form, map[string]string{"HX-Request": "true"})
	if w.Code != 204 || !strings.Contains(w.Header().Get("HX-Location"), "shows-added") {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	shows, err := (persistence.Schedule{DB: st.DB}).List(ctx, "s")
	if err != nil || len(shows) != 5 {
		t.Fatal(shows, err)
	}
	ids := map[string]bool{}
	for day, show := range shows {
		if show.Day != day || show.Name != "Weekday breakfast" || show.Start != 540 || show.End != 600 || show.DJID != "default-pop" || show.PlaylistID != "pool" || ids[show.ID] {
			t.Fatal(show)
		}
		ids[show.ID] = true
	}
	w = request(h, "GET", "/producer/stations/s/schedule", nil, nil)
	if strings.Count(w.Body.String(), `class="calendar-show `) != 5 {
		t.Fatal(w.Body.String())
	}
	// Saturday is inserted first, then Friday conflicts: neither may be saved.
	form["day"] = []string{"5", "4"}
	w = request(h, "POST", "/producer/stations/s/schedule", form, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "overlaps") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, day := range []string{"4", "5"} {
		if !strings.Contains(w.Body.String(), `name="day" value="`+day+`" checked`) {
			t.Fatal("lost day selection", w.Body.String())
		}
	}
	for _, days := range [][]string{nil, {"0", "7"}, {"bad"}, {"5", "5"}} {
		form["day"] = days
		w = request(h, "POST", "/producer/stations/s/schedule", form, nil)
		if w.Code != 422 || !strings.Contains(w.Body.String(), `value="Weekday breakfast"`) {
			t.Fatal(days, w.Code, w.Body.String())
		}
	}
	shows, err = (persistence.Schedule{DB: st.DB}).List(ctx, "s")
	if err != nil || len(shows) != 5 {
		t.Fatal("partial batch saved", shows, err)
	}
	// Editing still changes only a single independent entry.
	form.Set("show_id", shows[0].ID)
	form.Set("day", "6")
	w = request(h, "POST", "/producer/stations/s/schedule", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	shows, err = (persistence.Schedule{DB: st.DB}).List(ctx, "s")
	if err != nil || len(shows) != 5 || shows[4].Day != 6 {
		t.Fatal(shows, err)
	}
}

func TestMultiDayShowsRejectOvernightSelfOverlap(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	ctx := context.Background()
	if err := st.Save(ctx, station.Station{ID: "s", Name: "Station", Timezone: "Europe/London", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	if err := (persistence.Playlists{DB: st.DB}).Save(ctx, playlists.Playlist{ID: "pool", StationID: "s", Name: "Pool"}); err != nil {
		t.Fatal(err)
	}
	// Overnight slots on adjacent days are allowed and remain independent.
	form := url.Values{"csrf": {csrf(t, h)}, "name": {"Night"}, "day": {"6", "0"}, "start": {"23:00"}, "end": {"01:00"}, "dj": {"default-pop"}, "playlist": {"pool"}}
	w := request(h, "POST", "/producer/stations/s/schedule", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	shows, err := (persistence.Schedule{DB: st.DB}).List(ctx, "s")
	if err != nil || len(shows) != 2 {
		t.Fatal(shows, err)
	}
	// The second selected day overlaps Sunday’s continuation into Monday.
	form["day"] = []string{"2", "0"}
	form.Set("start", "00:30")
	form.Set("end", "02:00")
	w = request(h, "POST", "/producer/stations/s/schedule", form, map[string]string{"HX-Request": "true"})
	if w.Code != 422 || !strings.Contains(w.Body.String(), "overlaps") {
		t.Fatal(w.Code, w.Body.String())
	}
	shows, err = (persistence.Schedule{DB: st.DB}).List(ctx, "s")
	if err != nil || len(shows) != 2 {
		t.Fatal("overnight conflict partially saved", shows, err)
	}
}
