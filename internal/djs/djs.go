// Package djs owns reusable fictional presenter configuration, not AI execution.
package djs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"net/url"
	"regexp"
	"strings"
)

var ErrInUse = errors.New("This DJ is used by a scheduled show. Update or remove the show first.")
var ErrNotFound = errors.New("DJ not found")
var ErrInvalid = errors.New("invalid DJ settings")
var photoVersion = regexp.MustCompile(`^[a-f0-9]{64}$`)
var modelID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)

type Profile struct {
	ID, Name, Model, Voice, Prompt, Vibe string
	Background, PortraitURL              string
	HasKey                               bool
	ConversationSpeed                    float64
	Photo                                *PhotoChange `json:"-"`
}

// PhotoChange is a write-only normalized JPEG replacement; nil keeps the photo.
type PhotoChange struct {
	Data  []byte
	Clear bool
}
type PhotoReader interface {
	Photo(context.Context, string) ([]byte, error)
}

// KeyChange remains for adapter compatibility. DJ saves accept only the zero value; credentials belong to shared Assistant settings.
type KeyChange struct {
	Value string
	Clear bool
}
type Repository interface {
	List(context.Context) ([]Profile, error)
	Get(context.Context, string) (Profile, error)
	Save(context.Context, Profile, KeyChange) error
	Delete(context.Context, string) error
}
type Service struct{ Repo Repository }

type Voice struct{ ID, Name string }

const DefaultVoice = "cedar"
const DefaultConversationSpeed = 1.0
const MaxKeyLength = 4096

// Built-in OpenAI TTS voices; voice direction is stored separately in Vibe.
func Voices() []Voice {
	return []Voice{{"alloy", "Alloy"}, {"ash", "Ash"}, {"ballad", "Ballad"}, {"cedar", "Cedar"}, {"coral", "Coral"}, {"echo", "Echo"}, {"fable", "Fable"}, {"marin", "Marin"}, {"nova", "Nova"}, {"onyx", "Onyx"}, {"sage", "Sage"}, {"shimmer", "Shimmer"}, {"verse", "Verse"}}
}
func (p Profile) Validate() error {
	if math.IsNaN(p.ConversationSpeed) || math.IsInf(p.ConversationSpeed, 0) || p.ConversationSpeed < 0.25 || p.ConversationSpeed > 4 {
		return errors.New("choose a conversation speed between 0.25× and 4.00×")
	}
	validVoice := false
	for _, v := range Voices() {
		if v.ID == p.Voice {
			validVoice = true
			break
		}
	}
	if !validVoice {
		return errors.New("choose a supported voice")
	}
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 200 {
		return errors.New("enter a fictional name (up to 200 characters)")
	}
	if !modelID.MatchString(p.Model) {
		return errors.New("choose an AI model or enter a valid model ID")
	}
	if len(p.Background) > 16000 {
		return errors.New("background profile is too long")
	}
	if p.Photo != nil && (len(p.Photo.Data) > 1<<20 || (p.Photo.Clear && len(p.Photo.Data) > 0)) {
		return errors.New("invalid photo replacement")
	}
	if p.PortraitURL != "" {
		u, err := url.Parse(p.PortraitURL)
		uploaded := err == nil && p.ID != "" && strings.HasPrefix(p.PortraitURL, "/djs/"+url.PathEscape(p.ID)+"/photo?v=") && photoVersion.MatchString(u.Query().Get("v"))
		local := uploaded || p.PortraitURL == "/static/djs/alex-bright.jpg" || p.PortraitURL == "/static/djs/miles-mercer.jpg" || p.PortraitURL == "/static/djs/clara-vale.jpg"
		if len(p.PortraitURL) > 2048 || err != nil || (!local && (u.Scheme != "https" || u.Hostname() == "" || u.User != nil)) {
			return errors.New("invalid saved DJ photo")
		}
	}
	if len(p.Prompt) > 32000 || len(p.Vibe) > 16000 {
		return errors.New("prompt or vibe is too long")
	}
	return nil
}
func (s Service) Save(ctx context.Context, p Profile, k KeyChange) (Profile, error) {
	if p.ConversationSpeed == 0 {
		p.ConversationSpeed = DefaultConversationSpeed
	}
	if p.Voice == "" {
		p.Voice = DefaultVoice
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Model = strings.TrimSpace(p.Model)
	p.Background = strings.TrimSpace(p.Background)
	p.PortraitURL = strings.TrimSpace(p.PortraitURL)
	if k.Value != "" || k.Clear {
		return Profile{}, errors.Join(ErrInvalid, errors.New("Manage the shared OpenAI API key in Assistant settings."))
	}
	if err := p.Validate(); err != nil {
		return Profile{}, errors.Join(ErrInvalid, err)
	}
	if p.ID == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return Profile{}, err
		}
		p.ID = hex.EncodeToString(b[:])
	} else if _, err := s.Repo.Get(ctx, p.ID); err != nil {
		return Profile{}, err
	}
	if err := s.Repo.Save(ctx, p, k); err != nil {
		return Profile{}, err
	}
	return s.Repo.Get(ctx, p.ID)
}

const ExampleVibe = `Voice: Deep, hushed, and enigmatic, with a slow, deliberate cadence that draws the listener in.

Phrasing: Sentences are short and rhythmic, building tension with pauses and carefully placed suspense.

Punctuation: Dramatic pauses, ellipses, and abrupt stops enhance the feeling of unease and anticipation.

Tone: Dark, ominous, and foreboding, evoking a sense of mystery and the unknown.`
