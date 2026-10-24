// Package announcements owns producer messages and their eligibility, independently of playout.
package announcements

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid announcement")
var ErrNotFound = errors.New("announcement not found")
var ErrConflict = errors.New("this announcement changed; reload before saving")

const MaxItems = 200

type Message struct {
	ID, StationID, Title, Category, Text, Pronunciation string
	Mode, Status, Repeat, DJID                          string
	ShowIDs                                             []string
	EpisodeKey                                          string
	Starts, Expires                                     time.Time
	IntervalMinutes                                     int
	Times                                               []string
	Version                                             int
	Next                                                bool
	LastAired                                           time.Time
	Plays                                               int
	LastOutcome                                         string
}
type Settings struct {
	StationID, DJID string
	GapMinutes      int
	Version         int
}
type Receipt struct {
	ID, AnnouncementID, StationID, Title, Text, DJID, DJName, Outcome string
	Version                                                           int
	Started, Ended                                                    time.Time
}
type Repository interface {
	List(context.Context, string) ([]Message, error)
	Get(context.Context, string, string) (Message, error)
	Save(context.Context, Message) error
	Delete(context.Context, string, string, int) error
	Settings(context.Context, string) (Settings, error)
	SaveSettings(context.Context, Settings) error
	LastAired(context.Context, string) (time.Time, error)
	Record(context.Context, Receipt) error
	History(context.Context, string, string) ([]Receipt, error)
}

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func invalid(s string) error { return fmt.Errorf("%w: %s", ErrInvalid, s) }
func validText(s string, max int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}
func (m Message) Validate() error {
	if m.ID == "" || m.StationID == "" || strings.TrimSpace(m.Title) == "" || strings.TrimSpace(m.Text) == "" || !validText(m.Title, 200) || !validText(m.Text, 4000) || !validText(m.Pronunciation, 1000) {
		return invalid("provide a title and script or facts (up to 4,000 characters)")
	}
	if !slices.Contains([]string{"message", "news", "promotion"}, m.Category) || !slices.Contains([]string{"exact", "adapt"}, m.Mode) || !slices.Contains([]string{"draft", "active", "paused"}, m.Status) {
		return invalid("choose a category, delivery and status")
	}
	if m.Starts.IsZero() || !m.Expires.After(m.Starts) {
		return invalid("expiry must be after the start")
	}
	if m.Version < 0 || len(m.ShowIDs) > 200 || len(m.EpisodeKey) > 200 || m.EpisodeKey != "" && len(m.ShowIDs) != 1 {
		return invalid("choose one show for a dated episode")
	}
	if !slices.Contains([]string{"once", "interval", "times"}, m.Repeat) {
		return invalid("choose a repeat rule")
	}
	if m.Repeat == "interval" && (m.IntervalMinutes < 1 || m.IntervalMinutes > 10080) {
		return invalid("repeat interval must be 1–10,080 minutes")
	}
	if m.Repeat == "times" {
		if len(m.Times) == 0 || len(m.Times) > 24 {
			return invalid("provide 1–24 daily times")
		}
		seen := map[string]bool{}
		for _, s := range m.Times {
			t, e := time.Parse("15:04", s)
			if e != nil || t.Format("15:04") != s || seen[s] {
				return invalid("use distinct daily times in HH:MM format")
			}
			seen[s] = true
		}
	}
	return nil
}

// Due is the earliest outstanding opportunity. Daily slots collapse to the most
// recent slot; missed repeats never become a backlog. One local slot also avoids
// duplicate delivery in the repeated hour when daylight saving ends.
func (m Message) Due(now time.Time, loc *time.Location) time.Time {
	if m.Status != "active" || !now.Before(m.Expires) {
		return time.Time{}
	}
	if m.Next {
		return m.Starts
	}
	switch m.Repeat {
	case "once":
		if !m.LastAired.IsZero() {
			return time.Time{}
		}
		return m.Starts
	case "interval":
		if m.LastAired.IsZero() {
			return m.Starts
		}
		return m.LastAired.Add(time.Duration(m.IntervalMinutes) * time.Minute)
	case "times":
		local := now.In(loc)
		var latest time.Time
		for _, day := range []time.Time{local.AddDate(0, 0, -1), local} {
			for _, s := range m.Times {
				t, e := time.Parse("15:04", s)
				if e != nil {
					continue
				}
				slot := time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, loc)
				if !slot.After(now) && !slot.Before(m.Starts) && slot.After(latest) {
					latest = slot
				}
			}
		}
		if latest.After(m.LastAired) {
			return latest
		}
	}
	return time.Time{}
}
func (m Message) Matches(showID, episodeKey string) bool {
	return (len(m.ShowIDs) == 0 || slices.Contains(m.ShowIDs, showID)) && (m.EpisodeKey == "" || m.EpisodeKey == episodeKey)
}
func (m Message) Eligible(now time.Time, loc *time.Location, showID, episodeKey string, last time.Time, gap int) bool {
	due := m.Due(now, loc)
	return !due.IsZero() && !due.After(now) && !now.Before(m.Starts) && m.Matches(showID, episodeKey) && (m.Next || last.IsZero() || !now.Before(last.Add(time.Duration(gap)*time.Minute)))
}
func (m Message) State(now time.Time) string {
	if !now.Before(m.Expires) {
		return "Expired"
	}
	if m.Status == "draft" {
		return "Draft"
	}
	if m.Status == "paused" {
		return "Paused"
	}
	if m.Repeat == "once" && !m.LastAired.IsZero() && !m.Next {
		return "Aired"
	}
	if now.Before(m.Starts) {
		return "Scheduled"
	}
	return "Active"
}

// NextOpportunity is a lower bound, not a promise of airtime. It also supplies
// one-shot wakeups when a track's duration is unavailable.
func (m Message) NextOpportunity(now time.Time, loc *time.Location) time.Time {
	if m.Status != "active" || !now.Before(m.Expires) {
		return time.Time{}
	}
	due := m.Due(now, loc)
	if m.Repeat == "times" && !m.Next && due.IsZero() {
		base := now
		if m.Starts.After(base) {
			base = m.Starts
		}
		local := base.In(loc)
		for day := 0; day < 3; day++ {
			date := local.AddDate(0, 0, day)
			for _, s := range m.Times {
				v, err := time.Parse("15:04", s)
				if err != nil {
					continue
				}
				at := time.Date(date.Year(), date.Month(), date.Day(), v.Hour(), v.Minute(), 0, 0, loc)
				if !at.Before(base) && at.After(m.LastAired) && (due.IsZero() || at.Before(due)) {
					due = at
				}
			}
			if !due.IsZero() {
				break
			}
		}
	}
	if !due.IsZero() && due.Before(m.Starts) {
		due = m.Starts
	}
	if !due.Before(m.Expires) {
		return time.Time{}
	}
	return due
}
