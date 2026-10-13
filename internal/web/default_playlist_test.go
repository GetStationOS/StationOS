package web

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/station"
)

func TestPlaylistDefaultActions(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stations := persistence.Stations{DB: db}
	pools := persistence.Playlists{DB: db}
	config := persistence.Broadcast{DB: db}
	a, err := (station.Service{Repo: stations}).Save(ctx, station.Station{Name: "First", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := (station.Service{Repo: stations}).Save(ctx, station.Station{Name: "Second", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []playlists.Playlist{{ID: "one", StationID: a.ID, Name: "One"}, {ID: "two", StationID: a.ID, Name: "Two"}, {ID: "other", StationID: b.ID, Name: "Other"}} {
		if err := pools.Save(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.Assign(ctx, b.ID, "other"); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := broadcast.New(config, tickingEncoder{}, logger, broadcast.Options{})
	defer manager.Close()
	h, err := New(Dependencies{Stations: stations, Sources: persistence.Sources{DB: db}, Catalogue: persistence.Catalogue{DB: db}, Playlists: pools, BroadcastConfig: config, Broadcast: manager, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	token := csrf(t, h)
	assertAssignment := func(id, want string) {
		t.Helper()
		got, err := config.Assignment(ctx, id)
		if err != nil || got != want {
			t.Fatalf("assignment = %q, %v; want %q", got, err, want)
		}
	}
	for _, id := range []string{"one", "two"} {
		w := request(h, "POST", "/producer/playlists/"+id+"/default", url.Values{"csrf": {token}, "action": {"set"}, "station": {b.ID}, "playlist": {"other"}}, map[string]string{"HX-Request": "true"})
		if w.Code != 200 || w.Header().Get("HX-Location") != "" || !strings.Contains(w.Body.String(), `id="playlist-default-`+id+`"`) || !strings.Contains(w.Body.String(), "Remove as station default") || strings.Contains(w.Body.String(), `name="description"`) || strings.Contains(w.Body.String(), "<title>") {
			t.Fatal(w.Code, w.Header(), w.Body.String())
		}
		assertAssignment(a.ID, id)
		assertAssignment(b.ID, "other")
	}
	// A stale remove action must not clear the newly selected default.
	w := request(h, "POST", "/producer/playlists/one/default", url.Values{"csrf": {token}, "action": {"clear"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	assertAssignment(a.ID, "two")
	w = request(h, "GET", "/producer/playlists?station="+a.ID, nil, nil)
	if strings.Count(w.Body.String(), `title="Station default playlist"`) != 1 {
		t.Fatal("missing or duplicate default marker", w.Body.String())
	}
	w = request(h, "GET", "/producer/playlists/two/settings", nil, nil)
	if !strings.Contains(w.Body.String(), "Remove as station default") {
		t.Fatal(w.Body.String())
	}
	w = request(h, "GET", "/producer/stations/"+a.ID+"/settings", nil, nil)
	if strings.Contains(w.Body.String(), `name="playlist"`) {
		t.Fatal("station settings still has playlist dropdown")
	}
	w = request(h, "POST", "/producer/playlists/two/default", url.Values{"action": {"clear"}}, nil)
	if w.Code != 403 {
		t.Fatal("unprotected default action", w.Code)
	}
	assertAssignment(a.ID, "two")
	w = request(h, "POST", "/producer/playlists/two/default", url.Values{"csrf": {token}, "action": {"invalid"}}, nil)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	assertAssignment(a.ID, "two")
	w = request(h, "POST", "/producer/playlists/two/default", url.Values{"csrf": {token}, "action": {"clear"}}, map[string]string{"HX-Request": "true"})
	if w.Code != 200 || w.Header().Get("HX-Location") != "" || !strings.Contains(w.Body.String(), "Set as station default") || strings.Contains(w.Body.String(), "Default playlist removed.") || strings.Contains(w.Body.String(), `name="description"`) {
		t.Fatal("clear must return only the updated default card", w.Code, w.Header(), w.Body.String())
	}
	assertAssignment(a.ID, "")
	if err := config.Assign(ctx, a.ID, "two"); err != nil {
		t.Fatal(err)
	}
	w = request(h, "POST", "/producer/playlists/two/default", url.Values{"csrf": {token}, "action": {"clear"}}, nil)
	if w.Code != 303 || w.Header().Get("Location") != "/producer/playlists/two/settings?notice=default-cleared" {
		t.Fatal(w.Code, w.Header())
	}
	assertAssignment(a.ID, "")
	assertAssignment(b.ID, "other")
	w = request(h, "GET", "/producer/playlists/two/settings", nil, nil)
	if !strings.Contains(w.Body.String(), "Set as station default") {
		t.Fatal(w.Body.String())
	}
}
