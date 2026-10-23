package openai

import (
	"context"
	"encoding/json"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/schedule"
)

// Speech never needs the eligibility pool, queue IDs or planning conversation.
// Keep the existing episode_session name for saved editorial prompts.
type speechCheckpoint struct {
	Theme             string   `json:"Theme,omitempty"`
	Notes             string   `json:"Notes,omitempty"`
	RecentAiredSpeech []string `json:"recent_aired_speech,omitempty"`
	Warning           string   `json:"warning,omitempty"`
}

func (p Producer) speechContext(ctx context.Context, show schedule.Occurrence) speechCheckpoint {
	if p.Session == nil {
		return speechCheckpoint{}
	}
	var raw string
	var err error
	if source, ok := p.Session.(interface {
		SpeechContext(context.Context, schedule.Occurrence) (string, error)
	}); ok {
		raw, err = source.SpeechContext(ctx, show)
	} else {
		raw, err = p.Session.Context(ctx, show)
	}
	if err != nil {
		return speechCheckpoint{Warning: "Earlier airtime is unknown; do not claim earlier recordings aired."}
	}
	if raw == "" {
		return speechCheckpoint{}
	}
	var value speechCheckpoint
	if json.Unmarshal([]byte(raw), &value) != nil {
		return speechCheckpoint{Warning: "Earlier airtime is unknown; do not claim earlier recordings aired."}
	}
	value.RecentAiredSpeech = value.RecentAiredSpeech[max(0, len(value.RecentAiredSpeech)-6):]
	return value
}

type speechRecording struct {
	Title      string
	Performers string
	Composer   string `json:",omitempty"`
	Album      string `json:",omitempty"`
	Conductor  string `json:",omitempty"`
}

func speechSongDetails(a content.AudioItem) speechRecording {
	return speechRecording{Title: short(a.Title, 300), Performers: short(a.Artist, 400), Composer: short(a.Composer, 400), Album: short(a.Album, 200), Conductor: short(a.Conductor, 400)}
}
