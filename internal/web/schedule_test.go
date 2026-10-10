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
	for _, path := range []string{"/stations/s/schedule", "/stations/s/schedule/new"} {
		w := request(h, "GET", path, nil, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Schedule") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	form := url.Values{"csrf": {token}, "name": {"Morning"}, "day": {"0"}, "start": {"09:00"}, "end": {"10:00"}, "dj": {"default-pop"}, "playlist": {"s-pool"}}
	w := request(h, "POST", "/stations/s/schedule", form, map[string]string{"HX-Request": "true"})
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	var id string
	st.DB.QueryRow(`SELECT id FROM station_shows`).Scan(&id)
	w = request(h, "GET", "/stations/s/schedule", nil, map[string]string{"HX-Request": "true"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "weekly-calendar") || !strings.Contains(w.Body.String(), "Alex Bright") || strings.Contains(w.Body.String(), "<!doctype") {
		t.Fatal(w.Code, w.Body.String())
	}
	form.Set("start", "09:15")
	w = request(h, "POST", "/stations/s/schedule", form, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "overlaps") {
		t.Fatal(w.Code, w.Body.String())
	}
	form.Set("playlist", "other-pool")
	w = request(h, "POST", "/stations/s/schedule", form, nil)
	if w.Code != 422 {
		t.Fatal("foreign playlist accepted", w.Code)
	}
	w = request(h, "GET", "/stations/other/schedule/"+id, nil, nil)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = request(h, "GET", "/stations/s/schedule/"+id+"/delete", nil, map[string]string{"HX-Request": "true", "HX-Target": "dialog-body"})
	if w.Code != 200 || strings.Contains(w.Body.String(), "<!doctype") || !strings.Contains(w.Body.String(), "dialog-heading") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/stations/s/schedule/"+id+"/delete", url.Values{"csrf": {token}, "confirm": {"yes"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
}
