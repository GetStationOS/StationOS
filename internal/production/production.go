// Package production prepares immutable presenter plans and audio off the playout path.
package production

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/schedule"
	"time"
)

var ErrUnavailable = errors.New("DJ preparation unavailable; continuing with music")

type Plan struct {
	TrackIDs  []string `json:"track_ids"`
	IntroText string   `json:"intro"`
	OutroText string   `json:"outro"`
	NextKey   string   `json:"next_key"`
}
type Prepared struct {
	Plan         Plan
	Intro, Outro []byte
}
type Producer interface {
	Prepare(context.Context, schedule.Occurrence, schedule.Occurrence, []content.AudioItem) (Prepared, error)
}
type Store interface {
	Get(context.Context, string) (Prepared, error)
	Put(context.Context, string, string, Prepared) error
}
type Credentials interface {
	APIKey(context.Context, string) (string, error)
}
type Normalizer interface {
	MP3(context.Context, []byte) ([]byte, error)
}

func CacheKey(show, next schedule.Occurrence) string {
	// Version the prepared speech contract: level the voice before caching,
	// preserve editorial pauses and use explicit performer/composer credits.
	// Portraits are presentation only; changing one must not regenerate speech.
	show.DJ.PortraitURL = ""
	next.DJ.PortraitURL = ""
	raw, _ := json.Marshal(struct {
		AudioVersion int
		Show, Next   schedule.Occurrence
	}{5, show, next})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func Validate(p Plan, pool []content.AudioItem) error {
	if len(p.TrackIDs) == 0 || len(p.TrackIDs) > 64 || len(p.IntroText) > 600 || len(p.OutroText) > 600 {
		return errors.New("DJ plan exceeds limits")
	}
	allowed := map[string]bool{}
	for _, track := range pool {
		allowed[track.ID] = true
	}
	seen := map[string]bool{}
	for _, id := range p.TrackIDs {
		if !allowed[id] || seen[id] {
			return errors.New("DJ selected unavailable or duplicate music")
		}
		seen[id] = true
	}
	return nil
}

// ReadBounded rejects oversized model/audio responses without retaining more than the limit.
func ReadBounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, errors.New("production response exceeds limit")
	}
	return b, err
}

const Timeout = 90 * time.Second

type LinkRequest struct {
	Show, Following  schedule.Occurrence
	Current, Next    content.AudioItem
	Handover         bool
	RemainingSeconds int64
}
type LinkProducer interface {
	Link(context.Context, LinkRequest) ([]byte, error)
}
