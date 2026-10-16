// Package prompts owns editable shared AI instructions and their shipped defaults.
package prompts

import (
	"context"
	"embed"
	"errors"
	"strings"
	"unicode/utf8"
)

//go:embed defaults/*.txt
var defaults embed.FS

const MaxLength = 64000

var ErrNotFound = errors.New("prompt not found")

type Definition struct{ ID, Name, Description string }

var definitions = []Definition{
	{ID: "assistant", Name: "Producer assistant", Description: "How the assistant manages stations, playlists, DJs and schedules."},
	{ID: "show", Name: "Show introduction and sign-off", Description: "Introduces the show and hands over to the following presenter."},
	{ID: "link", Name: "Between-track speech", Description: "Writes the spoken link between the current and next recording."},
	{ID: "background", Name: "Fictional character guidance", Description: "How DJ backgrounds inform musical choices and on-air personality."},
	{ID: "audience-link", Name: "Listener requests and shout-outs", Description: "Additional speech instructions when a listener contribution is included."},
	{ID: "selection", Name: "Track selection", Description: "Chooses the next recording from eligible candidates."},
	{ID: "audience-decisions", Name: "Audience moderation (decisions)", Description: "Reviews listener submissions through the decisions endpoint."},
	{ID: "audience", Name: "Audience moderation", Description: "Reviews listener requests and shout-outs for suitability."},
	{ID: "logo", Name: "Station logo", Description: "Visual direction for generated station logos."},
	{ID: "portrait", Name: "DJ portrait", Description: "Visual direction for generated fictional presenter portraits."},
}

func Definitions() []Definition { return append([]Definition(nil), definitions...) }
func Find(id string) (Definition, bool) {
	for _, d := range definitions {
		if d.ID == id {
			return d, true
		}
	}
	return Definition{}, false
}
func Default(id string) (string, error) {
	if _, ok := Find(id); !ok {
		return "", ErrNotFound
	}
	b, err := defaults.ReadFile("defaults/" + id + ".txt")
	return string(b), err
}

type Store interface {
	Prompt(context.Context, string) (string, error)
	SavePrompt(context.Context, string, string) error
	ResetPrompt(context.Context, string) error
}

func Validate(id, text string) error {
	if _, ok := Find(id); !ok {
		return ErrNotFound
	}
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || len(text) > MaxLength {
		return errors.New("Enter a prompt of up to 64,000 bytes")
	}
	return nil
}

// Load falls back only for an unconfigured store; storage failures remain visible.
func Load(ctx context.Context, store Store, id string) (string, error) {
	if store == nil {
		return Default(id)
	}
	return store.Prompt(ctx, id)
}
