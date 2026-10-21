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
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/schedule"
	"time"
)

// ErrPlanPending is an expected local-music fallback, not an inference failure.
var ErrPlanPending = errors.New("episode running order is pending; local music continues")

var ErrUnavailable = errors.New("DJ preparation unavailable; continuing with music")

type Plan struct {
	IntroText string `json:"intro"`
	OutroText string `json:"outro"`
	NextKey   string `json:"next_key"`
}
type Prepared struct {
	IntroSpeech, OutroSpeech SpeechProvenance
	Plan                     Plan
	Intro, Outro             []byte
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

// EditorialSession provides one durable context per dated episode.
type EditorialSession interface {
	Context(context.Context, schedule.Occurrence) (string, error)
	Aired(context.Context, schedule.Occurrence, content.AudioItem, string) error
	Remember(context.Context, schedule.Occurrence, string, string) error
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

// LinkProducer may return nil audio and nil error for an intentional normal-link
// skip. Required handovers and audience acknowledgements must contain speech.
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

// WithSpeechTranscript carries the exact text used to synthesize a prepared link.
// Reporting is optional for third-party producers; absence remains unknown.
type transcriptKey struct{}

func WithSpeechTranscript(ctx context.Context, report func(string)) context.Context {
	return context.WithValue(ctx, transcriptKey{}, report)
}
func ReportSpeechTranscript(ctx context.Context, text string) {
	if report, ok := ctx.Value(transcriptKey{}).(func(string)); ok {
		report(text)
	}
}

// PendingSelection preserves an expected fallback's safe cause and retry snapshot.
type PendingSelection struct {
	Reason  string
	RetryAt time.Time
}

func (e *PendingSelection) Error() string { return e.Reason }
func (e *PendingSelection) Unwrap() error { return ErrPlanPending }

// SelectionPlay projects actual and ready music for shared playback eligibility.
type SelectionPlay struct {
	Item    content.AudioItem
	Started time.Time
}
type TrackEligibility interface {
	EligibleTracks(context.Context, schedule.Occurrence, time.Time, []SelectionPlay) ([]content.AudioItem, error)
}

// Speech returns source audio for the existing presenter normalizer.
// Voice, direction and speed are provider capabilities; Nano uses its default voice.
type SpeechRequest struct {
	ProviderName                  string
	Connection                    string
	SpeechVoices                  map[string]string
	Text, Model, Voice, Direction string
	Speed                         float64
}
type SpeechGenerator interface {
	Synthesize(context.Context, SpeechRequest) ([]byte, error)
}
type Speech interface {
	SpeechGenerator
	Identity(context.Context, string) (string, error)
	Budget(context.Context, string) (time.Duration, error)
}

// SpeechProvenance is captured by the adapter that sends the synthesis request.
// Empty fields mean unknown, never inferred from a later DJ profile.
type SpeechProvenance struct {
	Provider, ProviderName, Connection, Model, Voice string
}
type speechProvenanceKey struct{}

func WithSpeechProvenance(ctx context.Context, report func(SpeechProvenance)) context.Context {
	return context.WithValue(ctx, speechProvenanceKey{}, report)
}
func ReportSpeechProvenance(ctx context.Context, value SpeechProvenance) {
	if report, ok := ctx.Value(speechProvenanceKey{}).(func(SpeechProvenance)); ok {
		report(value)
	}
}

// SpeechCapabilitySource reports capabilities for the actual selected endpoint
// and model. Missing metadata never implies cue support.
type SpeechCapabilitySource interface {
	SpeechCapabilities(context.Context, string) (djs.SpeechCapabilities, error)
}
