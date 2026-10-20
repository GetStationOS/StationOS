package inference

import (
	"context"
	"errors"
	"stationos.local/stationos/internal/production"
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
		return r.Chatterbox.Synthesize(ctx, conn, q.Text)
	case "openai":
		if r.OpenAISpeech != nil {
			return r.OpenAISpeech.Synthesize(ctx, q)
		}
	}
	return nil, errors.New("Selected speech provider is unavailable")
}
