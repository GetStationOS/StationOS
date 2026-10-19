package upnp

import (
	"encoding/xml"
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
			(didlEntry{Dates: []didlDate{{XMLName: xml.Name{Space: "http://purl.org/dc/elements/1.1/", Local: "date"}, Value: tc.date}}, Annotations: tc.annotations}).applyDates(&item)
			if item.Year != tc.year || item.OriginalDate != tc.original || item.ReleaseDate != tc.date {
				t.Fatalf("unexpected dates: %+v", item)
			}
		})
	}
}

func TestServerIndependentDateMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, metadata, release, original string
		year                              int
	}{
		{"MinimServer annotation", `<dc:date>2008-10-06</dc:date><upnp:userAnnotation>tag.OriginalDate=1987</upnp:userAnnotation>`, "2008-10-06", "1987", 1987},
		{"Gerbera original date", `<dc:date>2015-05-04</dc:date><upnp:date>2015</upnp:date><upnp:originalDate>1983</upnp:originalDate>`, "2015-05-04", "1983", 1983},
		{"reversed date order", `<upnp:date>2015</upnp:date><dc:date>2015-05-04</dc:date><upnp:originalDate>1983</upnp:originalDate>`, "2015-05-04", "1983", 1983},
		{"different prefixes", `<d:date xmlns:d="http://purl.org/dc/elements/1.1/">2015-05-04</d:date><m:originalDate xmlns:m="urn:schemas-upnp-org:metadata-1-0/upnp/">1983</m:originalDate>`, "2015-05-04", "1983", 1983},
		{"generic release only", `<dc:date>1999-06-12</dc:date>`, "1999-06-12", "", 1999},
		{"unqualified legacy date", `<date>2001-03-04</date>`, "2001-03-04", "", 2001},
		{"UPnP year only", `<upnp:date>2002</upnp:date>`, "2002", "", 2002},
		{"invalid original falls back", `<dc:date>2015-05-04</dc:date><upnp:originalDate>1983junk</upnp:originalDate>`, "2015-05-04", "", 2015},
		{"first valid original", `<upnp:originalDate>unknown</upnp:originalDate><upnp:originalDate>1983</upnp:originalDate>`, "", "1983", 1983},
		{"valid annotation retains precedence", `<dc:date>2015</dc:date><upnp:userAnnotation>tag.OriginalDate=1983</upnp:userAnnotation><upnp:originalDate>1984</upnp:originalDate>`, "2015", "1983", 1983},
		{"invalid annotation allows direct original", `<upnp:userAnnotation>tag.OriginalDate=unknown</upnp:userAnnotation><upnp:originalDate>1983</upnp:originalDate>`, "", "1983", 1983},
		{"unrelated namespace ignored", `<dc:date>2015-05-04</dc:date><x:date xmlns:x="urn:other">1980</x:date><x:originalDate xmlns:x="urn:other">1983</x:originalDate>`, "2015-05-04", "", 2015},
		{"missing dates stay unknown", ``, "", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `<DIDL-Lite xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"><item id="track"><dc:title>Track</dc:title><upnp:class>object.item.audioItem.musicTrack</upnp:class>` + tc.metadata + `<res protocolInfo="http-get:*:audio/flac:*">http://example.com/track.flac</res></item></DIDL-Lite>`
			var response struct {
				Items []didlEntry `xml:"item"`
			}
			if err := xml.Unmarshal([]byte(raw), &response); err != nil || len(response.Items) != 1 {
				t.Fatal(response, err)
			}
			var a content.AudioItem
			response.Items[0].applyDates(&a)
			if a.ReleaseDate != tc.release || a.OriginalDate != tc.original || a.Year != tc.year {
				t.Fatalf("got release=%q original=%q year=%d; want %q %q %d", a.ReleaseDate, a.OriginalDate, a.Year, tc.release, tc.original, tc.year)
			}
		})
	}
}
