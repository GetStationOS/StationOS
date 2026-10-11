// Package station owns operator configuration, independent of playout state.
package station

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var ErrNotFound = errors.New("station not found")
var ErrSlugTaken = errors.New("stream address is already used by another station")
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return "station"
	}
	return b.String()
}

// LogoChange is a write-only normalized JPEG replacement. Nil preserves the logo.
type LogoChange struct {
	Data  []byte
	Clear bool
}
type LogoReader interface {
	Logo(context.Context, string) ([]byte, error)
}

func (s Station) LogoURL() string {
	if s.ID == "" {
		return "/static/station-logo.jpg"
	}
	path := "/station-logos/" + url.PathEscape(s.ID) + ".jpg"
	if s.LogoVersion != "" {
		path += "?v=" + s.LogoVersion
	}
	return path
}

type Station struct {
	LogoVersion string      `json:"logo_version,omitempty"`
	Logo        *LogoChange `json:"-"`
	StreamSlug  string      `json:"stream_slug"`
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Timezone    string      `json:"timezone"`
	Locale      string      `json:"locale"`
}

func (s Station) Validate() error {
	if s.Logo != nil && ((s.Logo.Clear && len(s.Logo.Data) > 0) || (!s.Logo.Clear && (len(s.Logo.Data) == 0 || len(s.Logo.Data) > 5<<20))) {
		return errors.New("invalid logo replacement")
	}
	if s.StreamSlug != "" && (len(s.StreamSlug) > 80 || !slugPattern.MatchString(s.StreamSlug)) {
		return errors.New("stream address must use lowercase letters, numbers and single hyphens (maximum 80 characters)")
	}
	if strings.TrimSpace(s.Name) == "" || len(s.Name) > 200 || len(s.Description) > 4000 || len(s.Locale) > 64 {
		return errors.New("invalid station name, description or locale")
	}
	if s.Timezone == "" {
		return errors.New("timezone is required")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return errors.New("unknown timezone")
	}
	return nil
}

type Repository interface {
	Save(context.Context, Station) error
	Delete(context.Context, string) error
	Get(context.Context, string) (Station, error)
	List(context.Context) ([]Station, error)
}

type StreamLookup interface {
	ByStreamSlug(context.Context, string) (Station, error)
}
type Service struct{ Repo Repository }

func (s Service) Save(ctx context.Context, v Station) (Station, error) {
	v.Name = strings.TrimSpace(v.Name)
	if err := v.Validate(); err != nil {
		return Station{}, err
	}
	if v.ID == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return Station{}, err
		}
		v.ID = hex.EncodeToString(b[:])
	} else if _, err := s.Repo.Get(ctx, v.ID); err != nil {
		return Station{}, err
	}
	if err := s.Repo.Save(ctx, v); err != nil {
		return Station{}, err
	}
	return s.Repo.Get(ctx, v.ID)
}
