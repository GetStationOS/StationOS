package schedule

import (
	"testing"
	"time"
)

func TestOverlapWrapAndAdjacency(t *testing.T) {
	a := Show{Day: 6, Start: 23 * 60, End: 60}
	for _, x := range []struct {
		b    Show
		want bool
	}{{Show{Day: 0, Start: 30, End: 90}, true}, {Show{Day: 0, Start: 60, End: 90}, false}, {Show{Day: 6, Start: 22 * 60, End: 23 * 60}, false}, {Show{Day: 6, Start: 22 * 60, End: 30}, true}} {
		if Overlaps(a, x.b) != x.want {
			t.Fatal(x)
		}
	}
}
func TestTimelineWeeklyFallbackAndBoundary(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/London")
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, loc)
	shows := []Show{{ID: "one", StationID: "s", Name: "Morning", PlaylistID: "p", Day: 0, Start: 540, End: 600, Revision: 1}, {ID: "two", StationID: "s", Name: "Next", PlaylistID: "q", Day: 0, Start: 600, End: 660, Revision: 1}}
	line := Resolve(shows, "s", "default", "Music", now, loc)
	if line.Current.ShowID != "one" || line.Next.ShowID != "two" || line.Current.End.Hour() != 10 {
		t.Fatal(line)
	}
	line = Resolve(shows, "s", "default", "Music", now.Add(time.Hour), loc)
	if line.Current.ShowID != "two" || line.Next.PlaylistID != "default" {
		t.Fatal(line)
	}
	line = Resolve(shows, "s", "default", "Music", now.Add(2*time.Hour), loc)
	if line.Current.PlaylistID != "default" || line.Next.ShowID != "one" || line.Next.Start.Day() != 19 {
		t.Fatal(line)
	}
}
func TestDSTMissingAndRepeatedBoundaries(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/London")
	spring := time.Date(2026, 3, 29, 0, 0, 0, 0, loc)
	if t1 := WallTime(spring, 90, false); t1.Hour() != 2 || t1.Minute() != 0 {
		t.Fatal("missing time", t1)
	}
	fold := time.Date(2026, 10, 25, 0, 0, 0, 0, loc)
	start := WallTime(fold, 90, false)
	end := WallTime(fold, 90, true)
	if !end.Equal(start) {
		t.Fatal("fold policy", start, end)
	}
	// A nonexistent slot collapses instead of starting after its end.
	shows := []Show{{ID: "spring", Day: 6, Start: 60, End: 90}}
	for _, o := range Occurrences(shows, spring, loc) {
		if o.Start.Day() == 29 && o.Start.Month() == 3 {
			t.Fatal("collapsed show retained", o)
		}
	}
}
