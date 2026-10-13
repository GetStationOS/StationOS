// Package audience owns listener submissions independently of catalogue and playout.
package audience

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

var ErrInvalid = errors.New("check your name, message and track choice")
var ErrClosed = errors.New("this show is no longer accepting that submission; choose an available show")
var ErrLimited = errors.New("you have sent several submissions recently; please try again later")
var ErrDuplicate = errors.New("you have already sent this submission for this show")
var ErrNotFound = errors.New("submission not found")
var ErrConflict = errors.New("submission has already been used, dismissed or expired")

const MaxMessages = 200
const MaxBatch = 4

type Kind string

const (
	Request  Kind = "request"
	Shoutout Kind = "shoutout"
)

type Status string

const (
	Pending      Status = "pending"
	ReviewNeeded Status = "review"
	Approved     Status = "approved"
	Rejected     Status = "rejected"
	Used         Status = "used"
	Expired      Status = "expired"
)

type Message struct {
	ID, StationID, ShowID, OccurrenceKey, ShowName, SenderKey string
	Kind                                                      Kind
	Status                                                    Status
	Name, Text, TrackID, TrackTitle, TrackArtist, Reason      string
	Created, ShowStart, Expires, UsedAt                       time.Time
}

func (m Message) Validate() error {
	if m.ID == "" || m.StationID == "" || m.ShowID == "" || m.OccurrenceKey == "" || len(m.SenderKey) != 64 || !m.Expires.After(m.Created) || !m.Expires.After(m.ShowStart) {
		return ErrInvalid
	}
	if !validText(m.Name, 80) || !validText(m.Text, 400) {
		return ErrInvalid
	}
	switch m.Kind {
	case Request:
		if m.TrackID == "" {
			return ErrInvalid
		}
	case Shoutout:
		if m.TrackID != "" || strings.TrimSpace(m.Text) == "" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if m.Status != Pending {
		return ErrInvalid
	}
	return nil
}
func validText(s string, max int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

type Decision struct {
	ID        string
	Approve   bool
	Uncertain bool
}
type Repository interface {
	Submit(context.Context, Message) error
	List(context.Context, string, string, int, int, time.Time) ([]Message, int, error)
	ForShow(context.Context, string, string, Status, time.Time, int) ([]Message, error)
	Decide(context.Context, string, string, Status, string, time.Time) error
	Review(context.Context, string, []Decision, time.Time) error
	MarkUsed(context.Context, string, []string, time.Time) error
	Usable(context.Context, string, string, []string, time.Time) (bool, error)
}
type Pool interface {
	Pool(context.Context, string) ([]content.AudioItem, error)
}
type Service struct {
	Stations station.Repository
	Shows    schedule.Repository
	Pool     Pool
	Repo     Repository
	Now      func() time.Time
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Opportunities projects the next occurrence of each show that accepts this kind.
func (s Service) Opportunities(ctx context.Context, stationID string, kind Kind) ([]schedule.Occurrence, error) {
	if kind != Request && kind != Shoutout {
		return nil, ErrInvalid
	}
	st, err := s.Stations.Get(ctx, stationID)
	if err != nil {
		return nil, err
	}
	if !st.Public() {
		return nil, station.ErrNotFound
	}
	shows, err := s.Shows.List(ctx, stationID)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(st.Timezone)
	if err != nil {
		return nil, err
	}
	now := s.now()
	seen := map[string]bool{}
	out := []schedule.Occurrence{}
	for _, o := range schedule.Occurrences(shows, now, loc) {
		if !o.End.After(now) || o.Start.After(now.Add(7*24*time.Hour)) || seen[o.ShowID] {
			continue
		}
		if kind == Request && !o.AcceptRequests || kind == Shoutout && !o.AcceptShoutouts {
			continue
		}
		seen[o.ShowID] = true
		out = append(out, o)
	}
	return out, nil
}
func (s Service) Target(ctx context.Context, stationID, key string, kind Kind) (schedule.Occurrence, error) {
	items, err := s.Opportunities(ctx, stationID, kind)
	if err != nil {
		return schedule.Occurrence{}, err
	}
	for _, o := range items {
		if o.Key == key {
			return o, nil
		}
	}
	return schedule.Occurrence{}, ErrClosed
}
func (s Service) Submit(ctx context.Context, stationID, key, sender, name, text, trackID string, kind Kind) (Message, error) {
	o, err := s.Target(ctx, stationID, key, kind)
	if err != nil {
		return Message{}, err
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return Message{}, err
	}
	m := Message{ID: hex.EncodeToString(id[:]), StationID: stationID, ShowID: o.ShowID, OccurrenceKey: o.Key, ShowName: o.Title, SenderKey: sender, Kind: kind, Status: Pending, Name: strings.TrimSpace(name), Text: strings.TrimSpace(text), TrackID: trackID, Created: s.now(), ShowStart: o.Start, Expires: o.End}
	if kind == Request {
		pool, err := s.Pool.Pool(ctx, o.PlaylistID)
		if err != nil {
			return Message{}, err
		}
		for _, a := range pool {
			if a.ID == trackID {
				m.TrackTitle = a.Title
				m.TrackArtist = a.Artist
				break
			}
		}
		if m.TrackTitle == "" {
			return Message{}, ErrInvalid
		}
	}
	if err = m.Validate(); err != nil {
		return Message{}, err
	}
	if err = s.Repo.Submit(ctx, m); err != nil {
		return Message{}, err
	}
	return m, nil
}
