package web

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/sources"
	"strings"
	"testing"
)

func TestSourceWorkspace(t *testing.T) {
	h, _, src, cat := setupWeb(t, "")
	ctx := context.Background()
	token := csrf(t, h)
	source, err := (sources.Service{Repo: src}).Save(ctx, sources.Config{Name: "Server & music", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	item := content.AudioItem{ID: content.ID(source.ID, "a"), SourceID: source.ID, SourceItemID: "a", Title: "Track", Album: "Album", Kind: content.Track, Origin: content.Original}
	if err := cat.Replace(ctx, source, []content.AudioItem{item}); err != nil {
		t.Fatal(err)
	}
	path := "/producer/sources/" + source.ID
	w := request(h, "GET", "/producer/sources", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), path) || !strings.Contains(w.Body.String(), "1 tracks") || strings.Contains(w.Body.String(), "Connection settings") {
		t.Fatal("source index mixes settings", w.Code, w.Body.String())
	}
	w = request(h, "GET", path, nil, map[string]string{"HX-Request": "true"})
	for _, want := range []string{"Source navigation", "Imported tracks", "Refresh library", `name="return_to" value="` + path + `"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatal(want, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), `name="endpoint"`) {
		t.Fatal("overview mixes settings")
	}
	w = request(h, "GET", path+"/settings", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="endpoint"`) || strings.Contains(w.Body.String(), "Refresh library") {
		t.Fatal("settings mixes overview")
	}
	w = request(h, "POST", "/producer/sources", url.Values{"csrf": {token}, "id": {source.ID}, "name": {"Updated"}, "endpoint": {source.Endpoint}}, nil)
	if w.Code != 303 || w.Header().Get("Location") != path+"/settings?notice=source-saved" {
		t.Fatal("source save lost context", w.Code, w.Header())
	}
	if w = request(h, "GET", "/producer/sources/missing", nil, nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestRemoveSourceConfirmationAndCatalogueScope(t *testing.T) {
	h, _, src, cat := setupWeb(t, "")
	ctx := context.Background()
	token := csrf(t, h)
	var configs []sources.Config
	for _, name := range []string{"Remove me", "Keep me"} {
		c, err := (sources.Service{Repo: src}).Save(ctx, sources.Config{Name: name, Kind: "upnp", Endpoint: "http://server/desc"})
		if err != nil {
			t.Fatal(err)
		}
		configs = append(configs, c)
		item := content.AudioItem{ID: content.ID(c.ID, "track"), SourceID: c.ID, SourceItemID: "track", Title: "Track", Kind: content.Track, Origin: content.Original}
		if err := cat.Replace(ctx, c, []content.AudioItem{item}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/producer/sources/" + configs[0].ID
	w := request(h, "GET", path+"/settings", nil, nil)
	if !strings.Contains(w.Body.String(), "Remove source") {
		t.Fatal("missing removal action")
	}
	for _, headers := range []map[string]string{nil, {"HX-Request": "true", "HX-Target": "dialog-body"}} {
		w = request(h, "GET", path+"/delete", nil, headers)
		for _, want := range []string{`id="dialog-heading"`, "Remove me", "Music files stay on your server", "data-close-dialog"} {
			if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
				t.Fatalf("confirmation missing %q: %d %s", want, w.Code, w.Body.String())
			}
		}
		if (headers == nil) != strings.Contains(w.Body.String(), "<!doctype") {
			t.Fatal("wrong confirmation shell")
		}
	}
	for _, tc := range []struct {
		form url.Values
		code int
	}{
		{url.Values{"csrf": {token}}, 400}, {url.Values{"confirm": {"yes"}}, 403},
	} {
		if w = request(h, "POST", path+"/delete", tc.form, nil); w.Code != tc.code {
			t.Fatal(w.Code)
		}
		if _, err := src.Get(ctx, configs[0].ID); err != nil {
			t.Fatal("unconfirmed removal changed data", err)
		}
	}
	w = request(h, "POST", path+"/delete", url.Values{"csrf": {token}, "confirm": {"yes"}}, map[string]string{"HX-Request": "true"})
	if w.Code != 204 || !strings.Contains(w.Header().Get("HX-Location"), "/producer/sources?notice=source-deleted") {
		t.Fatal(w.Code, w.Header())
	}
	if _, err := src.Get(ctx, configs[0].ID); !errors.Is(err, sources.ErrNotFound) {
		t.Fatal("removed source retained", err)
	}
	result, err := cat.Search(ctx, catalogue.Query{Limit: 100})
	if err != nil || result.Total != 1 || result.Items[0].SourceID != configs[1].ID {
		t.Fatal("catalogue deletion escaped source scope", result, err)
	}
	if err := src.Delete(ctx, configs[0].ID); !errors.Is(err, sources.ErrNotFound) {
		t.Fatal("missing source deletion", err)
	}
	if err := cat.Replace(ctx, configs[0], nil); err == nil {
		t.Fatal("in-flight import restored deleted source")
	}
	if w = request(h, "GET", path+"/delete", nil, nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestSourceAlbumBrowserAndTrackPagination(t *testing.T) {
	h, _, src, cat := setupWeb(t, "")
	ctx := context.Background()
	c, err := (sources.Service{Repo: src}).Save(ctx, sources.Config{Name: "Library", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	var items []content.AudioItem
	for a := 0; a < 14; a++ {
		for n := 0; n < 28; n++ {
			id := fmt.Sprintf("%02d-%02d", a, n)
			items = append(items, content.AudioItem{ID: content.ID(c.ID, id), SourceID: c.ID, SourceItemID: id, Title: "Recording " + id, Album: fmt.Sprintf("Album %02d", a), Artist: "Performer", Kind: content.Track, Origin: content.Original})
		}
	}
	if err = cat.Replace(ctx, c, items); err != nil {
		t.Fatal(err)
	}
	base := "/producer/sources/" + c.ID
	w := request(h, "GET", base, nil, nil)
	if w.Code != 200 || strings.Count(w.Body.String(), `class="album-cover-card"`) != 12 || !strings.Contains(w.Body.String(), "Showing 1–12 of 14") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", base+"?page=2", nil, map[string]string{"HX-Request": "true", "HX-Target": "source-album-results"})
	if w.Code != 200 || strings.Count(w.Body.String(), `class="album-cover-card"`) != 2 || strings.Contains(w.Body.String(), "<!doctype") {
		t.Fatal(w.Code, w.Body.String())
	}
	path := base + "/album?album=Album+13&browse_page=2"
	w = request(h, "GET", path, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "album-detail-card") || !strings.Contains(w.Body.String(), "Recording 13-24") || strings.Contains(w.Body.String(), "Recording 13-25") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", path+"&page=2", nil, map[string]string{"HX-Request": "true", "HX-Target": "source-album-tracks"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Recording 13-25") || strings.Contains(w.Body.String(), "album-detail-card") || !strings.Contains(html.UnescapeString(w.Body.String()), "album=Album+13") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", base+"/album?album=missing", nil, nil)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = request(h, "GET", base+"?page=999", nil, nil)
	if w.Code != 200 || strings.Count(w.Body.String(), `class="album-cover-card"`) != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Artwork identities cannot be used to escape the source scope.
	result, err := cat.Search(ctx, catalogue.Query{SourceID: "another-source", ItemID: items[0].ID, Limit: 1})
	if err != nil || result.Total != 0 {
		t.Fatal(result, err)
	}
}
