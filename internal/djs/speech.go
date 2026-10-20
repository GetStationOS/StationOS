package djs

import (
	"context"
	"errors"
	"maps"
	"math"
	"regexp"
)

const DefaultSpeechModel = "gpt-4o-mini-tts"

type SpeechPreference struct {
	Model string  `json:"model"`
	Voice string  `json:"voice"`
	Speed float64 `json:"speed"`
	Vibe  string  `json:"vibe"`
}
type SpeechSelection struct {
	Connection string           `json:"connection"`
	Settings   SpeechPreference `json:"settings"`
}
type SpeechEndpoint struct{ ID, Name, Provider, Model string }
type SpeechSource interface {
	VoiceSource
	SpeechConnections(context.Context) ([]SpeechEndpoint, error)
}
type speechContextKey struct{}

func WithSpeechConnection(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, speechContextKey{}, id)
}
func SpeechConnection(ctx context.Context) string {
	id, _ := ctx.Value(speechContextKey{}).(string)
	return id
}

// Preference restores saved endpoint settings, or supplies provider-specific defaults.
func (p Profile) Preference(id, provider string) SpeechPreference {
	if pref, ok := p.SpeechSettings[id]; ok {
		return pref
	}
	if provider == "chatterbox" {
		return SpeechPreference{Model: "chatterbox-nano", Voice: p.SpeechVoices[id], Speed: 1}
	}
	pref := SpeechPreference{Model: DefaultSpeechModel, Voice: p.Voice, Speed: p.ConversationSpeed, Vibe: p.Vibe}
	if pref.Voice == "" {
		pref.Voice = DefaultVoice
	}
	if pref.Speed == 0 {
		pref.Speed = 1
	}
	if id == p.SpeechConnection && p.SpeechModel != "" && p.SpeechModel != "chatterbox-nano" {
		pref.Model = p.SpeechModel
	}
	return pref
}
func (p Profile) ActiveSpeech() SpeechPreference {
	if pref, ok := p.SpeechSettings[p.SpeechConnection]; ok {
		return pref
	}
	return SpeechPreference{Model: p.SpeechModel, Voice: p.Voice, Speed: p.ConversationSpeed, Vibe: p.Vibe}
}

var speechModelID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/@+-]{0,127}$`)

func validateSpeech(pref SpeechPreference) error {
	if !speechModelID.MatchString(pref.Model) || math.IsNaN(pref.Speed) || math.IsInf(pref.Speed, 0) || pref.Speed < 0.25 || pref.Speed > 4 || len(pref.Vibe) > 16000 || (pref.Voice != "" && !ValidVoiceID(pref.Voice)) {
		return errors.New("Choose valid speech model, voice, speed and direction settings")
	}
	return nil
}

func (s Service) applySpeech(ctx context.Context, p *Profile) error {
	change := p.SpeechChange
	if change == nil {
		return nil
	}
	source, ok := s.Voices.(SpeechSource)
	if !ok {
		return errors.New("Speech endpoint settings are unavailable")
	}
	endpoints, err := source.SpeechConnections(ctx)
	if err != nil {
		return errors.New("Could not load speech endpoints")
	}
	choices := map[string]SpeechEndpoint{}
	for _, endpoint := range endpoints {
		choices[endpoint.ID] = endpoint
	}
	if _, ok := choices[change.Connection]; !ok {
		return errors.New("Choose an available speech endpoint")
	}
	pending := maps.Clone(p.SpeechDraft)
	if pending == nil {
		pending = map[string]SpeechPreference{}
	}
	selected := change.Settings
	if selected == (SpeechPreference{}) {
		selected = p.Preference(change.Connection, choices[change.Connection].Provider)
	}
	pending[change.Connection] = selected
	if len(pending) > 20 {
		return errors.New("Too many speech endpoint edits; save fewer at once")
	}
	if p.SpeechSettings == nil {
		p.SpeechSettings = map[string]SpeechPreference{}
	}
	for id, pref := range pending {
		endpoint, ok := choices[id]
		if !ok {
			return errors.New("A speech endpoint was removed; reopen this DJ before saving")
		}
		previous := p.Preference(id, endpoint.Provider)
		if pref.Model == "" {
			pref.Model = previous.Model
		}
		if endpoint.Provider == "openai" && pref.Voice == "" {
			pref.Voice = previous.Voice
		}
		if pref.Speed == 0 {
			pref.Speed = previous.Speed
			if pref.Speed == 0 {
				pref.Speed = 1
			}
		}
		if endpoint.Provider == "chatterbox" {
			pref.Model = endpoint.Model
			pref.Speed = 1
			pref.Vibe = ""
		}
		if err := validateSpeech(pref); err != nil {
			return err
		}
		if endpoint.Provider == "openai" {
			found := false
			for _, voice := range Voices() {
				if voice.ID == pref.Voice {
					found = true
				}
			}
			if !found {
				return errors.New("Choose a supported OpenAI voice")
			}
		} else if endpoint.Provider == "chatterbox" {
			previous := p.Preference(id, endpoint.Provider)
			if pref.Voice != "" && pref.Voice != previous.Voice {
				catalog, err := source.Voices(WithSpeechConnection(ctx, id))
				if err != nil {
					return errors.New("Could not discover Chatterbox voices; keep the saved voice or try again")
				}
				found := false
				for _, voice := range catalog.Voices {
					if voice.ID == pref.Voice {
						found = true
					}
				}
				if !found {
					return errors.New("Choose an available Chatterbox voice")
				}
			}
			if p.SpeechVoices == nil {
				p.SpeechVoices = map[string]string{}
			}
			p.SpeechVoices[id] = pref.Voice
		} else {
			return errors.New("Choose a supported speech provider")
		}
		p.SpeechSettings[id] = pref
	}
	p.SpeechConnection = change.Connection
	p.SpeechModel = p.SpeechSettings[change.Connection].Model
	if choices[change.Connection].Provider == "openai" {
		pref := p.SpeechSettings[change.Connection]
		p.Voice = pref.Voice
		p.Vibe = pref.Vibe
		p.ConversationSpeed = pref.Speed
	}
	return nil
}
