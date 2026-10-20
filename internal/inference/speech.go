package inference

import (
	"context"
	"errors"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"time"
)

// Synthesize routes only explicitly selected speech providers. OpenAI retains its
// original production adapter, including voices, direction, speed and retries.
func (r *Router) Synthesize(ctx context.Context, q production.SpeechRequest) ([]byte, error) {
	_, conn, _, err := r.selection(ctx, "speech")
	if err != nil {
		return nil, err
	}
	switch conn.Type {
	case "chatterbox":
		return r.Chatterbox.Synthesize(ctx, conn, q.Text, q.SpeechVoices[conn.ID])
	case "openai":
		if r.OpenAISpeech != nil {
			return r.OpenAISpeech.Synthesize(ctx, q)
		}
	}
	return nil, errors.New("Selected speech provider is unavailable")
}

// Voices discovers only the selected speech connection. OpenAI keeps its built-in catalogue.
func (r *Router) Voices(parent context.Context) (djs.VoiceCatalog, error) {
	routes, err := r.routes(parent)
	if err != nil {
		return djs.VoiceCatalog{}, err
	}
	conn, err := r.Store.Connection(parent, routes.Speech.Connection)
	if err != nil {
		return djs.VoiceCatalog{}, err
	}
	out := djs.VoiceCatalog{Connection: conn.ID, Provider: conn.Type}
	if conn.Type == "openai" {
		out.DefaultVoice = djs.DefaultVoice
		out.Voices = djs.Voices()
		return out, nil
	}
	if conn.Type != "chatterbox" {
		return out, errors.New("The selected provider does not support speech voices")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	catalog, err := r.Chatterbox.Voices(ctx, conn)
	out.DefaultVoice = catalog.DefaultVoice
	out.Voices = catalog.Voices
	return out, err
}
