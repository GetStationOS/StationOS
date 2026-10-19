// Package sources owns the provider contract; adapters hide protocol details.
package sources

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"strings"

	"stationos.local/stationos/internal/content"
)

var ErrNotFound = errors.New("source not found")
var ErrUnsupported = errors.New("operation unsupported")
var ErrConflict = errors.New("source configuration changed during import; retry")

type Permission string

const (
	Unknown             Permission = "unknown"
	Allowed             Permission = "allowed"
	Prohibited          Permission = "prohibited"
	OperatorResponsible Permission = "operator_responsible"
)

type Capabilities struct {
	Browse          bool
	Search          bool
	Metadata        bool
	Artwork         bool
	Streaming       bool
	Seeking         bool
	Enumeration     bool
	Recommendations bool
}
type Restrictions struct {
	Caching                   Permission
	Downloading               Permission
	Crossfading               Permission
	Ducking                   Permission
	Analysis                  Permission
	PublicBroadcast           Permission
	PlaybackReportingRequired bool
}
type Profile struct {
	Capabilities Capabilities
	Restrictions Restrictions
}
type Config struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Endpoint string `json:"endpoint"`
	Revision int64  `json:"revision"`
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 200 || c.Kind != "upnp" {
		return errors.New("invalid source configuration")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(c.Endpoint) > 4096 {
		return errors.New("invalid source description URL")
	}
	return nil
}

type Entry struct {
	ContainerID    string
	ContainerTitle string
	Item           *content.AudioItem
}
type Page struct {
	Entries  []Entry
	Total    int
	UpdateID string
}
type AudioSource interface {
	Profile() Profile
	Browse(context.Context, string, int, int) (Page, error)
	Get(context.Context, string) (content.AudioItem, error)
	Resolve(context.Context, string) ([]content.Resource, error)
}

// StreamOpener fetches a freshly resolved resource through the adapter's checked
// transport. Consumers never open catalogue URLs themselves.
type StreamOpener interface {
	OpenStream(context.Context, string) (io.ReadCloser, string, error)
}

// CatalogueRootProvider identifies a complete enumeration view when navigation
// folders contain virtual or repeated views of the same content.
type CatalogueRootProvider interface {
	CatalogueRoot(context.Context) (string, error)
}

type Searcher interface {
	Search(context.Context, string, int, int) (Page, error)
}
type Factory func(context.Context, Config) (AudioSource, error)
type Repository interface {
	Save(context.Context, Config) error
	Get(context.Context, string) (Config, error)
	List(context.Context) ([]Config, error)
	Delete(context.Context, string) error
}
type Service struct{ Repo Repository }

func (s Service) Save(ctx context.Context, c Config) (Config, error) {
	c.Name = strings.TrimSpace(c.Name)
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	if c.ID == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return Config{}, err
		}
		c.ID = hex.EncodeToString(b[:])
		c.Revision = 1
	} else {
		old, err := s.Repo.Get(ctx, c.ID)
		if err != nil {
			return Config{}, err
		}
		c.Revision = old.Revision + 1
	}
	return c, s.Repo.Save(ctx, c)
}

// ArtworkReader retrieves metadata artwork through the source's checked
// transport. The UI never opens arbitrary resource URLs itself.
type ArtworkReader interface {
	Artwork(context.Context, string) ([]byte, string, error)
}

// DiscoveredSource is a verified music-server description, not saved configuration.
type DiscoveredSource struct {
	Name, Endpoint, Model string
}

type Discoverer interface {
	Discover(context.Context) ([]DiscoveredSource, error)
}
