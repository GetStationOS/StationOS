package playlists_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

func setup(t *testing.T) (playlists.Service, persistence.Playlists, persistence.Catalogue, sources.Config, []station.Station) {
	t.Helper()
	ctx := context.Background()
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	source, err := (sources.Service{Repo: persistence.Sources{DB: db}}).Save(ctx, sources.Config{Name: "Shared music", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	st := persistence.Stations{DB: db}
	stations := []station.Station{}
	for _, name := range []string{"Jazz station", "Pop station", "Classical station"} {
		saved, err := (station.Service{Repo: st}).Save(ctx, station.Station{Name: name, Timezone: "UTC"})
		if err != nil {
			t.Fatal(err)
		}
		stations = append(stations, saved)
	}

	repo := persistence.Playlists{DB: db}
	return playlists.Service{Repo: repo, Stations: st}, repo, persistence.Catalogue{DB: db}, source, stations
}
func audio(c sources.Config, id, title, genre, album string) content.AudioItem {
	d := 3 * time.Minute
	return content.AudioItem{ID: content.ID(c.ID, id), SourceID: c.ID, SourceItemID: id, Title: title, Artist: "Artist", Album: album, Genre: genre, Duration: &d, Kind: content.Track, Origin: content.Original}
}
func TestStationPlaylistsShareCatalogueAndResolveCategories(t *testing.T) {
	svc, repo, cat, c, stations := setup(t)
	ctx := context.Background()
	jazz := audio(c, "j", "Blue", "Jazz", "Blue album")
	pop := audio(c, "p", "Pop", "Pop", "Pop album")
	classic := audio(c, "c", "Sonata", "Classical", "Sonatas")
	if err := cat.Replace(ctx, c, []content.AudioItem{jazz, pop, classic}); err != nil {
		t.Fatal(err)
	}
	jp, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Jazz rotation"})
	if err != nil {
		t.Fatal(err)
	}
	pp, err := svc.Save(ctx, playlists.Playlist{StationID: stations[1].ID, Name: "Pop rotation"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, jp.ID, playlists.Rule{Kind: "genre", Value: "Jazz"}); err != nil {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, jp.ID, playlists.Rule{Kind: "album", Value: "Blue album", SourceID: c.ID}); err != nil {
		t.Fatal(err)
	}
	// A manual selection overlapping a rule must not duplicate the track.
	if err = svc.Select(ctx, jp.ID, []string{jazz.ID}, true); err != nil {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, pp.ID, playlists.Rule{Kind: "genre", Value: "Pop"}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Resolve(ctx, jp.ID, 0, 100)
	if err != nil || got.Total != 1 || got.Duration != 3*time.Minute {
		t.Fatal(got, err)
	}
	other, err := repo.Resolve(ctx, pp.ID, 0, 100)
	if err != nil || other.Total != 1 || other.Entries[0].Item.ID != pop.ID {
		t.Fatal(other, err)
	}
	// Newly imported matches are automatically included in only the matching stations.
	newJazz := audio(c, "new", "New jazz", "Jazz", "Blue album")
	if err = cat.Replace(ctx, c, []content.AudioItem{jazz, pop, classic, newJazz}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Resolve(ctx, jp.ID, 0, 100)
	if err != nil || got.Total != 2 {
		t.Fatal(got, err)
	}
	other, _ = repo.Resolve(ctx, pp.ID, 0, 100)
	if other.Total != 1 {
		t.Fatal("station selections leaked")
	}
	shared, err := cat.Search(ctx, catalogue.Query{Limit: 100})
	if err != nil || shared.Total != 4 {
		t.Fatal(shared, err)
	}
	list, err := repo.List(ctx, stations[0].ID)
	if err != nil || len(list) != 1 || list[0].ID != jp.ID {
		t.Fatal(list, err)
	}
}
func TestSelectionsSurviveRebuildAndExclusionsWin(t *testing.T) {
	svc, repo, cat, c, stations := setup(t)
	ctx := context.Background()
	a := audio(c, "a", "Alpha", "Jazz", "Album")
	b := audio(c, "b", "Beta", "Jazz", "Album")
	if err := cat.Replace(ctx, c, []content.AudioItem{a, b}); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Subset"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, p.ID, playlists.Rule{Kind: "genre", Value: "Jazz"}); err != nil {
		t.Fatal(err)
	}
	if err = svc.Select(ctx, p.ID, []string{a.ID}, false); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Resolve(ctx, p.ID, 0, 100)
	if got.Total != 1 || got.Entries[0].Item.ID != b.ID {
		t.Fatal(got)
	}
	if err = svc.Select(ctx, p.ID, []string{b.ID}, true); err != nil {
		t.Fatal(err)
	}
	if err = cat.Clear(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Resolve(ctx, p.ID, 0, 100)
	if err != nil || got.Total != 1 || got.Unavailable != 1 || got.Entries[0].Item.Title != "Beta" {
		t.Fatal(got, err)
	}
	rules, _ := repo.Rules(ctx, p.ID)
	if len(rules) != 1 {
		t.Fatal("authoritative rule deleted")
	}
	if err = cat.Replace(ctx, c, []content.AudioItem{a, b}); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Resolve(ctx, p.ID, 0, 100)
	if got.Total != 1 || got.Unavailable != 0 {
		t.Fatal(got)
	}
	if err = svc.Select(ctx, p.ID, []string{a.ID}, true); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Resolve(ctx, p.ID, 0, 100)
	if got.Total != 2 {
		t.Fatal(got)
	}
	if err = repo.RemoveRule(ctx, p.ID, rules[0].ID); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Resolve(ctx, p.ID, 0, 100)
	if got.Total != 2 {
		t.Fatal("manual choices lost with rule removal")
	}
}
func TestPoolBrowsingAndTransactionalSelectionFailure(t *testing.T) {
	svc, repo, cat, c, stations := setup(t)
	ctx := context.Background()
	a := audio(c, "a", "Alpha", "Pop", "Album")
	b := audio(c, "b", "Beta", "Pop", "Album")
	if err := cat.Replace(ctx, c, []content.AudioItem{b, a}); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Pool"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, p.ID, playlists.Rule{Kind: "genre", Value: "Pop"}); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Resolve(ctx, p.ID, 0, 1)
	if got.Total != 2 || got.Entries[0].Item.ID != a.ID {
		t.Fatal(got)
	}
	if err = cat.Replace(ctx, c, []content.AudioItem{a, b}); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Resolve(ctx, p.ID, 0, 100)
	if got.Entries[0].Item.ID != a.ID {
		t.Fatal("rebuild changed stable browsing order")
	}
	if err = svc.Select(ctx, p.ID, []string{a.ID, "missing"}, false); !errors.Is(err, playlists.ErrUnavailable) {
		t.Fatal(err)
	}
	got, _ = repo.Resolve(ctx, p.ID, 0, 100)
	if got.Total != 2 {
		t.Fatal("partial selection update committed")
	}
	p.Name = "Renamed"
	if _, err = svc.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	saved, _ := repo.Get(ctx, p.ID)
	if saved.Name != "Renamed" {
		t.Fatal(saved)
	}
	p.StationID = stations[1].ID
	if _, err = svc.Save(ctx, p); !errors.Is(err, playlists.ErrInvalid) {
		t.Fatal("playlist moved to other station", err)
	}
}
func TestPlaylistValidationAndMissingItems(t *testing.T) {
	svc, repo, _, _, stations := setup(t)
	ctx := context.Background()
	for _, p := range []playlists.Playlist{{Name: "No owner"}, {StationID: stations[0].ID}, {StationID: "missing", Name: "Wrong station"}} {
		if _, err := svc.Save(ctx, p); !errors.Is(err, playlists.ErrInvalid) {
			t.Fatal(p, err)
		}
	}
	p, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Empty"})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []playlists.Rule{{Kind: "genre"}, {Kind: "wrong", Value: "Jazz"}, {Kind: "album", Value: "Album"}, {Kind: "genre", Value: "Jazz", SourceID: "source"}} {
		if err := svc.AddRule(ctx, p.ID, rule); !errors.Is(err, playlists.ErrInvalid) {
			t.Fatal(rule, err)
		}
	}
	if err = svc.Select(ctx, p.ID, nil, true); !errors.Is(err, playlists.ErrInvalid) {
		t.Fatal(err)
	}
	if err = svc.Select(ctx, p.ID, []string{"missing"}, true); !errors.Is(err, playlists.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err = repo.Resolve(ctx, p.ID, -1, 25); !errors.Is(err, playlists.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err = repo.Get(ctx, "missing"); !errors.Is(err, playlists.ErrNotFound) {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, p.ID, playlists.Rule{Kind: "album", Value: "Missing", SourceID: "source"}); !errors.Is(err, playlists.ErrUnavailable) {
		t.Fatal(err)
	}
	if err = repo.RemoveRule(ctx, p.ID, "missing"); !errors.Is(err, playlists.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestRuleLimitAllowsDuplicateAndRepositoryPreservesOwner(t *testing.T) {
	svc, repo, _, _, stations := setup(t)
	ctx := context.Background()
	p, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Rules"})
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < playlists.MaxRules; n++ {
		if err = svc.AddRule(ctx, p.ID, playlists.Rule{Kind: "genre", Value: fmt.Sprintf("Genre %d", n)}); err != nil {
			t.Fatal(err)
		}
	}
	if err = svc.AddRule(ctx, p.ID, playlists.Rule{Kind: "genre", Value: "Genre 0"}); err != nil {
		t.Fatal(err)
	}
	if err = svc.AddRule(ctx, p.ID, playlists.Rule{Kind: "genre", Value: "Over limit"}); !errors.Is(err, playlists.ErrInvalid) {
		t.Fatal(err)
	}
	rules, _ := repo.Rules(ctx, p.ID)
	if len(rules) != playlists.MaxRules {
		t.Fatal(len(rules))
	}
	p.StationID = stations[1].ID
	if err = repo.Save(ctx, p); !errors.Is(err, playlists.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestBatchSelectionsAtomicAndPreserveManualChoices(t *testing.T) {
	svc, repo, cat, c, stations := setup(t)
	ctx := context.Background()
	a := audio(c, "a", "Jazz track", "Jazz", "Jazz album")
	b := audio(c, "b", "Pop track", "Pop", "Pop album")
	if err := cat.Replace(ctx, c, []content.AudioItem{a, b}); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "Mixed"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Select(ctx, p.ID, []string{a.ID}, true); err != nil {
		t.Fatal(err)
	}
	rules := []playlists.Rule{{Kind: "genre", Value: "Jazz"}, {Kind: "album", Value: "Pop album", SourceID: c.ID}}
	if err = svc.ReplaceRules(ctx, p.ID, rules); err != nil {
		t.Fatal(err)
	}
	result, _ := repo.Resolve(ctx, p.ID, 0, 100)
	if result.Total != 1 {
		t.Fatal(result)
	}
	if err = svc.ReplaceRules(ctx, p.ID, []playlists.Rule{{Kind: "album", Value: "Missing", SourceID: c.ID}}); !errors.Is(err, playlists.ErrUnavailable) {
		t.Fatal(err)
	}
	saved, _ := repo.Rules(ctx, p.ID)
	if len(saved) != 2 {
		t.Fatal("failed save changed rules", saved)
	}
	if err = cat.Clear(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	// Re-saving temporarily unavailable choices keeps them intact.
	if err = svc.ReplaceRules(ctx, p.ID, rules); err != nil {
		t.Fatal(err)
	}
	if err = svc.ReplaceRules(ctx, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	saved, _ = repo.Rules(ctx, p.ID)
	result, _ = repo.Resolve(ctx, p.ID, 0, 100)
	if len(saved) != 0 || result.Total != 1 || result.Unavailable != 1 {
		t.Fatal(saved, result)
	}
	if err = svc.ReplaceRules(ctx, "missing", nil); !errors.Is(err, playlists.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestDeleteStationAndPlaylistPreserveSharedMusic(t *testing.T) {
	svc, repo, cat, source, stations := setup(t)
	ctx := context.Background()
	item := audio(source, "one", "One", "Jazz", "Album")
	if err := cat.Replace(ctx, source, []content.AudioItem{item}); err != nil {
		t.Fatal(err)
	}
	first, err := svc.Save(ctx, playlists.Playlist{StationID: stations[0].ID, Name: "First"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Save(ctx, playlists.Playlist{StationID: stations[1].ID, Name: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []playlists.Playlist{first, second} {
		if err := svc.AddRule(ctx, p.ID, playlists.Rule{Kind: "genre", Value: "Jazz"}); err != nil {
			t.Fatal(err)
		}
		if err := svc.Select(ctx, p.ID, []string{item.ID}, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Delete(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, first.ID); !errors.Is(err, playlists.ErrNotFound) {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, first.ID); !errors.Is(err, playlists.ErrNotFound) {
		t.Fatal(err)
	}
	for _, table := range []string{"playlist_rules", "playlist_selections"} {
		var count int
		if err := repo.DB.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE playlist_id=?", first.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
	stationRepo := persistence.Stations{DB: repo.DB}
	if err := stationRepo.Delete(ctx, stations[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, second.ID); !errors.Is(err, playlists.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := stationRepo.Get(ctx, stations[1].ID); !errors.Is(err, station.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := stationRepo.Get(ctx, stations[0].ID); err != nil {
		t.Fatal("other station lost", err)
	}
	result, err := cat.Search(ctx, catalogue.Query{Limit: 1})
	if err != nil || result.Total != 1 {
		t.Fatal("shared music lost", result, err)
	}
	if _, err := (persistence.Sources{DB: repo.DB}).Get(ctx, source.ID); err != nil {
		t.Fatal("shared source lost", err)
	}
	// A failed station delete rolls back the playlist removal.
	third, err := svc.Save(ctx, playlists.Playlist{StationID: stations[2].ID, Name: "Third"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(ctx, `CREATE TRIGGER reject_station_delete BEFORE DELETE ON stations BEGIN SELECT RAISE(ABORT,'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := stationRepo.Delete(ctx, stations[2].ID); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := repo.Get(ctx, third.ID); err != nil {
		t.Fatal("partial deletion", err)
	}
}
