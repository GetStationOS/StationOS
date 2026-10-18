package web

import (
	"context"
	"html/template"
	"net/url"
	"strings"
	"testing"

	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

func navigationHTML(t *testing.T, name string, data any) string {
	t.Helper()
	templates, err := template.New("pages").Funcs(templateFunctions()).ParseFS(assets, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	s := Server{t: templates}
	body, err := s.executeHTML(name, data)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
func TestSharedNavigationEscapesContentAndRetainsLinkSemantics(t *testing.T) {
	body := navigationHTML(t, "breadcrumbs", breadcrumbs(crumb("Stations", "/producer"), crumb("<script>long name</script>", "")))
	for _, want := range []string{`<nav class="breadcrumbs" aria-label="Breadcrumb">`, `<ol>`, `aria-current="page"`, `&lt;script&gt;long name&lt;/script&gt;`} {
		if !strings.Contains(body, want) {
			t.Fatal(want, body)
		}
	}
	if strings.Contains(body, "<script>") {
		t.Fatal("breadcrumb content was not escaped")
	}
	cases := []struct {
		view        workspaceTabsView
		active, key string
	}{
		{stationTabs(pageFrame{pageView: pageView{Station: station.Station{ID: "s"}, Section: "settings", ActiveIncidents: 2}}), "Settings", "station:s"},
		{sourceTabs(sourcePageView{Source: sources.Config{ID: "source"}, SourceTab: "settings"}), "Settings", "source:source"},
		{playlistTabs(playlistPageView{Playlist: playlists.Playlist{ID: "pool"}, PlaylistTab: "tracks"}), "Tracks", "playlist:pool"},
		{assistantTabs("delete-connection"), "Endpoints", "assistant"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			body := navigationHTML(t, "workspace-tabs", tc.view)
			if strings.Count(body, `aria-current="page"`) != 1 || !strings.Contains(body, `data-tab-scroll-key="`+tc.key+`"`) || !strings.Contains(body, `aria-current="page">`+tc.active) {
				t.Fatal(body)
			}
			if strings.Contains(body, `role="tab`) || !strings.Contains(body, `<a href=`) {
				t.Fatal("workspace tabs must retain ordinary linked navigation", body)
			}
			if tc.key == "station:s" && (!strings.Contains(body, `id="incident-menu-indicator"`) || !strings.Contains(body, `data-live-url="/producer/stations/s/operations/indicator"`) || !strings.Contains(body, "2 active incidents")) {
				t.Fatal("lost live incident indicator", body)
			}
		})
	}
}
func TestSharedHeadingPreservesProtectedActionsAndMetadata(t *testing.T) {
	view := playlistPageView{pageView: pageView{Title: "<Music>", CSRF: "token"}, Playlist: playlists.Playlist{ID: "pool"}, PlaylistTab: "tracks", PlaylistResult: playlists.Result{Total: 12, UnknownDurations: 2}, ExcludedTracks: 3, PlaylistDuration: "48:00", ReturnTo: "/producer/playlists/pool/tracks?page=2"}
	body := navigationHTML(t, "page-heading", playlistHeading(view))
	for _, want := range []string{`<h1>&lt;Music&gt;</h1>`, `12 tracks · 3 excluded · 48:00 · 2 durations unknown`, `method="post" action="/producer/playlists/pool/refresh"`, `name="csrf" value="token"`, `name="return_to" value="/producer/playlists/pool/tracks?page=2"`, `Recheck saved genre and album selections`} {
		if !strings.Contains(body, want) {
			t.Fatal(want, body)
		}
	}
	view.PlaylistTab = "delete"
	body = navigationHTML(t, "page-heading", playlistHeading(view))
	if strings.Contains(body, "<form") || strings.Contains(body, "heading-meta") {
		t.Fatal("delete heading must not offer refresh", body)
	}
	assistant := assistantPageView{pageView: pageView{Title: "Assistant", CSRF: "token", StationID: "s"}, AssistantMode: "chat"}
	body = navigationHTML(t, "page-heading", assistantHeading(assistant))
	if !strings.Contains(body, `action="/producer/assistant/conversations"`) || !strings.Contains(body, `name="station" value="s"`) {
		t.Fatal(body)
	}
}
func TestSharedNavigationWorksInFullFragmentsAndHistoryRestoration(t *testing.T) {
	h, st, src, _ := setupWeb(t, "")
	ctx := context.Background()
	if err := st.Save(ctx, station.Station{ID: "s", Name: "Radio & sound", StreamSlug: "radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if err := src.Save(ctx, sources.Config{ID: "source", Name: "Shared music", Kind: "upnp", Endpoint: "http://server/description.xml", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	token := csrf(t, h)
	created := request(h, "POST", "/producer/playlists", url.Values{"csrf": {token}, "station": {"s"}, "name": {"Pool"}}, nil)
	pool := created.Header().Get("Location")
	if pool == "" {
		t.Fatal(created.Code, created.Body.String())
	}
	for _, path := range []string{"/producer/stations/s/settings", "/producer/sources/source/settings", pool + "/tracks"} {
		for _, mode := range []struct {
			name    string
			headers map[string]string
			full    bool
		}{{"ordinary", nil, true}, {"fragment", map[string]string{"HX-Request": "true"}, false}, {"history", map[string]string{"HX-Request": "true", "HX-History-Restore-Request": "true"}, true}} {
			t.Run(path+"/"+mode.name, func(t *testing.T) {
				response := request(h, "GET", path, nil, mode.headers)
				body := response.Body.String()
				if response.Code != 200 || strings.Count(body, "<h1>") != 1 || !strings.Contains(body, `class="workspace-tabs"`) || !strings.Contains(body, `aria-label="Breadcrumb"`) {
					t.Fatal(response.Code, body)
				}
				if strings.Contains(body, "<!doctype html>") != mode.full {
					t.Fatal("incorrect shell for navigation mode", body)
				}
			})
		}
	}
}
