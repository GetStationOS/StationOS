package playlists_test

import (
	"context"
	"fmt"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"testing"
	"time"
)

func TestRefreshCountsOnlyNewEligibleTracksAndAlbums(t *testing.T) {
	svc, repo, cat, source, stations := setup(t)
	ctx := context.Background()
	a := audio(source, "a", "Alpha", "Blues, Jazz, Jazz", "Original album")
	x := audio(source, "x", "Excluded", "Jazz", "Excluded album")
	if err := cat.Replace(ctx, source, []content.AudioItem{a, x}); err != nil {
		t.Fatal(err)
	}
	pool, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Jazz"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, pool.ID, playlists.Rule{Kind: "genre", Value: "Jazz"}); err != nil {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, pool.ID, playlists.Rule{Kind: "genre", Value: "Blues"}); err != nil {
		t.Fatal(err)
	}
	if err = svc.Select(ctx, pool.ID, []string{x.ID}, false); err != nil {
		t.Fatal(err)
	}
	report, err := repo.Refresh(ctx, pool.ID)
	if err != nil || report.NewTracks != 0 || report.NewAlbums != 0 {
		t.Fatal(report, err)
	}
	b := audio(source, "b", "Beta", "Jazz, Blues", a.Album)
	c := audio(source, "c", "Gamma", "Blues, Jazz", "New album")
	d := audio(source, "d", "Delta", "Pop", "Unrelated album")
	if err = cat.Replace(ctx, source, []content.AudioItem{a, b, c, d, x}); err != nil {
		t.Fatal(err)
	}
	report, err = repo.Refresh(ctx, pool.ID)
	if err != nil || report.NewTracks != 2 || report.NewAlbums != 1 {
		t.Fatal(report, err)
	}
	again, err := repo.Refresh(ctx, pool.ID)
	if err != nil || again.NewTracks != 0 || again.NewAlbums != 0 {
		t.Fatal("repeat refresh duplicated additions", again, err)
	}
	rows, err := repo.Review(ctx, pool.ID, 0, 25)
	if err != nil || rows.Total != 3 || rows.Rows != 4 || rows.Excluded != 1 || rows.Duration != 9*time.Minute {
		t.Fatal(rows, err)
	}
	for _, entry := range rows.Entries {
		if entry.Item.ID == x.ID && !entry.Excluded {
			t.Fatal("refresh restored excluded track")
		}
		if entry.Item.ID == d.ID {
			t.Fatal("unrelated recording included")
		}
	}
	playable, err := (persistence.Broadcast{DB: repo.DB}).Pool(ctx, pool.ID)
	if err != nil || len(playable) != 3 {
		t.Fatal(playable, err)
	}
	for _, item := range playable {
		if item.ID == x.ID {
			t.Fatal("excluded audio reached playout")
		}
	}
}
func TestReviewRetainsExcludedSnapshotsAndRestoresUnavailableTracks(t *testing.T) {
	svc, repo, cat, source, stations := setup(t)
	ctx := context.Background()
	a := audio(source, "a", "Alpha", "Jazz", "Album")
	if err := cat.Replace(ctx, source, []content.AudioItem{a}); err != nil {
		t.Fatal(err)
	}
	pool, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Jazz"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, pool.ID, playlists.Rule{Kind: "genre", Value: "Jazz"}); err != nil {
		t.Fatal(err)
	}
	if err = svc.Select(ctx, pool.ID, []string{a.ID}, false); err != nil {
		t.Fatal(err)
	}
	if err = cat.Clear(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	review, err := repo.Review(ctx, pool.ID, 0, 25)
	if err != nil || review.Rows != 1 || review.Total != 0 || review.Excluded != 1 || review.Entries[0].Available || review.Entries[0].Item.Title != "Alpha" {
		t.Fatal(review, err)
	}
	if err = svc.Select(ctx, pool.ID, []string{a.ID}, true); err != nil {
		t.Fatal(err)
	}
	eligible, err := repo.Resolve(ctx, pool.ID, 0, 25)
	if err != nil || eligible.Total != 1 || eligible.Unavailable != 1 {
		t.Fatal(eligible, err)
	}
	if err = cat.Replace(ctx, source, []content.AudioItem{a}); err != nil {
		t.Fatal(err)
	}
	review, err = repo.Review(ctx, pool.ID, 0, 25)
	if err != nil || review.Excluded != 0 || review.Rows != 1 || !review.Entries[0].Available {
		t.Fatal(review, err)
	}
	report, err := repo.Refresh(ctx, pool.ID)
	if err != nil || report.NewTracks != 0 {
		t.Fatal("manual restore counted again", report, err)
	}
}
func TestReviewPaginatesIncludedAndExcludedTogether(t *testing.T) {
	svc, repo, cat, source, stations := setup(t)
	ctx := context.Background()
	a, b := audio(source, "a", "Alpha", "Jazz", "Album"), audio(source, "b", "Beta", "Jazz", "Album")
	if err := cat.Replace(ctx, source, []content.AudioItem{b, a}); err != nil {
		t.Fatal(err)
	}
	pool, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Jazz"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, pool.ID, playlists.Rule{Kind: "genre", Value: "Jazz"}); err != nil {
		t.Fatal(err)
	}
	if err = svc.Select(ctx, pool.ID, []string{a.ID}, false); err != nil {
		t.Fatal(err)
	}
	first, err := repo.Review(ctx, pool.ID, 0, 1)
	if err != nil || first.Rows != 2 || first.Total != 1 || len(first.Entries) != 1 || !first.Entries[0].Excluded {
		t.Fatal(first, err)
	}
	second, err := repo.Review(ctx, pool.ID, 1, 1)
	if err != nil || len(second.Entries) != 1 || second.Entries[0].Item.ID != b.ID || second.Entries[0].Excluded {
		t.Fatal(second, err)
	}
}

