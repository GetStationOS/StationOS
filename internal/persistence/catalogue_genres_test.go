package persistence

import (
	"context"
	"reflect"
	"testing"
	"time"

	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
)

func TestGenreMembershipAndAuthoritativeRefresh(t *testing.T) {
	db := database(t)
	source := seed(t, db)
	ctx := context.Background()
	repo := Catalogue{DB: db}
	a := item(source, "one", "One")
	duration := time.Minute
	a.Duration = &duration
	a.Genre = " Ballad;Classic Rock;Disco;Downtempo;Electro, Disco; ,;\t "
	b := item(source, "two", "Two")
	b.Genre = " ,;\n,; "
	if err := repo.Replace(ctx, source, []content.AudioItem{a, b}); err != nil {
		t.Fatal(err)
	}
	facets, err := repo.Facets(ctx)
	want := []string{"Ballad", "Classic Rock", "Disco", "Downtempo", "Electro"}
	if err != nil || !reflect.DeepEqual(facets.Genres, want) {
		t.Fatal(facets, err)
	}
	for _, g := range want {
		found, err := repo.Search(ctx, catalogue.Query{Genre: g, Limit: 25})
		if err != nil || found.Total != 1 || found.Items[0].ID != a.ID {
			t.Fatal(g, found, err)
		}
	}
	found, err := repo.Search(ctx, catalogue.Query{Genre: "Classic", Limit: 25})
	if err != nil || found.Total != 0 {
		t.Fatal("partial match", found, err)
	}
	summary, err := repo.Summary(ctx, catalogue.SummaryQuery{Limit: 20})
	if err != nil || summary.Tracks != 2 || summary.Genres != 5 || summary.UnknownGenres != 1 || summary.KnownSeconds != 60 || len(summary.TopGenres) != 5 {
		t.Fatal(summary, err)
	}
	for _, g := range summary.TopGenres {
		if g.Tracks != 1 {
			t.Fatal("duplicate coverage", g)
		}
	}
	summary, err = repo.Summary(ctx, catalogue.SummaryQuery{Genre: "Disco", Limit: 20})
	if err != nil || summary.Tracks != 1 || summary.Genres != 5 || summary.KnownSeconds != 60 {
		t.Fatal(summary, err)
	}
	a.Genre = "Rock"
	if err = repo.Replace(ctx, source, []content.AudioItem{a}); err != nil {
		t.Fatal(err)
	}
	facets, err = repo.Facets(ctx)
	if err != nil || !reflect.DeepEqual(facets.Genres, []string{"Rock"}) {
		t.Fatal("refresh retained old genres", facets, err)
	}
	// Metadata updates and catalogue clearing also maintain the derived index.
	if _, err = db.Exec(`UPDATE catalogue_items SET metadata=json_set(metadata,'$.genre','Jazz; Blues') WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	facets, err = repo.Facets(ctx)
	if err != nil || !reflect.DeepEqual(facets.Genres, []string{"Blues", "Jazz"}) {
		t.Fatal(facets, err)
	}
	if err = repo.Clear(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	facets, err = repo.Facets(ctx)
	if err != nil || len(facets.Genres) != 0 {
		t.Fatal(facets, err)
	}
}
