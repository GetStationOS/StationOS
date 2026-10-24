package episodes

import (
	"fmt"
	"time"
)

// ReconcileEnd changes the unaired remainder, never the start or airtime receipts.
// Only the schedule adapter calls this for an episode active before the edit.
func (e *Episode) ReconcileEnd(end, now time.Time) {
	e.End = end
	e.TimingChangedAt = now
	cursor := now
	if e.PlanStartsAt.After(cursor) {
		cursor = e.PlanStartsAt
	}
	if !e.QueueCurrent.AiredAt.IsZero() {
		until := e.QueueCurrent.AiredAt.Add(time.Duration(e.QueueCurrent.Seconds) * time.Second)
		if until.After(cursor) {
			cursor = until
		}
	}
	played := map[string]bool{}
	for _, track := range e.Aired {
		played[track.ID] = true
		if !track.AiredAt.IsZero() {
			until := track.AiredAt.Add(time.Duration(track.Seconds) * time.Second)
			if until.After(cursor) {
				cursor = until
			}
		}
	}
	kept := []Track{}
	accepted := false
	fits := true
	for _, track := range e.Plan {
		if played[track.ID] || !track.AiredAt.IsZero() {
			kept = append(kept, track)
			continue
		}
		until := cursor.Add(time.Duration(track.Seconds+15) * time.Second)
		if !fits || track.Seconds <= 0 || until.After(end) {
			fits = false
			continue
		}
		kept = append(kept, track)
		cursor = until
		if track.ID == e.AcceptedNextID {
			accepted = true
		}
	}
	e.Plan = kept
	if !accepted {
		e.AcceptedNextID = ""
	}
	e.EditorialRevision++
	e.Version++
	e.PlanningFailed = false
	e.FailureCode = ""
	e.RetryAt = time.Time{}
	e.Status = "attention"
	e.Problem = "Show timing changed; updating the remaining running order."
	if !now.Before(end) {
		e.Status = "finished"
		e.Problem = ""
	}
	e.Note("timing changed", fmt.Sprintf("End changed to %s; original start and aired history retained.", end.Format(time.RFC3339)))
}
