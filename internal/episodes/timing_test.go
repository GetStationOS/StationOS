package episodes

import (
	"testing"
	"time"
)

func TestReconcileEndRetainsHistoryAndOnlyFittingQueue(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name          string
		minutes, kept int
	}{{"shorten", 5, 1}, {"extend", 30, 2}, {"ended", -1, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			e := Episode{Start: now.Add(-time.Hour), End: now.Add(time.Hour), Notes: "Keep", Theme: "80s", Aired: []Track{{ID: "current", AiredAt: now.Add(-time.Minute)}}, QueueCurrent: Track{ID: "current", AiredAt: now.Add(-time.Minute), Seconds: 120}, AcceptedNextID: "second", Plan: []Track{{ID: "first", Seconds: 180, Approved: true}, {ID: "second", Seconds: 300, Approved: true}}, Version: 2}
			e.ReconcileEnd(now.Add(time.Duration(tc.minutes)*time.Minute), now)
			if len(e.Plan) != tc.kept || len(e.Aired) != 1 || e.Aired[0].ID != "current" || e.QueueCurrent.ID != "current" || e.Notes != "Keep" || e.Theme != "80s" || e.Version != 3 || e.EditorialRevision != 1 || e.TimingChangedAt.IsZero() {
				t.Fatal(e)
			}
			if tc.kept < 2 && e.AcceptedNextID != "" {
				t.Fatal("obsolete reservation", e)
			}
		})
	}
}
