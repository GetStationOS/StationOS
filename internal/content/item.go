// Package content represents broadcast material independently of provider models.
package content

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type Kind string

const (
	Track             Kind = "track"
	Jingle            Kind = "jingle"
	Sweeper           Kind = "sweeper"
	PresenterLink     Kind = "presenter_link"
	Advert            Kind = "advert"
	NewsBulletin      Kind = "news"
	WeatherBulletin   Kind = "weather"
	Feature           Kind = "feature"
	RecordedProgramme Kind = "recorded_programme"
	LiveFeed          Kind = "live_feed"
	PodcastEpisode    Kind = "podcast_episode"
	EmergencyMessage  Kind = "emergency"
)

type Origin string

const (
	Original  Origin = "original"
	Generated Origin = "generated"
	Derived   Origin = "derived"
)

type Resource struct {
	URL  string `json:"url"`
	MIME string `json:"mime"`
}
type AudioItem struct {
	ID           string         `json:"id"`
	SourceID     string         `json:"source_id"`
	SourceItemID string         `json:"source_item_id"`
	Kind         Kind           `json:"kind"`
	Title        string         `json:"title"`
	Artist       string         `json:"artist,omitempty"`
	Composer     string         `json:"composer,omitempty"`
	AlbumArtist  string         `json:"album_artist,omitempty"`
	Conductor    string         `json:"conductor,omitempty"`
	Creator      string         `json:"creator,omitempty"`
	Album        string         `json:"album,omitempty"`
	Genre        string         `json:"genre,omitempty"`
	Duration     *time.Duration `json:"duration_ns,omitempty"`
	Artwork      string         `json:"artwork,omitempty"`
	Resources    []Resource     `json:"resources"`
	Origin       Origin         `json:"origin"`
}

// A forbidden NUL separator keeps source and provider identities unambiguous.
func ID(source, object string) string {
	h := sha256.Sum256([]byte(source + "\x00" + object))
	return hex.EncodeToString(h[:16])
}
func (a AudioItem) Validate() error {
	if a.SourceID == "" || len(a.SourceID) > 128 || len(a.ID) > 128 || a.SourceItemID == "" || strings.ContainsRune(a.SourceID, 0) || strings.ContainsRune(a.SourceItemID, 0) || len(a.SourceItemID) > 2048 || strings.TrimSpace(a.Title) == "" || len(a.Title) > 4096 || len(a.Artist) > 4096 || len(a.Composer) > 4096 || len(a.AlbumArtist) > 4096 || len(a.Conductor) > 4096 || len(a.Creator) > 4096 || len(a.Album) > 4096 || len(a.Genre) > 4096 || len(a.Artwork) > 4096 {
		return errors.New("invalid content metadata")
	}
	switch a.Kind {
	case Track, Jingle, Sweeper, PresenterLink, Advert, NewsBulletin, WeatherBulletin, Feature, RecordedProgramme, LiveFeed, PodcastEpisode, EmergencyMessage:
	default:
		return errors.New("invalid content kind")
	}
	switch a.Origin {
	case Original, Generated, Derived:
	default:
		return errors.New("invalid content origin")
	}
	if a.Duration != nil && (*a.Duration < 0 || *a.Duration > 7*24*time.Hour) {
		return errors.New("invalid content duration")
	}
	for _, resource := range a.Resources {
		if resource.URL == "" || len(resource.URL) > 4096 || resource.MIME == "" || len(resource.MIME) > 256 {
			return errors.New("invalid audio resource")
		}
	}
	if len(a.Resources) > 32 {
		return errors.New("too many audio resources")
	}
	return nil
}
