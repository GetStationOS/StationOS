package persistence

import (
	"context"
	"errors"
	"stationos.local/stationos/internal/announcements"
	"stationos.local/stationos/internal/changefeed"
	"testing"
	"time"
)

func TestAnnouncementVersionsReceiptsAndNotifications(t *testing.T) {
	db := database(t)
	seed(t, db)
	ctx := context.Background()
	bus := &changefeed.Bus{}
	r := Announcements{db, bus}
	events, stop := bus.Watch("announcements")
	defer stop()
	now := time.Now().Truncate(time.Second)
	m := announcements.Message{ID: "message", StationID: "station", Title: "News", Category: "news", Text: "Facts", Mode: "exact", Status: "draft", Repeat: "once", Starts: now, Expires: now.Add(time.Hour)}
	if err := r.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
	default:
		t.Fatal("no committed change")
	}
	saved, err := r.Get(ctx, "station", m.ID)
	if err != nil || saved.Version != 1 || saved.Plays != 0 {
		t.Fatalf("saved: %+v %v", saved, err)
	}
	if _, err = r.Get(ctx, "other", m.ID); !errors.Is(err, announcements.ErrNotFound) {
		t.Fatal("cross-station read", err)
	}
	saved.Text = "Changed"
	saved.Status = "active"
	saved.Next = true
	if err = r.Save(ctx, saved); err != nil {
		t.Fatal(err)
	}
	<-events
	if err = r.Save(ctx, saved); !errors.Is(err, announcements.ErrConflict) {
		t.Fatal("stale edit accepted", err)
	}
	select {
	case <-events:
		t.Fatal("failed write published")
	default:
	}
	receipt := announcements.Receipt{ID: "receipt", StationID: "station", AnnouncementID: m.ID, Version: 2, Title: "News", Text: "Changed", DJID: "dj", DJName: "Presenter", Started: now, Outcome: "transmitting"}
	if err = r.Record(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Outcome = "interrupted"
	receipt.Ended = now.Add(time.Second)
	if err = r.Record(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	saved, err = r.Get(ctx, "station", m.ID)
	if err != nil || saved.Plays != 1 || saved.Next || saved.LastOutcome != "interrupted" || !saved.LastAired.Equal(now) {
		t.Fatalf("receipt not reflected: %+v %v", saved, err)
	}
	if err = r.Delete(ctx, "other", m.ID, 2); !errors.Is(err, announcements.ErrConflict) {
		t.Fatal("cross-station delete", err)
	}
	if err = r.Delete(ctx, "station", m.ID, 2); err != nil {
		t.Fatal(err)
	}
	history, err := r.History(ctx, "station", m.ID)
	if err != nil || len(history) != 1 || history[0].Text != "Changed" {
		t.Fatal("lost immutable history", history, err)
	}
}
func TestAnnouncementSettingsAndLimits(t *testing.T) {
	db := database(t)
	seed(t, db)
	r := Announcements{DB: db}
	ctx := context.Background()
	s, err := r.Settings(ctx, "station")
	if err != nil || s.GapMinutes != 10 {
		t.Fatal(s, err)
	}
	s.GapMinutes = 15
	if err = r.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err = r.SaveSettings(ctx, s); !errors.Is(err, announcements.ErrConflict) {
		t.Fatal("stale settings accepted", err)
	}
	s.Version = 1
	s.GapMinutes = 0
	if err = r.SaveSettings(ctx, s); !errors.Is(err, announcements.ErrInvalid) {
		t.Fatal("invalid gap accepted", err)
	}
}

func TestAnnouncementRestartPreservesPartialAirtime(t *testing.T) {
	db := database(t)
	seed(t, db)
	r := Announcements{DB: db}
	ctx := context.Background()
	at := time.Now().Truncate(time.Second)
	m := announcements.Message{ID: "a", StationID: "station", Title: "Notice", Text: "Facts", Mode: "exact", Category: "message", Status: "active", Repeat: "once", Starts: at.Add(-time.Hour), Expires: at.Add(time.Hour)}
	if err := r.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(ctx, announcements.Receipt{ID: "air", AnnouncementID: m.ID, StationID: "station", Version: 1, Started: at, Text: "Facts", Outcome: "transmitting"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	m, err := r.Get(ctx, "station", "a")
	if err != nil || m.LastOutcome != "interrupted" || m.Plays != 1 || m.State(at) != "Aired" {
		t.Fatal(m, err)
	}
	if m.Eligible(at.Add(time.Minute), time.UTC, "", "", time.Time{}, 10) {
		t.Fatal("partial one-off automatically replayed after restart")
	}
}
