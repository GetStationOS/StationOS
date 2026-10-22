package broadcast

import (
	"fmt"
	"stationos.local/stationos/internal/content"
	"testing"
	"time"
)

func TestNewAlbumCannotDominateBalancedShortlist(t *testing.T) {
	pool := []content.AudioItem{}
	history := Rotation{LastPlayed: map[string]time.Time{}}
	for i := 0; i < 20; i++ {
		a := length(fmt.Sprintf("new-%02d", i), 60)
		a.Album = "New album"
		a.Artist = "New artist"
		pool = append(pool, a)
	}
	now := time.Now()
	for i := 0; i < 15; i++ {
		a := length(fmt.Sprintf("old-%02d", i), 60)
		a.Album = fmt.Sprintf("Album %d", i)
		a.Artist = fmt.Sprintf("Artist %d", i)
		pool = append(pool, a)
		history.LastPlayed[a.ID] = now.Add(time.Duration(i-30) * time.Hour)
	}
	got := balancedShortlist(pool, history, "episode")
	if len(got) != 10 {
		t.Fatal(len(got))
	}
	newCount := 0
	seen := map[string]bool{}
	for _, a := range got {
		if history.LastPlayed[a.ID].IsZero() {
			newCount++
		}
		if seen[a.Album] {
			t.Fatal("repeated album despite alternatives", a.Album)
		}
		seen[a.Album] = true
	}
	if newCount != 1 {
		t.Fatal("new album monopolised shortlist", newCount)
	}
	for i := 0; i < 9; i++ {
		if got[i+1].ID != fmt.Sprintf("old-%02d", i) {
			t.Fatal("known music lost recency ordering", got)
		}
	}
}

func TestBalancedShortlistCapsNewMusicAndRelaxesOnlySmallPools(t *testing.T) {
	for _, allNew := range []bool{false, true} {
		t.Run(fmt.Sprint(allNew), func(t *testing.T) {
			pool := []content.AudioItem{}
			history := Rotation{LastPlayed: map[string]time.Time{}}
			for i := 0; i < 20; i++ {
				a := length(fmt.Sprintf("track-%02d", i), 60)
				a.Album = "Only album"
				a.Artist = "Only artist"
				pool = append(pool, a)
				if !allNew && i >= 5 {
					history.LastPlayed[a.ID] = time.Now().Add(time.Duration(i-30) * time.Hour)
				}
			}
			got := balancedShortlist(pool, history, "episode")
			want := 10
			if allNew {
				want = 3
			}
			if len(got) != want {
				t.Fatal(len(got), want)
			}
			n := 0
			seen := map[string]bool{}
			for _, a := range got {
				if seen[a.ID] {
					t.Fatal("duplicate", a.ID)
				}
				seen[a.ID] = true
				if history.LastPlayed[a.ID].IsZero() {
					n++
				}
			}
			if n != 3 {
				t.Fatal("incorrect new-track allowance", n)
			}
		})
	}
}

func TestBalancedShortlistSpreadsNewAlbumsAndPerformers(t *testing.T) {
	pool := []content.AudioItem{}
	history := Rotation{LastPlayed: map[string]time.Time{}}
	for i := 0; i < 16; i++ {
		a := length(fmt.Sprintf("track-%02d", i), 60)
		a.Album = fmt.Sprintf("Album %d", i)
		a.Artist = fmt.Sprintf("Artist %d", i)
		pool = append(pool, a)
		if i >= 6 {
			history.LastPlayed[a.ID] = time.Now().Add(time.Duration(i-30) * time.Hour)
		}
	}
	got := balancedShortlist(pool, history, "episode")
	if len(got) != 10 {
		t.Fatal(len(got))
	}
	n := 0
	for _, a := range got {
		if history.LastPlayed[a.ID].IsZero() {
			n++
		}
	}
	if n != 3 {
		t.Fatal(n)
	}
}
