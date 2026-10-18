package upnp

import (
	"stationos.local/stationos/internal/content"
	"testing"
)

func TestSourceDateYear(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want int
	}{{"", 0}, {"1984", 1984}, {"1984-06", 1984}, {"1984-06-01", 1984}, {" 1984-06-01T12:00:00Z ", 1984}, {"1984-02-30", 0}, {"unknown", 0}, {"0000", 0}, {"1984junk", 0}, {"-1984", 0}, {"1984-13-01", 0}} {
		if got := sourceYear(c.raw); got != c.want {
			t.Errorf("%q: %d, want %d", c.raw, got, c.want)
		}
	}
}

func TestOriginalDatePreference(t *testing.T) {
	for _, tc := range []struct {
		name, date  string
		annotations []string
		year        int
		original    string
	}{
		{"compilation", "2008-10-06", []string{"tag.OriginalDate=1987"}, 1987, "1987"},
		{"fallback", "2008-10-06", nil, 2008, ""},
		{"invalid original", "2008-10-06", []string{"tag.OriginalDate=1987junk"}, 2008, ""},
		{"unrelated", "2008", []string{"tag.Date=1987"}, 2008, ""},
		{"original only", "", []string{"tag.OriginalDate=1987-08-04"}, 1987, "1987-08-04"},
		{"first valid", "2008", []string{"tag.OriginalDate=unknown", "tag.OriginalDate=1987", "tag.OriginalDate=1988"}, 1987, "1987"},
		{"unknown", "", []string{"tag.OriginalDate="}, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var item content.AudioItem
			(didlEntry{Date: tc.date, Annotations: tc.annotations}).applyDates(&item)
			if item.Year != tc.year || item.OriginalDate != tc.original || item.ReleaseDate != tc.date {
				t.Fatalf("unexpected dates: %+v", item)
			}
		})
	}
}
