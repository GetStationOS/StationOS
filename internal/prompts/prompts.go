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
	{ID: "show", Name: "DJ show introduction and sign-off", Description: "Opens the show and closes it with a handover; does not choose recordings."},
	{ID: "link", Name: "DJ links between tracks", Description: "Introduces the committed next track, or signs off to the incoming presenter."},
	{ID: "background", Name: "DJ character and personality", Description: "Uses the fictional background naturally without inventing personal history."},
	{ID: "audience-link", Name: "Reading listener requests and shout-outs", Description: "Adds approved listener contributions to a spoken DJ link."},
	{ID: "selection", Name: "DJ track selection", Description: "Chooses one eligible recording to add behind the accepted queue."},
	{ID: "audience-decisions", Name: "Check one listener message", Description: "Checks whether one listener submission is suitable to read aloud."},
	{ID: "audience", Name: "Review listener messages", Description: "Approves, dismisses or flags each supplied listener submission for review."},
	{ID: "logo", Name: "Station logo design", Description: "Creates distinctive station artwork that remains legible at small sizes."},
	{ID: "portrait", Name: "Fictional DJ portrait", Description: "Creates a studio portrait from the supplied fictional presenter profile."},
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
