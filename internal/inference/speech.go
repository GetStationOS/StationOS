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
	ctx = djs.WithSpeechConnection(ctx, q.Connection)
	_, conn, _, err := r.selection(ctx, "speech")
	if err != nil {
		return nil, err
	}
	switch conn.Type {
	case "chatterbox":
		voice := q.SpeechVoices[conn.ID]
		if q.Connection != "" {
			voice = q.Voice
		}
		return r.Chatterbox.Synthesize(ctx, conn, q.Text, voice)
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
	id := routes.Speech.Connection
	if djs.SpeechConnection(parent) != "" {
		id = djs.SpeechConnection(parent)
	}
	conn, err := r.Store.Connection(parent, id)
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

func (r *Router) SpeechConnections(ctx context.Context) ([]djs.SpeechEndpoint, error) {
	connections, err := r.Store.Connections(ctx)
	if err != nil {
		return nil, err
	}
	out := []djs.SpeechEndpoint{}
	for _, conn := range connections {
		if conn.Type == "openai" || conn.Type == "chatterbox" {
			endpoint := djs.SpeechEndpoint{ID: conn.ID, Name: conn.Name, Provider: conn.Type}
			if conn.Type == "chatterbox" {
				endpoint.Model = "chatterbox-nano"
			}
			out = append(out, endpoint)
		}
	}
	return out, nil
}
