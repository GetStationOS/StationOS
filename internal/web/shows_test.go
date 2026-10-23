package web

import (
	"context"
	"net/url"
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

func TestShowsWorkspaceSharesSettingsAndPreservesEpisodeIdentity(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	ctx := context.Background()
	if err := st.Save(ctx, station.Station{ID: "s", Name: "Station", Timezone: "Europe/London", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	if err := (persistence.Playlists{DB: st.DB}).Save(ctx, playlists.Playlist{ID: "pool", StationID: "s", Name: "Pool"}); err != nil {
		t.Fatal(err)
	}
	token := csrf(t, h)
	form := url.Values{"csrf": {token}, "name": {"Breakfast"}, "description": {"A bright start"}, "day": {"0", "2"}, "start": {"09:00"}, "end": {"10:00"}, "dj": {"default-pop"}, "playlist": {"pool"}}
	w := request(h, "POST", "/producer/stations/s/shows", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	repo := persistence.Schedule{DB: st.DB}
	shows, err := repo.List(ctx, "s")
	if err != nil || len(shows) != 1 || len(shows[0].Weekdays()) != 2 {
		t.Fatal(shows, err)
	}
	show := shows[0]
	w = request(h, "GET", "/producer/stations/s/shows", nil, nil)
	if w.Code != 200 || strings.Count(w.Body.String(), ">Breakfast</a>") != 1 || !strings.Contains(w.Body.String(), "Monday, Wednesday") {
		t.Fatal(w.Code, w.Body.String())
	}
	tabs := stationTabs(pageFrame{pageView: pageView{Station: station.Station{ID: "s"}, Section: "shows"}})
	if tabs.Items[0].Label != "Overview" || tabs.Items[1].Label != "Shows" || !tabs.Items[1].Current || tabs.Items[2].Label != "Schedule" {
		t.Fatal(tabs)
	}
	loc, _ := time.LoadLocation("Europe/London")
	monday := schedule.Monday(time.Now(), loc).AddDate(0, 0, 7)
	var o schedule.Occurrence
	for _, candidate := range schedule.Occurrences(shows, monday, loc) {
		if candidate.Start.Equal(monday.Add(9 * time.Hour)) {
			o = candidate
		}
	}
	if o.Key == "" {
		t.Fatal("no Monday occurrence")
	}
	path := episodePath("s", o.Key)
	// Viewing dated instances must not create sessions or start preparation.
	for _, route := range []string{path, "/producer/stations/s/shows/" + show.ID + "/episodes"} {
		w = request(h, "GET", route, nil, nil)
		if w.Code != 200 {
			t.Fatal(route, w.Code, w.Body.String())
		}
	}
	var count int
	if err := st.DB.QueryRow(`SELECT count(*) FROM show_episodes`).Scan(&count); err != nil || count != 0 {
		t.Fatal("read created an episode", count, err)
	}
	w = request(h, "POST", path, url.Values{"csrf": {token}, "version": {"0"}, "notes": {"Explore the new releases"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	store := persistence.Episodes{DB: st.DB}
	saved, err := store.Get(ctx, o.Key)
	if err != nil {
		t.Fatal(err)
	}
	w = request(h, "POST", path+"/cancel", url.Values{"csrf": {token}, "version": {strconv.Itoa(saved.Version)}, "confirm": {"yes"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Move both weekly slots and change their shared editorial metadata.
	form.Set("show_id", show.ID)
	form.Set("name", "Morning discoveries")
	form.Set("description", "Fresh music every morning")
	form["day"] = []string{"1", "3"}
	form.Set("start", "10:00")
	form.Set("end", "11:00")
	w = request(h, "POST", "/producer/stations/s/shows", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	updated, err := repo.Get(ctx, "s", show.ID)
	if err != nil {
		t.Fatal(err)
	}
	var moved schedule.Occurrence
	for _, candidate := range schedule.Occurrences([]schedule.Show{updated}, monday, loc) {
		if schedule.EpisodeKey(candidate.Key) == schedule.EpisodeKey(o.Key) {
			moved = candidate
		}
	}
	if moved.Key == "" || moved.Start.Weekday() != time.Tuesday || moved.Start.Hour() != 10 || episodePath("s", moved.Key) != path {
		t.Fatal("moving the slot changed episode identity", o, moved)
	}
	w = request(h, "GET", path, nil, nil)
	for _, want := range []string{"Morning discoveries", "Fresh music every morning", "Cancelled", "10:00"} {
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Fatal(want, w.Code, w.Body.String())
		}
	}
	saved, err = store.Get(ctx, moved.Key)
	if err != nil || !saved.Cancelled || saved.Notes != "Explore the new releases" {
		t.Fatal("episode direction/exception lost", saved, err)
	}
	timeline, err := repo.Resolve(ctx, "s", moved.Start.Add(time.Minute))
	if err != nil || timeline.Current.ShowID != "" {
		t.Fatal("cancelled episode scheduled", timeline, err)
	}
	// The indexed start follows a moved future instance on its next write.
	w = request(h, "POST", path+"/cancel", url.Values{"csrf": {token}, "version": {strconv.Itoa(saved.Version)}, "confirm": {"yes"}, "restore": {"yes"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	var indexedStart int64
	if err := st.DB.QueryRow(`SELECT starts_at FROM show_episodes WHERE episode_key=?`, schedule.EpisodeKey(o.Key)).Scan(&indexedStart); err != nil || indexedStart != moved.Start.Unix() {
		t.Fatal("moved episode index is stale", indexedStart, moved.Start, err)
	}
	// Parent settings cannot erase an actual-air receipt or move its historical window.
	past := schedule.Occurrences([]schedule.Show{updated}, monday.AddDate(0, 0, -21), loc)[0]
	if err := store.Update(ctx, past, func(e *episodes.Episode) error {
		e.Aired = []episodes.Track{{ID: "played", Title: "Actual broadcast recording", AiredAt: past.Start}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pastURL := episodePath("s", past.Key)
	if err := repo.Delete(ctx, "s", show.ID); err != nil {
		t.Fatal(err)
	}
	w = request(h, "GET", pastURL, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Actual broadcast recording") || strings.Contains(w.Body.String(), "Edit episode direction") {
		t.Fatal("archived episode/history inaccessible", w.Code, w.Body.String())
	}
	w = request(h, "GET", "/producer/stations/s/shows?archived=1", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Morning discoveries") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/producer/stations/s/shows/"+show.ID+"/episodes", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), pastURL) {
		t.Fatal("archive lost history link", w.Code, w.Body.String())
	}
	w = request(h, "GET", strings.Replace(path, "/stations/s/", "/stations/foreign/", 1), nil, nil)
	if w.Code != 404 {
		t.Fatal("episode escaped its station", w.Code)
	}
}
