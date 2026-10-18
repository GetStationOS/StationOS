package playlists

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Filter requires every supplied condition. Separate rules remain a union.
// Years describe source-reported dates, never an inferred original release.
type Filter struct {
	Genre    string `json:"genre,omitempty"`
	Artist   string `json:"artist,omitempty"`
	Composer string `json:"composer,omitempty"`
	Album    string `json:"album,omitempty"`
	SourceID string `json:"source_id,omitempty"`
	YearFrom int    `json:"year_from,omitempty"`
	YearTo   int    `json:"year_to,omitempty"`
}

func ParseFilter(raw string) (Filter, error) {
	var f Filter
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		return f, fmt.Errorf("%w: invalid combined filter: %v", ErrInvalid, err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return f, fmt.Errorf("%w: supply one filter object", ErrInvalid)
	}
	return f, f.Validate()
}
func (f Filter) Validate() error {
	invalid := func(s string) error { return fmt.Errorf("%w: %s", ErrInvalid, s) }
	if f == (Filter{}) {
		return invalid("choose at least one filter condition")
	}
	for _, v := range []string{f.Genre, f.Artist, f.Composer, f.Album, f.SourceID} {
		if len(v) > 4096 || (v != "" && strings.TrimSpace(v) == "") {
			return invalid("invalid filter value")
		}
	}
	if len(f.SourceID) > 128 || (f.Album != "" && f.SourceID == "") {
		return invalid("album conditions require a source_id")
	}
	if f.YearFrom < 0 || f.YearFrom > 9999 || f.YearTo < 0 || f.YearTo > 9999 || (f.YearFrom > 0 && f.YearTo > 0 && f.YearFrom > f.YearTo) {
		return invalid("choose a valid year range from 1–9999")
	}
	return nil
}
func (r Rule) Label() string {
	if r.Kind != "filter" {
		if r.Kind == "decade" {
			return r.Value + "s"
		}
		return r.Value
	}
	f, err := ParseFilter(r.Value)
	if err != nil {
		return "Invalid combined rule"
	}
	parts := []string{}
	for _, p := range []struct{ k, v string }{{"Genre", f.Genre}, {"Artist", f.Artist}, {"Composer", f.Composer}, {"Album", f.Album}, {"Source", f.SourceID}} {
		if p.v != "" {
			parts = append(parts, p.k+": "+p.v)
		}
	}
	if f.YearFrom > 0 {
		parts = append(parts, fmt.Sprintf("From %d", f.YearFrom))
	}
	if f.YearTo > 0 {
		parts = append(parts, fmt.Sprintf("To %d", f.YearTo))
	}
	return strings.Join(parts, " · ")
}
