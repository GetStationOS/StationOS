package inference

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/production"
	"strings"
	"testing"
)

type receiptSpeech struct{ fail bool }

func (s receiptSpeech) Synthesize(context.Context, production.SpeechRequest) ([]byte, error) {
	if s.fail {
		return nil, errors.New("generation failed")
	}
	return []byte("audio"), nil
}
func TestSpeechReceiptsAndLogsUseSelectedRequest(t *testing.T) {
	for _, fail := range []bool{false, true} {
		store := &testStore{connections: map[string]ai.Connection{"cloud": {ID: "cloud", Name: "My OpenAI", Type: "openai"}}, keys: map[string]string{"cloud": "private-key"}, routes: ai.Routes{Speech: ai.Selection{Connection: "cloud"}}}
		r := New(store, nil)
		r.OpenAISpeech = receiptSpeech{fail: fail}
		var logs bytes.Buffer
		r.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
		var meta production.SpeechProvenance
		ctx := production.WithSpeechProvenance(context.Background(), func(value production.SpeechProvenance) { meta = value })
		_, err := r.Synthesize(ctx, production.SpeechRequest{Text: "private spoken text", Model: "tts-1", Voice: "marin"})
		if (err != nil) != fail || meta != (production.SpeechProvenance{Provider: "openai", ProviderName: "My OpenAI", Connection: "cloud", Model: "tts-1", Voice: "marin"}) {
			t.Fatal(meta, err)
		}
		for _, wanted := range []string{`"speech_provider":"openai"`, `"speech_model":"tts-1"`, `"speech_voice":"marin"`} {
			if !strings.Contains(logs.String(), wanted) {
				t.Fatal(logs.String())
			}
		}
		if strings.Contains(logs.String(), "private-key") || strings.Contains(logs.String(), "private spoken text") {
			t.Fatal("private content logged")
		}
		// Receipt values remain the original request even when routing preferences change.
		store.routes.Speech.Connection = "elsewhere"
		if meta.Connection != "cloud" {
			t.Fatal(meta)
		}
	}
}
