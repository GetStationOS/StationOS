package playlists_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"testing"
)

func TestRichRulesResolveCreditsDatesAndIntersections(t *testing.T) {
	svc, repo, cat, source, stations := setup(t)
	ctx := context.Background()
	items := []content.AudioItem{}
	for _, r := range []struct {
		id, genre, artist, composer string
		year                        int
	}{
		{"a", "Jazz, Blues", "Miles Davis; John Coltrane", "Davis, Miles", 1959},
		{"b", "Jazz", "Miles Davis", "Other", 1969},
		{"c", "Jazz", "Miles Davis", "Davis, Miles", 1970},
		{"d", "Pop", "Other", "Davis, Miles", 1959},
		{"e", "Jazz", "Miles Davis", "Davis, Miles", 0},
		{"f", "Jazz", "Miles Davis Quartet", "Other", 1959},
	} {
		item := audio(source, r.id, r.id, r.genre, "Album")
		item.Artist = r.artist
		item.Composer = r.composer
		item.Year = r.year
		items = append(items, item)
	}
	if err := cat.Replace(ctx, source, items); err != nil {
		t.Fatal(err)
	}
	pool, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Rich rules"})
	if err != nil {
		t.Fatal(err)
	}
	combined, _ := json.Marshal(playlists.Filter{Genre: "Jazz", Artist: "Miles Davis", Composer: "Davis, Miles", YearFrom: 1950, YearTo: 1969})
	for _, tc := range []struct {
		kind, value string
		want        []string
	}{
		{"artist", "Miles Davis", []string{"a", "b", "c", "e"}},
		{"artist", "John Coltrane", []string{"a"}},
		{"artist", "Miles", nil},
		{"composer", "Davis, Miles", []string{"a", "c", "d", "e"}},
		{"year", "1959", []string{"a", "d", "f"}},
		{"decade", "1960", []string{"b"}},
		{"filter", string(combined), []string{"a"}},
		{"filter", `{"year_to":1969}`, []string{"a", "b", "d", "f"}},
		{"filter", `{"year_from":1970}`, []string{"c"}},
		{"filter", `{"artist":"Miles Davis","genre":"Pop"}`, nil},
	} {
		t.Run(tc.kind+tc.value, func(t *testing.T) {
			if err := svc.ReplaceRules(ctx, pool.ID, []playlists.Rule{{Kind: tc.kind, Value: tc.value}}); err != nil {
				t.Fatal(err)
			}
			result, err := repo.Resolve(ctx, pool.ID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, e := range result.Entries {
				ids = append(ids, e.Item.SourceItemID)
			}
			sort.Strings(ids)
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("got %v want %v", ids, tc.want)
			}
			counts, err := repo.Availability(ctx, pool.ID)
			if err != nil || counts.Total != len(tc.want) {
				t.Fatal(counts, err)
			}
			tracks, err := (persistence.Broadcast{DB: repo.DB}).Pool(ctx, pool.ID)
			if err != nil || len(tracks) != len(tc.want) {
				t.Fatal(tracks, err)
			}
		})
	}
	// Categories intersect; manual inclusions and overriding exclusions remain consistent.
	if err := svc.ReplaceRules(ctx, pool.ID, []playlists.Rule{{Kind: "filter", Value: string(combined)}, {Kind: "year", Value: "1959"}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Select(ctx, pool.ID, []string{items[1].ID}, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Select(ctx, pool.ID, []string{items[0].ID}, false); err != nil {
		t.Fatal(err)
	}
	result, err := repo.Resolve(ctx, pool.ID, 0, 100)
	if err != nil || result.Total != 1 {
		t.Fatal(result, err)
	}
	review, err := repo.Review(ctx, pool.ID, 0, 100)
	if err != nil || review.Excluded != 1 || review.Rows != 2 {
		t.Fatal(review, err)
	}
	facets, err := cat.Facets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(facets.Artists) != 4 || len(facets.Composers) != 2 {
		t.Fatal(facets)
	}
	// All targeted catalogue lookup conditions are conjunctive too.
	found, err := cat.Search(ctx, catalogue.Query{Artist: "Miles Davis", Composer: "Davis, Miles", YearTo: 1969, Limit: 100})
	if err != nil || found.Total != 1 {
		t.Fatal(found, err)
	}
	// Changes and newly imported matches are live; exclusions survive source clear/rebuild.
	added := items[0]
	added.ID = content.ID(source.ID, "new")
	added.SourceItemID = "new"
	items = append(items, added)
	if err := cat.Clear(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if err := cat.Replace(ctx, source, items); err != nil {
		t.Fatal(err)
	}
	report, err := repo.Refresh(ctx, pool.ID)
	if err != nil || report.NewTracks != 1 {
		t.Fatal(report, err)
	}
	report, err = repo.Refresh(ctx, pool.ID)
	if err != nil || report.NewTracks != 0 {
		t.Fatal(report, err)
	}
	items[0].Artist = "New credit"
	items[0].Composer = "New composer"
	if err := cat.Replace(ctx, source, items); err != nil {
		t.Fatal(err)
	}
	var stale int
	if err := repo.DB.QueryRow(`SELECT count(*) FROM catalogue_credits WHERE content_id=? AND name='Miles Davis'`, items[0].ID).Scan(&stale); err != nil || stale != 0 {
		t.Fatal(stale, err)
	}
}
func TestRichRuleValidationAndAtomicFailure(t *testing.T) {
	for _, rule := range []playlists.Rule{
		{Kind: "year", Value: "0"}, {Kind: "year", Value: "01959"}, {Kind: "year", Value: "10000"}, {Kind: "decade", Value: "1985"},
		{Kind: "filter", Value: `{}`}, {Kind: "filter", Value: `{"year_from":1970,"year_to":1960}`},
		{Kind: "filter", Value: `{"artist":"Miles","unknown":true}`}, {Kind: "filter", Value: `{"artist":"Miles"} {}`},
		{Kind: "filter", Value: `{"album":"Album"}`}, {Kind: "filter", Value: `{"year_from":"1980"}`},
	} {
		if err := rule.Validate(); !errors.Is(err, playlists.ErrInvalid) {
			t.Errorf("accepted %+v: %v", rule, err)
		}
	}
	svc, repo, _, _, stations := setup(t)
	ctx := context.Background()
	pool, _ := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Preserve"})
	if err := svc.AddRule(ctx, pool.ID, playlists.Rule{Kind: "artist", Value: "Keep"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReplaceRules(ctx, pool.ID, []playlists.Rule{{Kind: "composer", Value: "New"}, {Kind: "year", Value: "bad"}}); !errors.Is(err, playlists.ErrInvalid) {
		t.Fatal(err)
	}
	rules, err := repo.Rules(ctx, pool.ID)
	if err != nil || len(rules) != 1 || rules[0].Value != "Keep" {
		t.Fatal(rules, err)
	}
}
