package producer

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stationos.local/stationos/internal/catalogue"
)

type ruleFacetFixture struct {
	catalogue.Repository
	facets catalogue.Facets
	err    error
}

func (f ruleFacetFixture) Facets(context.Context, ...string) (catalogue.Facets, error) {
	return f.facets, f.err
}

func TestCatalogueSpellingResolutionPreservesExactAmbiguousAndUnknownRules(t *testing.T) {
	repo := ruleFacetFixture{facets: catalogue.Facets{Genres: []string{"Jazz", "Rock", "rock"}, Artists: []string{"Example Artist"}, Composers: []string{"Example Composer"}}}
	input := []rule{{Kind: "genre", Value: "jazz"}, {Kind: "genre", Value: "Rock"}, {Kind: "genre", Value: "ROCK"}, {Kind: "genre", Value: "Unknown future genre"}, {Kind: "artist", Value: "example artist"}, {Kind: "composer", Value: "example composer"}, {Kind: "album", Value: "jazz", SourceID: "specific-source"}}
	original := append([]rule(nil), input...)
	got, changed := catalogueRuleValues(context.Background(), repo, input)
	want := append([]rule(nil), input...)
	want[0].Value = "Jazz"
	want[4].Value = "Example Artist"
	want[5].Value = "Example Composer"
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(input, original) || len(changed) != 3 {
		t.Fatal("resolution altered exact, ambiguous, unknown or source-scoped rules", got, changed)
	}
	again, normalized := catalogueRuleValues(context.Background(), repo, got)
	if !reflect.DeepEqual(again, want) || len(normalized) != 0 {
		t.Fatal("resolution was not stable", again, normalized)
	}
	for _, failed := range []catalogue.Repository{nil, ruleFacetFixture{err: errors.New("catalogue unavailable")}} {
		unchanged, changes := catalogueRuleValues(context.Background(), failed, input)
		if !reflect.DeepEqual(unchanged, input) || len(changes) != 0 {
			t.Fatal("unavailable catalogue guessed a spelling", unchanged, changes)
		}
	}
}
