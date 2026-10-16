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
	IntroText string `json:"intro"`
	OutroText string `json:"outro"`
	NextKey   string `json:"next_key"`
}
type Prepared struct {
	Plan         Plan
	Intro, Outro []byte
}
type Producer interface {
	Prepare(context.Context, schedule.Occurrence, schedule.Occurrence) (Prepared, error)
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
	}{6, show, next})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ValidatePresentation bounds show speech independently of music selection.
func ValidatePresentation(p Plan) error {
	if len(p.IntroText) > 600 || len(p.OutroText) > 600 {
		return errors.New("DJ presentation exceeds limits")
	}
	return nil
}

// SelectionRequest contains only eligible candidates supplied by broadcasting.
// Providers choose one ID; broadcasting revalidates it before it can play.
type SelectionRequest struct {
	Show             schedule.Occurrence
	Current          content.AudioItem
	Candidates       []content.AudioItem
	RemainingSeconds int64
}

type TrackSelector interface {
	SelectTrack(context.Context, SelectionRequest) (string, error)
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
	Audience         []AudienceItem
	Show, Following  schedule.Occurrence
	Current, Next    content.AudioItem
	Handover         bool
	RemainingSeconds int64
}
type LinkProducer interface {
	Link(context.Context, LinkRequest) ([]byte, error)
}

// ModelSettings supplies independently configured models for AI service roles.
type ModelSettings interface {
	Model(context.Context, string) (string, error)
}

type inferenceNoticeKey struct{}

func WithInferenceNotice(ctx context.Context, notify func(string)) context.Context {
	return context.WithValue(ctx, inferenceNoticeKey{}, notify)
}
func ReportInference(ctx context.Context, notice string) {
	if notify, ok := ctx.Value(inferenceNoticeKey{}).(func(string)); ok {
		notify(notice)
	}
}
