package upnp

import (
	"strconv"
	"strings"
	"time"
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
