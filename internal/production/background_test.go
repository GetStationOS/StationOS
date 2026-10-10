package production

import (
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/schedule"
	"testing"
)

func TestBackgroundInvalidatesSpeechButPortraitDoesNot(t *testing.T) {
	show := schedule.Occurrence{Key: "show", DJ: djs.Profile{ID: "dj", Background: "Original"}}
	next := schedule.Occurrence{}
	original := CacheKey(show, next)
	show.DJ.PortraitURL = "https://example.com/photo.jpg"
	if CacheKey(show, next) != original {
		t.Fatal("portrait invalidated speech")
	}
	show.DJ.Background = "Changed"
	if CacheKey(show, next) == original {
		t.Fatal("changed character reused stale speech")
	}
}
