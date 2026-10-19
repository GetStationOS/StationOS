// Package schedule owns recurring shows in station-local civil time.
package schedule

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"stationos.local/stationos/internal/djs"
	"strconv"
	"strings"
	"time"
)

var ErrNotFound = errors.New("show not found")
var ErrInvalid = errors.New("invalid show settings")
var ErrOverlap = errors.New("this show overlaps another show; choose a free time")
var ErrInUse = errors.New("this item is used by a scheduled show; remove or update that show first")

const WeekMinutes = 7 * 24 * 60
const MaxShows = 200

type Show struct {
	AcceptRequests, AcceptShoutouts       bool
	ID, StationID, Name, DJID, PlaylistID string
	Description                           string
	MusicDirection                        string
	Day, Start, End                       int // Monday=0; minutes after midnight. End <= Start means next day.
	Revision                              int
	DJName, PlaylistName                  string
	DJ                                    djs.Profile
}

func (s Show) Duration() int {
	d := s.End - s.Start
	if d <= 0 {
		d += 1440
	}
	return d
}

// Invalid preserves the validation category while identifying the correction.
func Invalid(reason string) error { return fmt.Errorf("%w: %s", ErrInvalid, reason) }

// Normalized keeps the stored midnight convention while accepting a caller's
// unambiguous 24:00 end-of-day boundary. Start times never roll into another day.
func (s Show) Normalized() Show {
	s.Name = strings.TrimSpace(s.Name)
	s.Description = strings.TrimSpace(s.Description)
	s.MusicDirection = strings.TrimSpace(s.MusicDirection)
	if s.End == 1440 {
		s.End = 0
	}
	return s
}

func (s Show) Validate() error {
	switch {
	case s.StationID == "":
		return Invalid("choose an existing station (station_id)")
	case strings.TrimSpace(s.Name) == "":
		return Invalid("supply a show name (name)")
	case len(s.Name) > 200:
		return Invalid("show name must be at most 200 characters")
	case len(s.Description) > 2000:
		return Invalid("show description must be at most 2000 characters")
	case len(s.MusicDirection) > 4000:
		return Invalid("DJ music prompt must be at most 4000 characters")
	case s.DJID == "":
		return Invalid("choose an existing presenter (dj_id)")
	case s.PlaylistID == "":
		return Invalid("choose an existing station playlist (playlist_id)")
	case s.Day < 0 || s.Day > 6:
		return Invalid("day must be 0–6: Monday=0, Tuesday=1, Wednesday=2, Thursday=3, Friday=4, Saturday=5, Sunday=6")
	case s.Start < 0 || s.Start >= 1440:
		return Invalid("start must be minutes after midnight from 0 to 1439; for example 480 is 08:00")
	case s.End < 0 || s.End >= 1440:
		return Invalid("end must be minutes after midnight from 0 to 1439; use 0 for midnight, not 2400")
	case s.Start%15 != 0:
		return Invalid("start must be a multiple of 15 minutes")
	case s.End%15 != 0:
		return Invalid("end must be a multiple of 15 minutes")
	}
	return nil
}

// Conflict identifies the saved show that prevented a write. Week boundaries
// and overnight endings are explicit so callers can repair the intended slot.
func Conflict(requested, existing Show) error {
	return fmt.Errorf("%w: requested %q (%s) conflicts with saved %q (%s, show_id=%s)", ErrOverlap, requested.Name, slotLabel(requested), existing.Name, slotLabel(existing), existing.ID)
}

func slotLabel(s Show) string {
	days := [...]string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	endDay := s.Day
	if s.End <= s.Start {
		endDay = (endDay + 1) % 7
	}
	return fmt.Sprintf("%s %s–%s %s", days[s.Day], TimeLabel(s.Start), days[endDay], TimeLabel(s.End))
}

func Overlaps(a, b Show) bool {
	x := a.Day*1440 + a.Start
	y := b.Day*1440 + b.Start
	for _, shift := range []int{-WeekMinutes, 0, WeekMinutes} {
		if x < y+shift+b.Duration() && y+shift < x+a.Duration() {
			return true
		}
	}
	return false
}

type Repository interface {
	List(context.Context, string) ([]Show, error)
	Get(context.Context, string, string) (Show, error)
	Save(context.Context, Show) error
	SaveMany(context.Context, []Show) error
	Delete(context.Context, string, string) error
}
type Service struct{ Repo Repository }

func (s Service) Save(ctx context.Context, v Show) (Show, error) {
	v = v.Normalized()
	if err := v.Validate(); err != nil {
		return Show{}, err
	}
	if v.ID == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return Show{}, err
		}
		v.ID = hex.EncodeToString(b[:])
	} else if _, err := s.Repo.Get(ctx, v.StationID, v.ID); err != nil {
		return Show{}, err
	}
	if err := s.Repo.Save(ctx, v); err != nil {
		return Show{}, err
	}
	return s.Repo.Get(ctx, v.StationID, v.ID)
}

// CreateDays creates independent weekly shows in one atomic write.
func (s Service) CreateDays(ctx context.Context, v Show, days []int) error {
	if v.ID != "" {
		return Invalid("omit id when creating new weekly shows; edit an existing show separately")
	}
	if len(days) == 0 || len(days) > 7 {
		return Invalid("choose one to seven weekdays in days, using Monday=0 through Sunday=6")
	}
	v = v.Normalized()
	shows := make([]Show, 0, len(days))
	seen := map[int]bool{}
	for _, day := range days {
		if seen[day] {
			return Invalid("each weekday may appear only once in days")
		}
		seen[day] = true
		show := v
		show.Day = day
		if err := show.Validate(); err != nil {
			return err
		}
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		show.ID = hex.EncodeToString(b[:])
		shows = append(shows, show)
	}
	return s.Repo.SaveMany(ctx, shows)
}

