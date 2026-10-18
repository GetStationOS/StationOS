package playlists_test

import (
	"context"
	"reflect"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"testing"
)

func TestSelectionCategoriesIntersectAndValuesAreAlternatives(t *testing.T) {
	svc, repo, cat, source, stations := setup(t)
	ctx := context.Background()
	var items []content.AudioItem
	for _, v := range []struct {
		id, genre, artist, album string
		year                     int
	}{
		{"a", "Pop", "One", "First", 1984}, {"b", "Pop", "Two", "Second", 1994},
		{"c", "Rock", "One", "First", 1987}, {"d", "Jazz", "One", "First", 1982},
		{"e", "Pop", "One", "First", 0}, {"f", "Rock", "Two", "Second", 1974},
	} {
		a := audio(source, v.id, v.id, v.genre, v.album)
		a.Artist = v.artist
		a.Composer = "Writer"
		a.Year = v.year
		items = append(items, a)
	}
	if err := cat.Replace(ctx, source, items); err != nil {
		t.Fatal(err)
	}
	pool, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Categories"})
	if err != nil {
		t.Fatal(err)
	}
	genre := func(v string) playlists.Rule { return playlists.Rule{Kind: "genre", Value: v} }
	decade := func(v string) playlists.Rule { return playlists.Rule{Kind: "decade", Value: v} }
	for _, tc := range []struct {
		name  string
		rules []playlists.Rule
		want  []string
	}{
		{"empty", nil, nil},
		{"pop only", []playlists.Rule{genre("Pop")}, []string{"a", "b", "e"}},
		{"pop eighties", []playlists.Rule{genre("Pop"), decade("1980")}, []string{"a"}},
		{"genres OR decades OR", []playlists.Rule{genre("Pop"), genre("Rock"), decade("1980"), decade("1990")}, []string{"a", "b", "c"}},
		{"artist intersects", []playlists.Rule{genre("Pop"), genre("Rock"), decade("1980"), {Kind: "artist", Value: "One"}}, []string{"a", "c"}},
		{"album intersects", []playlists.Rule{genre("Pop"), {Kind: "album", Value: "First", SourceID: source.ID}}, []string{"a", "e"}},
		{"years and decades same category", []playlists.Rule{genre("Pop"), {Kind: "year", Value: "1994"}, decade("1980")}, []string{"a", "b"}},
		{"no match", []playlists.Rule{genre("Jazz"), decade("1990")}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.ReplaceRules(ctx, pool.ID, tc.rules); err != nil {
				t.Fatal(err)
			}
			result, err := repo.Resolve(ctx, pool.ID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, e := range result.Entries {
				got = append(got, e.Item.SourceItemID)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			counts, err := repo.Availability(ctx, pool.ID)
			if err != nil || counts.Total != len(tc.want) {
				t.Fatal(counts, err)
			}
			tracks, err := (persistence.Broadcast{DB: repo.DB}).Pool(ctx, pool.ID)
			if err != nil || len(tracks) != len(tc.want) {
				t.Fatal(tracks, err)
			}
			review, err := repo.Review(ctx, pool.ID, 0, 100)
			if err != nil || review.Total != len(tc.want) {
				t.Fatal(review, err)
			}
		})
	}
	if err := svc.ReplaceRules(ctx, pool.ID, []playlists.Rule{genre("Pop"), decade("1980")}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Select(ctx, pool.ID, []string{items[3].ID}, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Select(ctx, pool.ID, []string{items[0].ID}, false); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Resolve(ctx, pool.ID, 0, 100)
	if err != nil || got.Total != 1 || got.Entries[0].Item.SourceItemID != "d" {
		t.Fatal(got, err)
	}
	// Refresh adds only new tracks matching all categories, not unrelated eighties music.
	added := items[0]
	added.SourceItemID = "new"
	added.ID = content.ID(source.ID, "new")
	other := items[3]
	other.SourceItemID = "unrelated"
	other.ID = content.ID(source.ID, "unrelated")
	if err := cat.Replace(ctx, source, append(items, added, other)); err != nil {
		t.Fatal(err)
	}
	report, err := repo.Refresh(ctx, pool.ID)
	if err != nil || report.NewTracks != 1 {
		t.Fatal(report, err)
	}
}
