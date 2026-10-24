package announcements

import (
	"testing"
	"time"
)

func TestEligibilityAndNoRepeatBacklog(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	m := Message{ID: "a", StationID: "s", Title: "News", Category: "news", Text: "Facts", Mode: "exact", Status: "active", Repeat: "once", Starts: now.Add(-time.Hour), Expires: now.Add(time.Hour)}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if !m.Eligible(now, time.UTC, "", "", time.Time{}, 10) {
		t.Fatal("one-off not eligible")
	}
	m.LastAired = now.Add(-time.Hour)
	if m.Eligible(now, time.UTC, "", "", time.Time{}, 10) {
		t.Fatal("replayed one-off")
	}
	m.Next = true
	if !m.Eligible(now, time.UTC, "", "", now, 10) {
		t.Fatal("explicit next should bypass gap and prior airtime")
	}
	m.ShowIDs = []string{"show"}
	m.EpisodeKey = "show:slot:2026-10-19"
	if m.Eligible(now, time.UTC, "other", m.EpisodeKey, time.Time{}, 10) || m.Eligible(now, time.UTC, "show", "other", time.Time{}, 10) {
		t.Fatal("target ignored")
	}
	m.Next = false
	m.ShowIDs = nil
	m.EpisodeKey = ""
	m.Repeat = "interval"
	m.IntervalMinutes = 90
	m.LastAired = now.Add(-2 * time.Hour)
	if !m.Eligible(now, time.UTC, "", "", time.Time{}, 10) {
		t.Fatal("interval not due")
	}
	m.LastAired = now
	if m.Eligible(now, time.UTC, "", "", time.Time{}, 10) {
		t.Fatal("missed interval accumulated")
	}
	m.Repeat = "times"
	m.Starts = now.AddDate(0, 0, -2)
	m.Times = []string{"08:00", "10:00", "11:00"}
	m.LastAired = now.Add(-24 * time.Hour)
	if !m.Eligible(now, time.UTC, "", "", time.Time{}, 10) {
		t.Fatal("daily slot not due")
	}
	m.LastAired = now
	if !m.Due(now, time.UTC).IsZero() {
		t.Fatal("missed daily slots accumulated")
	}
	m.Expires = now
	m.Next = true
	if m.Eligible(now, time.UTC, "", "", time.Time{}, 10) {
		t.Fatal("expired next played")
	}
}
func TestDailySlotsAcrossDSTAndValidity(t *testing.T) {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	m := Message{Status: "active", Repeat: "times", Times: []string{"01:30"}, Starts: time.Date(2026, 10, 24, 0, 0, 0, 0, loc), Expires: time.Date(2026, 10, 27, 0, 0, 0, 0, loc)}
	first := time.Date(2026, 10, 25, 0, 45, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	slot := time.Date(2026, 10, 25, 1, 30, 0, 0, loc)
	if !m.Due(second, loc).Equal(slot) {
		t.Fatal("wrong local DST slot")
	}
	m.LastAired = slot.Add(time.Minute)
	if !m.Due(second, loc).IsZero() {
		t.Fatal("repeated autumn hour replayed a slot")
	}
	m.Starts = second.Add(time.Hour)
	if m.Eligible(second, loc, "", "", time.Time{}, 10) {
		t.Fatal("future start ignored")
	}
}
func TestMessageValidation(t *testing.T) {
	m := Message{ID: "a", StationID: "s", Title: "Test", Text: "Facts", Category: "message", Mode: "exact", Status: "draft", Repeat: "times", Times: []string{"8:00"}, Starts: time.Now(), Expires: time.Now().Add(time.Hour)}
	if m.Validate() == nil {
		t.Fatal("accepted malformed time")
	}
	m.Times = []string{"08:00", "08:00"}
	if m.Validate() == nil {
		t.Fatal("accepted duplicate time")
	}
	m.Times = []string{"08:00"}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	m.Text = "bad\x00text"
	if m.Validate() == nil {
		t.Fatal("accepted control characters")
	}
}
