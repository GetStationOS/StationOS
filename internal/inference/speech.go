package inference

import (
	"context"
	"errors"
	"log/slog"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"time"
)

// Synthesize routes only explicitly selected speech providers. OpenAI retains its
// original production adapter, including voices, direction, speed and retries.
func (r *Router) Synthesize(ctx context.Context, q production.SpeechRequest) (audio []byte, resultErr error) {
	ctx = djs.WithSpeechConnection(ctx, q.Connection)
	_, conn, key, err := r.selection(ctx, "speech")
	if err != nil {
		return nil, err
	}
	meta := production.SpeechProvenance{Provider: conn.Type, ProviderName: conn.Name, Connection: conn.ID, Model: q.Model, Voice: q.Voice}
	if conn.Type == "chatterbox" {
		if meta.Model == "" || meta.Model == "chatterbox-nano" || (q.Connection == "" && (meta.Model == djs.DefaultSpeechModel || meta.Model == "tts-1" || meta.Model == "tts-1-hd")) {
			meta.Model = "nano"
		}
		if q.Connection == "" {
			meta.Voice = q.SpeechVoices[conn.ID]
		}
		if meta.Voice == "" {
			meta.Voice = "(server default)"
		}
	}
	production.ReportSpeechProvenance(ctx, meta)
	logger := r.Logger
	if logger == nil {
		logger = slog.Default()
	}
	started := time.Now()
	defer func() {
		attrs := []any{"speech_provider", meta.Provider, "speech_connection", meta.Connection, "speech_model", meta.Model, "speech_voice", meta.Voice, "elapsed", time.Since(started)}
		if resultErr != nil {
			attrs = append(attrs, "error", resultErr)
			logger.WarnContext(ctx, "DJ speech synthesis failed", attrs...)
		} else {
			logger.InfoContext(ctx, "DJ speech generated", attrs...)
		}
	}()
	q.Connection = conn.ID
	q.ProviderName = conn.Name
	switch conn.Type {
	case "elevenlabs":
		return r.Local.elevenSpeech(ctx, conn, key, q)
	case "chatterbox":
		voice := meta.Voice
		if voice == "(server default)" {
			voice = ""
		}
		return r.Chatterbox.Synthesize(ctx, conn, q.Text, voice, meta.Model)
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
	if conn.Type == "elevenlabs" {
		key, err := r.Store.Credential(parent, conn.ID)
		if err != nil {
			return out, err
		}
		return r.Local.elevenVoices(parent, conn, key)
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
		if conn.Type == "openai" || conn.Type == "chatterbox" || conn.Type == "elevenlabs" {
			endpoint := djs.SpeechEndpoint{ID: conn.ID, Name: conn.Name, Provider: conn.Type}
			if conn.Type == "chatterbox" {
				endpoint.Model = "chatterbox-nano"
			}
			out = append(out, endpoint)
		}
	}
	return out, nil
}

func (r *Router) SpeechModelChoices(parent context.Context, id string) ([]djs.SpeechModel, error) {
	conn, err := r.Store.Connection(parent, id)
	if err != nil {
		return nil, err
	}
	if conn.Type != "chatterbox" {
		return nil, errors.New("Model choices are unavailable for this provider")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	return r.Chatterbox.Models(ctx, conn)
}
