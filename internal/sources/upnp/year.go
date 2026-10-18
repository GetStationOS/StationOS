package upnp

import (
	"strconv"
	"strings"
	"time"

	"stationos.local/stationos/internal/content"
)

// Accept a year or ISO date/time. Invalid and absent dates stay unknown.
func sourceYear(raw string) int {
	raw = strings.TrimSpace(raw)
	if len(raw) == 4 {
		y, err := strconv.Atoi(raw)
		if err == nil && y > 0 && y <= 9999 {
			return y
		}
		return 0
	}
	for _, layout := range []string{"2006-01", "2006-01-02", time.RFC3339} {
		if d, err := time.Parse(layout, raw); err == nil && d.Year() > 0 {
			return d.Year()
		}
	}
	return 0
}

// Keep both source dates so the effective year retains its provenance.
// Invalid originals must not hide a usable album release date.
func (v didlEntry) applyDates(a *content.AudioItem) {
	a.ReleaseDate = strings.TrimSpace(v.Date)
	a.Year = sourceYear(a.ReleaseDate)
	for _, annotation := range v.Annotations {
		tag, value, ok := strings.Cut(annotation, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(tag), "tag.OriginalDate") {
			continue
		}
		value = strings.TrimSpace(value)
		if year := sourceYear(value); year != 0 {
			a.OriginalDate = value
			a.Year = year
			break
		}
	}
}
