package playlists

import (
	"errors"
	"strings"
	"testing"
)

func TestRuleValidationExplainsCorrection(t *testing.T) {
	for _, tc := range []struct {
		rule Rule
		want string
	}{
		{Rule{Kind: "Genre", Value: "Jazz"}, "genre, album, artist, composer, year, decade or filter"},
		{Rule{Kind: "genre", Value: "Jazz", SourceID: "server"}, "genre rules must omit source_id"},
		{Rule{Kind: "album", Value: "Record"}, "album rules require source_id"},
		{Rule{Kind: "genre"}, "rule.value"},
	} {
		err := tc.rule.Validate()
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("missing actionable correction for %+v: %v", tc.rule, err)
		}
	}
	for _, rule := range []Rule{{Kind: "genre", Value: "Jazz"}, {Kind: "album", Value: "Record", SourceID: "server"}} {
		if err := rule.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