type Occurrence struct {
	AcceptRequests, AcceptShoutouts                 bool
	Key, StationID, Title, PlaylistID, PlaylistName string
	ShowID                                          string
	MusicDirection                                  string
	DJ                                              djs.Profile
	Start, End                                      time.Time
}
type Timeline struct{ Current, Next Occurrence }
type Source interface {
	Resolve(context.Context, string, time.Time) (Timeline, error)
}

func Monday(now time.Time, loc *time.Location) time.Time {
	t := now.In(loc)
	days := (int(t.Weekday()) + 6) % 7
	return time.Date(t.Year(), t.Month(), t.Day()-days, 0, 0, 0, 0, loc)
}

// WallTime resolves repeated boundaries at their first occurrence, so adjacent
// shows share one boundary. A missing spring-forward
// wall time moves to the next valid minute; collapsed occurrences are skipped.
func WallTime(date time.Time, minutes int, end bool) time.Time {
	loc := date.Location()
	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	desired := day.Add(time.Duration(minutes) * time.Minute)
	candidate := time.Date(desired.Year(), desired.Month(), desired.Day(), desired.Hour(), desired.Minute(), 0, 0, loc)
	var match time.Time
	for t := candidate.Add(-3 * time.Hour); !t.After(candidate.Add(3 * time.Hour)); t = t.Add(time.Minute) {
		wall := t.In(loc)
		naive := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), 0, 0, time.UTC)
		if naive.Equal(desired) {
			if match.IsZero() {
				match = t
			}
			return t
		}
	}
	if !match.IsZero() {
		return match
	}
	for i := 1; i <= 180; i++ {
		target := desired.Add(time.Duration(i) * time.Minute)
		t := time.Date(target.Year(), target.Month(), target.Day(), target.Hour(), target.Minute(), 0, 0, loc)
		wall := t.In(loc)
		if wall.Year() == target.Year() && wall.YearDay() == target.YearDay() && wall.Hour() == target.Hour() && wall.Minute() == target.Minute() {
			return t
		}
	}
	return candidate
}
func Occurrences(shows []Show, now time.Time, loc *time.Location) []Occurrence {
	monday := Monday(now, loc)
	out := []Occurrence{}
	for _, week := range []int{-1, 0, 1, 2} {
		for _, show := range shows {
			date := monday.AddDate(0, 0, week*7+show.Day)
			start := WallTime(date, show.Start, false)
			end := WallTime(date, show.Start+show.Duration(), true)
			if !end.After(start) {
				continue
			}
			out = append(out, Occurrence{AcceptRequests: show.AcceptRequests, AcceptShoutouts: show.AcceptShoutouts, Key: show.ID + ":" + start.UTC().Format(time.RFC3339) + ":" + strconv.Itoa(show.Revision), StationID: show.StationID, ShowID: show.ID, MusicDirection: show.MusicDirection, Title: show.Name, PlaylistID: show.PlaylistID, PlaylistName: show.PlaylistName, DJ: show.DJ, Start: start, End: end})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}
func Resolve(shows []Show, stationID, defaultPlaylist, defaultName string, now time.Time, loc *time.Location) Timeline {
	return ResolveOccurrences(Occurrences(shows, now, loc), stationID, defaultPlaylist, defaultName, now)
}

// ResolveOccurrences resolves an already filtered dated programme schedule.
func ResolveOccurrences(all []Occurrence, stationID, defaultPlaylist, defaultName string, now time.Time) Timeline {
	fallback := Occurrence{Key: "default:" + stationID + ":" + defaultPlaylist, StationID: stationID, Title: "Continuous music", PlaylistID: defaultPlaylist, PlaylistName: defaultName}
	timeline := Timeline{Current: fallback}
	for _, o := range all {
		if !now.Before(o.Start) && now.Before(o.End) {
			timeline.Current = o
			break
		}
	}
	boundary := now
	if timeline.Current.ShowID != "" {
		boundary = timeline.Current.End
	}
	for _, o := range all {
		if !o.Start.Before(boundary) {
			if timeline.Current.ShowID == "" {
				timeline.Current.End = o.Start
				timeline.Next = o
			} else if o.Start.Equal(boundary) {
				timeline.Next = o
			} else {
				timeline.Next = fallback
				timeline.Next.Start = boundary
				timeline.Next.End = o.Start
			}
			return timeline
		}
	}
	if timeline.Current.ShowID != "" {
		timeline.Next = fallback
		timeline.Next.Start = boundary
	}
	return timeline
}
func TimeLabel(minutes int) string {
	return time.Date(2000, 1, 1, minutes/60, minutes%60, 0, 0, time.UTC).Format("15:04")
}

// Cancellations supplies dated exceptions without removing recurring templates.
type Cancellations interface {
	CancelledEpisodes(context.Context, string) (map[string]bool, error)
}

// EpisodeKey identifies a dated occurrence independently of template revision.
func EpisodeKey(key string) string {
	i := strings.LastIndex(key, ":")
	if i < 0 {
		return key
	}
	suffix := key[i+1:]
	if suffix == "" {
		return key
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return key
		}
	}
	return key[:i]
}
