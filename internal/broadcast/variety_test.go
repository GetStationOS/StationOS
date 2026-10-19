package broadcast

import (
	"stationos.local/stationos/internal/content"
	"testing"
	"time"
)

func TestVarietyAppliesBeforeFallbackAndRelaxesSmallPools(t *testing.T) {
	current := length("current", 240)
	current.Artist = "Dire Straits"
	current.Album = "Money for Nothing"
	same := length("same", 240)
	same.Artist = " dire  straits "
	same.Album = current.Album
	compilation := length("compilation", 240)
	compilation.Artist = "Other"
	compilation.Album = current.Album
	different := length("different", 240)
	different.Artist = "Kate Bush"
	different.Album = "Hounds of Love"
	pool := []content.AudioItem{current, same, compilation, different}
	h := Rotation{LastPlayed: map[string]time.Time{current.ID: time.Now()}}
	got := varietyPool(pool[1:], pool, h, current.ID, current)
	if len(got) != 1 || got[0].ID != different.ID {
		t.Fatal(got)
	}
	if selected := selectTrack(got, current.ID, 4*time.Minute); selected.ID != different.ID {
		t.Fatal(selected)
	}
	got = varietyPool(pool[1:2], pool, h, current.ID, current)
	if len(got) != 1 {
		t.Fatal("small pool stopped", got)
	}
	got = varietyPoolFormat(pool[1:2], pool, h, current.ID, current, "album")
	if len(got) != 1 {
		t.Fatal("album special blocked", got)
	}
}
func TestVarietyMatchesSharedCreditsWithoutMatchingComposers(t *testing.T) {
	a := content.AudioItem{Artist: "First; Second, Name", Composer: "Writer"}
	b := content.AudioItem{Artist: " second, name "}
	if !samePerformerOrAlbum(a, b) {
		t.Fatal("shared performer missed")
	}
	b.Artist = "Writer"
	if samePerformerOrAlbum(a, b) {
		t.Fatal("composer treated as performer")
	}
}
