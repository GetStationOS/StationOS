package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

// Configuration pages must remain available when the rebuildable catalogue fails.
type unavailableViewCatalogue struct {
	catalogue.Repository
	calls int
}

func (r *unavailableViewCatalogue) Search(context.Context, catalogue.Query) (catalogue.Result, error) {
	r.calls++
	return catalogue.Result{}, errors.New("catalogue unavailable")
}
func (r *unavailableViewCatalogue) Facets(context.Context, ...string) (catalogue.Facets, error) {
	r.calls++
	return catalogue.Facets{}, errors.New("catalogue unavailable")
}

type viewPlaylistReads struct {
	playlists.Repository
	stations []string
}

func (r *viewPlaylistReads) List(ctx context.Context, stationID string) ([]playlists.Playlist, error) {
	r.stations = append(r.stations, stationID)
	return r.Repository.List(ctx, stationID)
}

type viewSourceReads struct {
	sources.Repository
	lists int
}

func (r *viewSourceReads) List(ctx context.Context) ([]sources.Config, error) {
	r.lists++
	return r.Repository.List(ctx)
}

func TestPageDataDependencies(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	stations := persistence.Stations{DB: db}
	st, err := (station.Service{Repo: stations}).Save(ctx, station.Station{Name: "Test radio", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	pools := &viewPlaylistReads{Repository: persistence.Playlists{DB: db}}
	if err := pools.Save(ctx, playlists.Playlist{ID: "pool", StationID: st.ID, Name: "Test pool"}); err != nil {
		t.Fatal(err)
	}
	sourceRepo := &viewSourceReads{Repository: persistence.Sources{DB: db}}
	source, err := (sources.Service{Repo: sourceRepo}).Save(ctx, sources.Config{Name: "Library", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	djRepo, err := persistence.NewDJs(db, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	cat := &unavailableViewCatalogue{}
	h, err := New(Dependencies{
		Stations: stations, Sources: sourceRepo, Catalogue: cat, Playlists: pools,
		DJs: djRepo, Schedule: persistence.Schedule{DB: db}, Mixer: persistence.Mixer{DB: db},
		Assistant: &producer.Service{Store: persistence.NewProducer(djRepo)},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	stationPath := "/producer/stations/" + st.ID
	cases := []struct {
		path, content string
		playlistScope string
		playlistLists int
	}{
		{"/producer", "Test radio", "", 1},
		{"/producer/stations/live", "Test radio", "", 1},
		{"/producer/station", "Create station", "", 0},
		{"/producer/stations/new?q=ignored&per_page=invalid", "Create station", "", 0},
		{stationPath, "Nothing playing", st.ID, 1},
		{stationPath + "/settings", "Station settings", "", 0},
		{stationPath + "/delete", "and its 1 playlist", st.ID, 1},
		{stationPath + "/mixer", "Music", "", 0},
		{stationPath + "/schedule", "Your week, your presenters", "", 0},
		{stationPath + "/schedule/new", "Test pool", st.ID, 1},
		{"/producer/djs", "DJs", "", 0},
		{"/producer/djs/new", "Create DJ", "", 0},
		{"/producer/sources/new", "Add source", "", 0},
		{"/producer/sources/" + source.ID + "/settings", "Library", "", 0},
		{"/producer/playlists", "Test pool", "", 1},
		{"/producer/playlists?station=" + st.ID, "Test pool", st.ID, 1},
		{"/producer/playlists/new", "Test radio", "", 0},
		{"/producer/playlists/new?station=" + st.ID, "Test radio", "", 0},
		{"/producer/playlists/pool/settings", "Test pool", "", 0},
		{"/producer/playlists/pool/delete", "Delete Test pool?", "", 0},
		{"/producer/assistant", "Producer assistant", "", 0},
		{"/producer/assistant/settings", "Models for features", "", 0},
		{"/producer/assistant/endpoints", "AI endpoints", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			for _, headers := range []map[string]string{nil, {"HX-Request": "true"}, {"HX-Request": "true", "HX-History-Restore-Request": "true"}} {
				cat.calls = 0
				pools.stations = nil
				sourceRepo.lists = 0
				w := request(h, http.MethodGet, tc.path, nil, headers)
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.content) {
					t.Fatalf("headers %v: status %d, body %s", headers, w.Code, w.Body.String())
				}
				if cat.calls != 0 {
					t.Fatalf("unrelated catalogue reads: %d", cat.calls)
				}
				wantSourceLists := 0
				if tc.path == "/producer/station" || strings.HasPrefix(tc.path, "/producer/stations/new") || tc.path == stationPath+"/settings" {
					wantSourceLists = 1
				}
				if sourceRepo.lists != wantSourceLists {
					t.Fatalf("unrelated source lists: %d", sourceRepo.lists)
				}
				if len(pools.stations) != tc.playlistLists {
					t.Fatalf("playlist lists = %v; want %d calls", pools.stations, tc.playlistLists)
				}
				for _, scope := range pools.stations {
					if scope != tc.playlistScope {
						t.Fatalf("playlist scope = %q; want %q", scope, tc.playlistScope)
					}
				}
			}
		})
	}
	// Ordinary form validation must reload the same station's playlist choices.
	t.Run("schedule validation retains choices", func(t *testing.T) {
		token := csrf(t, h)
		pools.stations = nil
		cat.calls = 0
		w := request(h, http.MethodPost, stationPath+"/schedule", url.Values{
			"csrf": {token}, "name": {"Morning"}, "day": {"1"}, "start": {"09:01"}, "end": {"10:00"}, "playlist": {"pool"},
		}, nil)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `<option value="pool" selected>Test pool</option>`) {
			t.Fatalf("validation lost playlist choices: %d %s", w.Code, w.Body.String())
		}
		if cat.calls != 0 || len(pools.stations) != 1 || pools.stations[0] != st.ID {
			t.Fatalf("unexpected validation reads: catalogue=%d, playlist scopes=%v", cat.calls, pools.stations)
		}
	})
	// Screens that actually browse catalogue data must still report its failure.
	for _, path := range []string{"/producer/content", "/producer/playlists/pool", "/producer/sources/" + source.ID} {
		cat.calls = 0
		w := request(h, http.MethodGet, path, nil, nil)
		if w.Code != http.StatusInternalServerError || cat.calls == 0 {
			t.Fatalf("%s: status %d, catalogue reads %d", path, w.Code, cat.calls)
		}
	}
}
