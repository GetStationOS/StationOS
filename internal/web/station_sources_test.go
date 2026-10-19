package web

import (
	"context"
	"net/url"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/sources"
	"strings"
	"testing"
)

func TestStationSourceCheckboxesAndScopedTrackBrowsing(t *testing.T) {
	h, stations, src, cat := setupWeb(t, "")
	ctx := context.Background()
	token := csrf(t, h)
	var configs []sources.Config
	for _, name := range []string{"MinimServer", "Gerbera"} {
		c, err := (sources.Service{Repo: src}).Save(ctx, sources.Config{Name: name, Kind: "upnp", Endpoint: "http://server/desc"})
		if err != nil {
			t.Fatal(err)
		}
		configs = append(configs, c)
		a := content.AudioItem{ID: content.ID(c.ID, "track"), SourceID: c.ID, SourceItemID: "track", Title: name + " track", Album: name + " album", Genre: name + " genre", Kind: content.Track, Origin: content.Original}
		if err := cat.Replace(ctx, c, []content.AudioItem{a}); err != nil {
			t.Fatal(err)
		}
	}
	form := url.Values{"csrf": {token}, "name": {"Radio"}, "timezone": {"UTC"}, "sources_supplied": {"yes"}, "source_id": {configs[1].ID}}
	w := request(h, "POST", "/producer/station", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	all, err := stations.List(ctx)
	if err != nil || len(all) != 1 || len(all[0].SourceIDs) != 1 || all[0].SourceIDs[0] != configs[1].ID {
		t.Fatal(all, err)
	}
	st := all[0]
	base := "/producer/stations/" + st.ID
	w = request(h, "GET", base+"/settings", nil, nil)
	for _, want := range []string{"Music sources", "MinimServer", "Gerbera", `name="sources_supplied"`, `value="` + configs[1].ID + `" checked`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatal("missing source control", want, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "Find a source") {
		t.Fatal("source settings should not have a search field")
	}
	if strings.Contains(w.Body.String(), `value="`+configs[0].ID+`" checked`) {
		t.Fatal("unchecked source rendered selected")
	}
	if err := (persistence.Playlists{DB: stations.DB}).Save(ctx, playlists.Playlist{ID: "pool", StationID: st.ID, Name: "Music"}); err != nil {
		t.Fatal(err)
	}
	w = request(h, "GET", "/producer/content?playlist=pool", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Gerbera track") || strings.Contains(w.Body.String(), "MinimServer track") {
		t.Fatal("station browsing includes unchecked source", w.Code, w.Body.String())
	}
	w = request(h, "GET", "/producer/playlists/pool", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Gerbera genre") || strings.Contains(w.Body.String(), "MinimServer genre") {
		t.Fatal("playlist choices include unchecked sources", w.Code, w.Body.String())
	}
	form.Set("id", st.ID)
	form.Set("stream_slug", st.StreamSlug)
	form["source_id"] = []string{configs[0].ID, configs[1].ID}
	w = request(h, "POST", "/producer/station", form, map[string]string{"HX-Request": "true"})
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	got, _ := stations.Get(ctx, st.ID)
	if len(got.SourceIDs) != 2 {
		t.Fatal("multiple sources not saved", got)
	}
	// Validation must retain the attempted checkbox choices without changing saved data.
	form.Set("timezone", "invalid/timezone")
	form["source_id"] = []string{configs[0].ID}
	w = request(h, "POST", "/producer/station", form, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), `value="`+configs[0].ID+`" checked`) {
		t.Fatal("validation lost source choices", w.Code, w.Body.String())
	}
	form.Set("timezone", "UTC")
	form.Del("source_id")
	w = request(h, "POST", "/producer/station", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	got, _ = stations.Get(ctx, st.ID)
	if got.SourceIDs == nil || len(got.SourceIDs) != 0 {
		t.Fatal("clear did not persist", got)
	}
	w = request(h, "GET", "/producer/content?playlist=pool", nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Gerbera track") || strings.Contains(w.Body.String(), "MinimServer track") {
		t.Fatal("empty source selection exposed music", w.Code, w.Body.String())
	}
	form["source_id"] = []string{configs[0].ID, "missing"}
	w = request(h, "POST", "/producer/station", form, nil)
	if w.Code != 422 {
		t.Fatal("unknown source not rejected", w.Code, w.Body.String())
	}
	got, _ = stations.Get(ctx, st.ID)
	if len(got.SourceIDs) != 0 {
		t.Fatal("invalid save changed selection")
	}
}
