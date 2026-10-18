package web

import (
	"context"
	"encoding/json"
	"net/url"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
)

func TestCombinedRuleJourneyPreservesSelectionsAndValidation(t *testing.T) {
	h, stations, sourceRepo, cat := setupWeb(t, "")
	ctx := context.Background()
	if err := stations.Save(ctx, station.Station{ID: "s", Name: "Radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	source, err := (sources.Service{Repo: sourceRepo}).Save(ctx, sources.Config{Name: "Library", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	a := content.AudioItem{ID: content.ID(source.ID, "a"), SourceID: source.ID, SourceItemID: "a", Title: "Alpha", Artist: "Miles Davis", Composer: "Writer", Genre: "Jazz", Year: 1959, Album: "Album", Kind: content.Track, Origin: content.Original}
	if err = cat.Replace(ctx, source, []content.AudioItem{a}); err != nil {
		t.Fatal(err)
	}
	repo := persistence.Playlists{DB: stations.DB}
	svc := playlists.Service{Repo: repo, Stations: stations}
	pool, err := svc.Save(ctx, playlists.Playlist{StationID: "s", Name: "Music"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/producer/playlists/" + pool.ID
	token := csrf(t, h)
	for _, route := range []string{path} {
		w := request(h, "GET", route, nil, nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	values := url.Values{"csrf": {token}, "genre": {"Jazz"}, "artist": {"Miles Davis"}, "year_from": {"1950"}, "year_to": {"1969"}}
	rawFilter, _ := json.Marshal(playlists.Filter{Genre: "Jazz", Artist: "Miles Davis", YearFrom: 1950, YearTo: 1969})
	if err := svc.AddRule(ctx, pool.ID, playlists.Rule{Kind: "filter", Value: string(rawFilter)}); err != nil {
		t.Fatal(err)
	}
	w := request(h, "GET", path, nil, nil)
	rules, _ := repo.Rules(ctx, pool.ID)
	if len(rules) != 1 || rules[0].Kind != "filter" {
		t.Fatal(rules)
	}
	filterRule := rules[0]
	w = request(h, "GET", path, nil, nil)
	for _, want := range []string{`name="artist"`, `name="composer"`, `name="year"`, `name="decade"`, `Saved advanced restrictions`, `Genre: Jazz`, "/edit"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatal(want, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), `name="filter"`) || strings.Contains(w.Body.String(), ">Combined rules<") || strings.Contains(w.Body.String(), "Add combined rule") {
		t.Fatal("redundant selector remains", w.Body.String())
	}
	// Category saves preserve prior advanced restrictions without a selector.
	w = request(h, "POST", path+"/selections", url.Values{"csrf": {token}, "artist": {"Miles Davis"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	rules, _ = repo.Rules(ctx, pool.ID)
	if len(rules) != 2 {
		t.Fatal(rules)
	}
	for _, r := range rules {
		if r.Kind == "filter" {
			filterRule = r
		}
	}
	editURL := path + "/rules/" + filterRule.ID + "/edit"
	values.Set("year_from", "1970")
	w = request(h, "POST", editURL, values, map[string]string{"HX-Request": "true"})
	if w.Code != 422 || !strings.Contains(w.Body.String(), `value="1970"`) || !strings.Contains(w.Body.String(), `data-playlist-filter`) {
		t.Fatal(w.Code, w.Body.String())
	}
	unchanged, _ := repo.Rules(ctx, pool.ID)
	if len(unchanged) != 2 {
		t.Fatal(unchanged)
	}
	values.Set("year_from", "1959")
	values.Set("year_to", "1959")
	w = request(h, "POST", editURL, values, map[string]string{"HX-Request": "true"})
	if w.Code != 204 || w.Header().Get("HX-Location") == "" {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	rules, _ = repo.Rules(ctx, pool.ID)
	if len(rules) != 2 {
		t.Fatal(rules)
	}
	for _, r := range rules {
		if r.Kind == "filter" {
			var f playlists.Filter
			_ = json.Unmarshal([]byte(r.Value), &f)
			if f.YearFrom != 1959 || f.YearTo != 1959 {
				t.Fatal(f)
			}
		}
	}
	// A source-only filter created by the assistant remains editable without losing scope.
	if err := svc.AddRule(ctx, pool.ID, playlists.Rule{Kind: "filter", Value: `{"source_id":"` + source.ID + `"}`}); err != nil {
		t.Fatal(err)
	}
	current, _ := repo.Rules(ctx, pool.ID)
	var sourceRule playlists.Rule
	for _, rule := range current {
		if rule.Value == `{"source_id":"`+source.ID+`"}` {
			sourceRule = rule
		}
	}
	sourceEdit := path + "/rules/" + sourceRule.ID + "/edit"
	w = request(h, "GET", sourceEdit, nil, nil)
	if !strings.Contains(w.Body.String(), `value="`+source.ID+`" selected`) {
		t.Fatal(w.Body.String())
	}
	w = request(h, "POST", sourceEdit, url.Values{"csrf": {token}, "source_id": {source.ID}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	// A conflicting source/album retains both inputs and saves nothing.
	current, _ = repo.Rules(ctx, pool.ID)
	for _, rule := range current {
		f, e := playlists.ParseFilter(rule.Value)
		if rule.Kind == "filter" && e == nil && f.Artist == "" && f.SourceID == source.ID {
			sourceEdit = path + "/rules/" + rule.ID + "/edit"
		}
	}
	other, err := (sources.Service{Repo: sourceRepo}).Save(ctx, sources.Config{Name: "Other Library", Kind: "upnp", Endpoint: "http://other/desc"})
	if err != nil {
		t.Fatal(err)
	}
	albumToken, _ := json.Marshal(albumChoice{SourceID: source.ID, Name: "Album"})
	w = request(h, "POST", sourceEdit, url.Values{"csrf": {token}, "source_id": {other.ID}, "album": {string(albumToken)}}, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), `value="`+other.ID+`" selected`) || !strings.Contains(w.Body.String(), `>Album · Library</option>`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// Restore the earlier two-rule set for bulk-edit rollback assertions.
	rules, _ = repo.Rules(ctx, pool.ID)
	kept := []playlists.Rule{}
	for _, rule := range rules {
		f, e := playlists.ParseFilter(rule.Value)
		if rule.Kind != "filter" || e != nil || f.Artist != "" {
			kept = append(kept, rule)
		}
	}
	if err := svc.ReplaceRules(ctx, pool.ID, kept); err != nil {
		t.Fatal(err)
	}
	// Invalid bulk filters roll back; unchecking filters removes them explicitly.
	w = request(h, "POST", path+"/selections", url.Values{"csrf": {token}, "filter": {`{"unknown":true}`}}, nil)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	rules, _ = repo.Rules(ctx, pool.ID)
	if len(rules) != 2 {
		t.Fatal(rules)
	}
	w = request(h, "POST", path+"/selections", url.Values{"csrf": {token}, "composer": {"Writer"}, "year": {"1959"}, "decade": {"1950"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	result, err := repo.Resolve(ctx, pool.ID, 0, 100)
	if err != nil || result.Total != 1 {
		t.Fatal(result, err)
	}
	if w = request(h, "POST", path+"/rules/new", url.Values{"artist": {"X"}}, nil); w.Code != 403 {
		t.Fatal("CSRF not enforced", w.Code)
	}
	if w = request(h, "GET", path+"/rules/new", nil, nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestPopAndDecadeSelectionJourneyHasNoCombinedSelector(t *testing.T) {
	h, stations, sourceRepo, cat := setupWeb(t, "")
	ctx := context.Background()
	if err := stations.Save(ctx, station.Station{ID: "s", Name: "Radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	source, err := (sources.Service{Repo: sourceRepo}).Save(ctx, sources.Config{Name: "Library", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	var items []content.AudioItem
	for _, v := range []struct {
		id, genre string
		year      int
	}{{"Eighties Pop", "Pop", 1984}, {"Nineties Pop", "Pop", 1994}, {"Eighties Rock", "Rock", 1987}, {"Undated Pop", "Pop", 0}} {
		items = append(items, content.AudioItem{ID: content.ID(source.ID, v.id), SourceID: source.ID, SourceItemID: v.id, Title: v.id, Genre: v.genre, Year: v.year, Kind: content.Track, Origin: content.Original})
	}
	if err = cat.Replace(ctx, source, items); err != nil {
		t.Fatal(err)
	}
	repo := persistence.Playlists{DB: stations.DB}
	svc := playlists.Service{Repo: repo, Stations: stations}
	pool, err := svc.Save(ctx, playlists.Playlist{StationID: "s", Name: "Music"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/producer/playlists/" + pool.ID
	token := csrf(t, h)
	w := request(h, "GET", path, nil, nil)
	for _, absent := range []string{"Combined rules", "Add combined rule", "Saved advanced restrictions", `name="filter"`} {
		if strings.Contains(w.Body.String(), absent) {
			t.Fatal("unexpected selector", absent)
		}
	}
	w = request(h, "POST", path+"/selections", url.Values{"csrf": {token}, "genre": {"Pop"}, "decade": {"1980"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	result, err := repo.Resolve(ctx, pool.ID, 0, 100)
	if err != nil || result.Total != 1 || result.Entries[0].Item.Title != "Eighties Pop" {
		t.Fatal(result, err)
	}
	w = request(h, "GET", path, nil, nil)
	for _, want := range []string{`name="genre" value="Pop" checked`, `name="decade" value="1980" checked`, "1 tracks", "Categories combine"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatal(want, w.Body.String())
		}
	}
	w = request(h, "GET", path+"/tracks", nil, nil)
	if !strings.Contains(w.Body.String(), "Eighties Pop") || strings.Contains(w.Body.String(), "Nineties Pop") || strings.Contains(w.Body.String(), "Eighties Rock") {
		t.Fatal(w.Body.String())
	}
	// HTMX uses the same save contract; adding Rock broadens only the genre category.
	w = request(h, "POST", path+"/selections", url.Values{"csrf": {token}, "genre": {"Pop", "Rock"}, "decade": {"1980"}}, map[string]string{"HX-Request": "true"})
	if w.Code != 204 || w.Header().Get("HX-Location") == "" {
		t.Fatal(w.Code, w.Header())
	}
	counts, err := repo.Availability(ctx, pool.ID)
	if err != nil || counts.Total != 2 {
		t.Fatal(counts, err)
	}
}