func TestSaveSelectionsRefreshesAtomicallyWithoutRestoringExclusions(t *testing.T) {
	svc, repo, cat, source, stations := setup(t)
	ctx := context.Background()
	a, x := audio(source, "a", "Alpha", "Jazz", "Original"), audio(source, "x", "Excluded", "Jazz", "Excluded")
	if err := cat.Replace(ctx, source, []content.AudioItem{a, x}); err != nil {
		t.Fatal(err)
	}
	pool, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Jazz"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, pool.ID, playlists.Rule{Kind: "genre", Value: "Jazz"}); err != nil {
		t.Fatal(err)
	}
	if err = svc.Select(ctx, pool.ID, []string{x.ID}, false); err != nil {
		t.Fatal(err)
	}
	b := audio(source, "b", "Beta", "Jazz", "New album")
	if err = cat.Replace(ctx, source, []content.AudioItem{a, b, x}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ReplaceRulesAndRefresh(ctx, pool.ID, []playlists.Rule{{Kind: "genre", Value: "Pop"}, {Kind: "album", Value: "Missing", SourceID: source.ID}}); err == nil {
		t.Fatal("invalid save succeeded")
	}
	rules, err := repo.Rules(ctx, pool.ID)
	if err != nil || len(rules) != 1 || rules[0].Value != "Jazz" {
		t.Fatal("failed save changed rules", rules, err)
	}
	report, err := svc.ReplaceRulesAndRefresh(ctx, pool.ID, []playlists.Rule{{Kind: "genre", Value: "Jazz"}})
	if err != nil || report.NewTracks != 1 || report.NewAlbums != 1 {
		t.Fatal("save missed imported matches or failed save consumed baseline", report, err)
	}
	review, err := repo.Review(ctx, pool.ID, 0, 25)
	if err != nil || review.Total != 2 || review.Excluded != 1 {
		t.Fatal("save changed exclusions", review, err)
	}
	report, err = repo.Refresh(ctx, pool.ID)
	if err != nil || report.NewTracks != 0 || report.NewAlbums != 0 {
		t.Fatal("save did not advance refresh baseline", report, err)
	}
}

func TestPlaylistSearchIncludesExcludedAndFiltersBeforePagination(t *testing.T) {
	svc, repo, cat, source, stations := setup(t)
	ctx := context.Background()
	items := []content.AudioItem{}
	for i := 0; i < 35; i++ {
		a := audio(source, fmt.Sprint(i), fmt.Sprintf("Song %02d", i), "Jazz", "Time Out")
		a.Artist = "Dave Brubeck Quartet"
		a.Composer = "Paul Desmond"
		items = append(items, a)
	}
	if err := cat.Replace(ctx, source, items); err != nil {
		t.Fatal(err)
	}
	pool, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Jazz"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, pool.ID, playlists.Rule{Kind: "genre", Value: "Jazz"}); err != nil {
		t.Fatal(err)
	}
	if err = svc.Select(ctx, pool.ID, []string{items[34].ID}, false); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"Song 34", "BRUBECK 34", "time out 34", "desmond 34"} {
		found, err := repo.SearchReview(ctx, pool.ID, q, 0, 25)
		if err != nil || found.Rows != 1 || len(found.Entries) != 1 || found.Entries[0].Item.ID != items[34].ID || !found.Entries[0].Excluded {
			t.Fatal(q, found, err)
		}
	}
	found, err := repo.SearchReview(ctx, pool.ID, "Time Out", 25, 25)
	if err != nil || found.Rows != 35 || len(found.Entries) != 10 {
		t.Fatal(found, err)
	}
	absent, err := repo.SearchReview(ctx, pool.ID, "missing", 0, 25)
	if err != nil || absent.Rows != 0 || len(absent.Entries) != 0 {
		t.Fatal(absent, err)
	}
}
