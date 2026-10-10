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
func TestCuratorPreferenceAndDurationPlanning(t *testing.T) {
	pool := []content.AudioItem{length("long", 400), length("short", 180), length("other", 180), {ID: "unknown"}}
	if a := selectTrack(pool, "", []string{"short", "long"}, time.Hour); a.ID != "short" {
		t.Fatal("curation ignored", a)
	}
	if a := selectTrack(pool, "", []string{"long", "short", "other"}, 6*time.Minute); a.ID != "short" && a.ID != "other" {
		t.Fatal("two short tracks should fit exact slot", a)
	}
	if a := selectTrack(pool, "short", []string{"short", "other"}, 3*time.Minute); a.ID != "other" {
		t.Fatal("immediate repeat", a)
	}
	if a := selectTrack([]content.AudioItem{{ID: "unknown"}}, "", nil, time.Minute); a.ID != "unknown" {
		t.Fatal("unknown duration treated as empty", a)
	}
}
