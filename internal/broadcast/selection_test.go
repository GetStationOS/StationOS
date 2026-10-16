package broadcast

import (
	"stationos.local/stationos/internal/content"
	"testing"
	"time"
)

func length(id string, seconds int) content.AudioItem {
	d := time.Duration(seconds) * time.Second
	return content.AudioItem{ID: id, Title: id, Duration: &d}
}
func TestFallbackDurationPlanning(t *testing.T) {
	pool := []content.AudioItem{length("long", 400), length("short", 180), length("other", 180), {ID: "unknown"}}
	if a := selectTrack(pool, "", 6*time.Minute); a.ID != "short" && a.ID != "other" {
		t.Fatal("two short tracks should fit exact slot", a)
	}
	if a := selectTrack(pool, "short", 3*time.Minute); a.ID != "other" {
		t.Fatal("immediate repeat", a)
	}
	if a := selectTrack([]content.AudioItem{{ID: "unknown"}}, "", time.Minute); a.ID != "unknown" {
		t.Fatal("unknown duration treated as empty", a)
	}
}
