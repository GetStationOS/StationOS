package persistence

import (
	"context"
	"fmt"
	"path/filepath"
	"stationos.local/stationos/internal/operations"
	"testing"
	"time"
)

func TestTransmissionHistoryRestartSnapshotsAndFilters(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	o := Operations{DB: db}
	at := time.Date(2026, 10, 25, 1, 15, 0, 0, time.UTC)
	e := operations.Event{ID: "music", StationID: "s", Started: at, Kind: "music", Outcome: "transmitting", Title: "Original title", Playlist: "Original pool", Artist: "Artist", Composer: "Composer", ShowID: "show", DJID: "dj", AudioBytes: 4096}
	if err = o.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	e.AudioBytes = 240000
	if err = o.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	complete := e
	complete.ID = "speech"
	complete.Kind = "speech"
	complete.Speech = "A lovely recording"
	complete.Started = at.Add(time.Minute)
	complete.Ended = at.Add(2 * time.Minute)
	complete.Outcome = "completed"
	if err = o.Save(ctx, complete); err != nil {
		t.Fatal(err)
	}
	foreign := e
	foreign.ID = "other"
	foreign.StationID = "other"
	foreign.Outcome = "completed"
	if err = o.Save(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	o.DB = db
	// Opening storage is not recovery: CLI reads must not alter live records.
	rows, err := o.Events(ctx, operations.Filter{StationID: "s", Kind: "music"})
	if err != nil || len(rows) != 1 || rows[0].Outcome != "transmitting" {
		t.Fatalf("before recovery: %+v %v", rows, err)
	}
	if err = o.Recover(ctx, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err = o.Events(ctx, operations.Filter{StationID: "s", Query: "original title", Kind: "music", Outcome: "unknown", ShowID: "show", DJID: "dj"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("filters: %+v %v", rows, err)
	}
	if !rows[0].Ended.IsZero() || rows[0].AudioBytes != 240000 || rows[0].Composer != "Composer" || rows[0].Playlist != "Original pool" {
		t.Fatalf("invented end or lost snapshot: %+v", rows[0])
	}
	rows, err = o.Events(ctx, operations.Filter{StationID: "s", Query: "lovely"})
	if err != nil || len(rows) != 1 || rows[0].Outcome != "completed" {
		t.Fatalf("speech: %+v %v", rows, err)
	}
	rows, err = o.Events(ctx, operations.Filter{StationID: "s", Before: "speech"})
	if err != nil || len(rows) != 1 || rows[0].ID != "music" {
		t.Fatalf("cursor: %+v %v", rows, err)
	}
	rows, err = o.Events(ctx, operations.Filter{StationID: "other", Before: "speech"})
	if err != nil || len(rows) != 0 {
		t.Fatalf("foreign cursor: %+v %v", rows, err)
	}
	rows, err = o.Events(ctx, operations.Filter{StationID: "s", From: at.Add(time.Second), Until: at.Add(2 * time.Minute)})
	if err != nil || len(rows) != 1 {
		t.Fatalf("dates: %+v %v", rows, err)
	}
}
func TestTransmissionIncidentsGroupAcknowledgeRecoverAndRecur(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	o := Operations{DB: db}
	at := time.Now()
	e := operations.Event{ID: "one", StationID: "s", Started: at, Kind: "ai", Outcome: "observed", IncidentCode: "speech", Reason: "Speech failed", Severity: "warning"}
	for _, id := range []string{"one", "two"} {
		e.ID = id
		e.Started = e.Started.Add(time.Second)
		if err = o.Save(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	i, err := o.Incidents(ctx, "s", true)
	if err != nil || len(i) != 1 || i[0].Count != 2 {
		t.Fatalf("grouping %+v %v", i, err)
	}
	if err = o.Acknowledge(ctx, "other", i[0].ID, at); err == nil {
		t.Fatal("cross-station acknowledgement")
	}
	if err = o.Acknowledge(ctx, "s", i[0].ID, at); err != nil {
		t.Fatal(err)
	}
	i, _ = o.Incidents(ctx, "s", true)
	if len(i) != 1 || i[0].Acknowledged.IsZero() {
		t.Fatal("acknowledgement resolved incident")
	}
	e.ID = "recovery"
	e.Kind = "recovery"
	e.Recover = true
	if err = o.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	i, _ = o.Incidents(ctx, "s", true)
	if len(i) != 0 {
		t.Fatal("not recovered")
	}
	e.ID = "no-op"
	if err = o.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	rows, _ := o.Events(ctx, operations.Filter{StationID: "s", Query: "no-op"})
	if len(rows) != 0 {
		t.Fatal("spurious recovery")
	}
	e.ID = "again"
	e.Recover = false
	if err = o.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	i, _ = o.Incidents(ctx, "s", true)
	if len(i) != 1 || i[0].Count != 1 || !i[0].Acknowledged.IsZero() {
		t.Fatalf("recurrence %+v", i)
	}
}

func TestHistoryPaginationCountsClampsAndKeepsSnapshot(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := Operations{DB: db}
	at := time.Now()
	for n := 0; n < 60; n++ {
		e := operations.Event{ID: fmt.Sprintf("event-%02d", n), StationID: "s", Started: at, Kind: "music", Outcome: "completed", Show: "Breakfast", ShowID: fmt.Sprintf("day-%d", n%7)}
		if err = store.Save(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	f := operations.Filter{StationID: "s", Kind: "music", ShowName: "Breakfast"}
	page, err := store.History(ctx, f, 2, 25)
	if err != nil || page.Total != 60 || page.Number != 2 || len(page.Events) != 25 || page.Anchor != "event-59" || page.Events[0].ID != "event-34" || page.Events[24].ID != "event-10" {
		t.Fatalf("page: %+v %v", page, err)
	}
	f.Anchor = page.Anchor
	if err = store.Save(ctx, operations.Event{ID: "new", StationID: "s", Started: at.Add(time.Minute), Kind: "music", Outcome: "completed", Show: "Breakfast"}); err != nil {
		t.Fatal(err)
	}
	stable, err := store.History(ctx, f, 2, 25)
	if err != nil || stable.Total != 60 || stable.Events[0].ID != page.Events[0].ID {
		t.Fatalf("new broadcast shifted older page: %+v %v", stable, err)
	}
	last, err := store.History(ctx, f, 999, 25)
	if err != nil || last.Number != 3 || len(last.Events) != 10 {
		t.Fatalf("clamp: %+v %v", last, err)
	}
	larger, err := store.History(ctx, f, 1, 50)
	if err != nil || len(larger.Events) != 50 || larger.Total != 60 {
		t.Fatalf("size: %+v %v", larger, err)
	}
	f.StationID = "other"
	foreign, err := store.History(ctx, f, 1, 25)
	if err != nil || foreign.Total != 0 || len(foreign.Events) != 0 {
		t.Fatal("foreign anchor leaked data", foreign, err)
	}
	if _, err = store.History(ctx, f, 0, 25); err == nil {
		t.Fatal("invalid page accepted")
	}
	if _, err = store.History(ctx, f, 1, 500); err == nil {
		t.Fatal("unbounded page accepted")
	}
}
