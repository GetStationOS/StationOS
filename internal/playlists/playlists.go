// Package playlists owns station-specific selections from the shared catalogue.
package playlists

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/station"
)

var (
	ErrInUse       = errors.New("This playlist is used by a scheduled show. Update or remove the show first.")
	ErrNotFound    = errors.New("playlist not found")
	ErrInvalid     = errors.New("invalid playlist selection")
	ErrUnavailable = errors.New("selected catalogue item is unavailable")
	ErrTooLarge    = errors.New("playlist exceeds the 10000-item limit")
)

const MaxItems = 10000
const MaxRules = 100

type Playlist struct {
	ID          string
	StationID   string
	Name        string
	Description string
}

func (p Playlist) Validate() error {
	if p.StationID == "" || strings.TrimSpace(p.Name) == "" || len(p.Name) > 200 || len(p.Description) > 4000 {
		return ErrInvalid
	}
	return nil
}

type Rule struct {
	ID       string
	Kind     string
	Value    string
	SourceID string
}

func (r Rule) Validate() error {
	if strings.TrimSpace(r.Value) == "" || len(r.Value) > 4096 || len(r.SourceID) > 128 {
		return ErrInvalid
	}
	switch r.Kind {
	case "genre":
		if r.SourceID != "" {
			return ErrInvalid
		}
	case "album":
		if r.SourceID == "" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

type Entry struct {
	Item      content.AudioItem
	Available bool
}
type Result struct {
	Entries          []Entry
	Total            int
	Unavailable      int
	Duration         time.Duration
	UnknownDurations int
}

// Availability counts eligibility without transferring recording metadata.
type Availability struct{ Total, Unavailable int }
type AvailabilityRepository interface {
	Availability(context.Context, string) (Availability, error)
}

type Repository interface {
	Save(context.Context, Playlist) error
	Delete(context.Context, string) error
	Get(context.Context, string) (Playlist, error)
	List(context.Context, string) ([]Playlist, error)
	Rules(context.Context, string) ([]Rule, error)
	AddRule(context.Context, string, Rule) error
	ReplaceRules(context.Context, string, []Rule) error
	RemoveRule(context.Context, string, string) error
	SetIncluded(context.Context, string, []string, bool) error
	Resolve(context.Context, string, int, int) (Result, error)
}
type StationLookup interface {
	Get(context.Context, string) (station.Station, error)
}
type Service struct {
	Repo     Repository
	Stations StationLookup
}

func id() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func (s Service) Save(ctx context.Context, p Playlist) (Playlist, error) {
	p.Name = strings.TrimSpace(p.Name)
	if err := p.Validate(); err != nil {
		return Playlist{}, err
	}
	if _, err := s.Stations.Get(ctx, p.StationID); err != nil {
		if errors.Is(err, station.ErrNotFound) {
			return Playlist{}, ErrInvalid
		}
		return Playlist{}, err
	}
	if p.ID == "" {
		var err error
		p.ID, err = id()
		if err != nil {
			return Playlist{}, err
		}
	} else {
		old, err := s.Repo.Get(ctx, p.ID)
		if err != nil {
			return Playlist{}, err
		}
		if old.StationID != p.StationID {
			return Playlist{}, ErrInvalid
		}
	}
	return p, s.Repo.Save(ctx, p)
}
func (s Service) AddRule(ctx context.Context, playlistID string, r Rule) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if _, err := s.Repo.Get(ctx, playlistID); err != nil {
		return err
	}
	var err error
	r.ID, err = id()
	if err != nil {
		return err
	}
	return s.Repo.AddRule(ctx, playlistID, r)
}
func (s Service) Select(ctx context.Context, playlistID string, ids []string, included bool) error {
	if len(ids) < 1 || len(ids) > 100 {
		return ErrInvalid
	}
	for _, id := range ids {
		if id == "" || len(id) > 128 {
			return ErrInvalid
		}
	}
	return s.Repo.SetIncluded(ctx, playlistID, ids, included)
}

// ReplaceRules commits the complete checkbox selection as one operator action.
func (s Service) ReplaceRules(ctx context.Context, playlistID string, rules []Rule) error {
	if len(rules) > MaxRules {
		return ErrInvalid
	}
	for n := range rules {
		if err := rules[n].Validate(); err != nil {
			return err
		}
		var err error
		rules[n].ID, err = id()
		if err != nil {
			return err
		}
	}
	return s.Repo.ReplaceRules(ctx, playlistID, rules)
}
