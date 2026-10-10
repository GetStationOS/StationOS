package web

import (
	"context"
	"net/url"
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
	path := "/sources/" + source.ID
	w := request(h, "GET", "/sources", nil, nil)
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
	w = request(h, "POST", "/sources", url.Values{"csrf": {token}, "id": {source.ID}, "name": {"Updated"}, "endpoint": {source.Endpoint}}, nil)
	if w.Code != 303 || w.Header().Get("Location") != path+"/settings?notice=source-saved" {
		t.Fatal("source save lost context", w.Code, w.Header())
	}
	if w = request(h, "GET", "/sources/missing", nil, nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
