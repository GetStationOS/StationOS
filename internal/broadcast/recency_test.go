package broadcast

import (
	"context"
	"fmt"
	"reflect"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/schedule"
	"testing"
	"time"
)

func TestEpisodeEligibilityRanksNeverPlayedThenOldestOnThisStation(t *testing.T) {
	now := time.Date(2026, 10, 22, 16, 0, 0, 0, time.UTC)
	history := &memoryHistory{played: map[string]map[string]time.Time{
		"station":       {"recent": now.Add(-time.Minute), "newer": now.Add(-4 * time.Hour), "oldest": now.Add(-48 * time.Hour)},
		"other-station": {"never": now},
	}}
	m, _, repo := fixture(t, Options{History: history})
	repo.pool = []content.AudioItem{length("newer", 60), length("recent", 60), length("never", 60), length("oldest", 60)}
	got, err := m.EligibleTracks(context.Background(), schedule.Occurrence{StationID: "station", PlaylistID: "pool", Key: "episode"}, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, a := range got {
		ids = append(ids, a.ID)
	}
	if !reflect.DeepEqual(ids, []string{"never", "oldest", "newer"}) {
		t.Fatal("wrong station-wide recency order", ids)
	}
}

func TestRecencyTiesVaryWithEpisodeProgressWithoutOverridingAge(t *testing.T) {
	now := time.Now()
	history := Rotation{LastPlayed: map[string]time.Time{"old": now.Add(-time.Hour), "new": now}}
	pool := []content.AudioItem{length("new", 60), length("old", 60)}
	for i := 0; i < 20; i++ {
		pool = append(pool, length(fmt.Sprintf("never-%02d", i), 60))
	}
	signature := func(seed string) []string {
		candidates := append([]content.AudioItem(nil), pool...)
		rankByLastPlayed(candidates, history, seed)
		ids := []string{}
		for _, a := range candidates {
			ids = append(ids, a.ID)
		}
		if ids[20] != "old" || ids[21] != "new" {
			t.Fatal("tie-break overrode recency", ids)
		}
		return ids[:10]
	}
	first := signature("episode:0")
	if !reflect.DeepEqual(first, signature("episode:0")) {
		t.Fatal("retries reshuffled the same choice")
	}
	varied := false
	for i := 1; i <= 32; i++ {
		if !reflect.DeepEqual(first, signature(fmt.Sprintf("episode:%d", i))) {
			varied = true
			break
		}
	}
	if !varied {
		t.Fatal("equal timestamps always produce the same shortlist")
	}
}
