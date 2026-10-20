package persistence

import (
	"context"
	"errors"
	"stationos.local/stationos/internal/djs"
	"testing"
)

type voiceSource struct {
	catalog djs.VoiceCatalog
	err     error
}

func (s *voiceSource) Voices(context.Context) (djs.VoiceCatalog, error) { return s.catalog, s.err }
func TestDJSpeechVoicesRetainOpenAIAndOtherConnections(t *testing.T) {
	ctx := context.Background()
	db := database(t)
	repo, _ := NewDJs(db, make([]byte, 32))
	source := &voiceSource{catalog: djs.VoiceCatalog{Connection: "nano", Provider: "chatterbox", DefaultVoice: "default", Voices: []djs.Voice{{ID: "warm", Name: "Warm"}}}}
	service := djs.Service{Repo: repo, Voices: source}
	p, err := service.Save(ctx, djs.Profile{Name: "Voice DJ", Voice: "onyx", SpeechVoiceChange: &djs.VoiceSelection{Connection: "nano", Voice: "warm"}}, djs.KeyChange{})
	if err != nil || p.Voice != "onyx" || p.SpeechVoices["nano"] != "warm" {
		t.Fatal(p, err)
	}
	source.catalog.Connection = "lan"
	p.SpeechVoiceChange = &djs.VoiceSelection{Connection: "lan", Voice: "warm"}
	p, err = service.Save(ctx, p, djs.KeyChange{})
	if err != nil || len(p.SpeechVoices) != 2 {
		t.Fatal(p, err)
	}
	// An OpenAI edit has no speech-selection command and retains both server choices.
	p.Voice = "marin"
	p.SpeechVoiceChange = nil
	p.SpeechVoices = nil
	p, err = service.Save(ctx, p, djs.KeyChange{})
	if err != nil || p.Voice != "marin" || p.SpeechVoices["nano"] != "warm" || p.SpeechVoices["lan"] != "warm" {
		t.Fatal(p, err)
	}
	// Unavailable saved choices can be retained while unrelated fields are edited.
	source.err = errors.New("offline")
	source.catalog.Voices = nil
	p.SpeechVoiceChange = &djs.VoiceSelection{Connection: "lan", Voice: "warm"}
	p.Name = "Retained DJ"
	if _, err = service.Save(ctx, p, djs.KeyChange{}); err != nil {
		t.Fatal("offline save lost voice", err)
	}
	p.SpeechVoiceChange.Voice = "invented"
	if _, err = service.Save(ctx, p, djs.KeyChange{}); !errors.Is(err, djs.ErrInvalid) {
		t.Fatal("unverified voice accepted", err)
	}
	source.err = nil
	if _, err = service.Save(ctx, p, djs.KeyChange{}); !errors.Is(err, djs.ErrInvalid) {
		t.Fatal("unknown voice accepted", err)
	}
	p.SpeechVoiceChange = &djs.VoiceSelection{Connection: "nano", Voice: "warm"}
	if _, err = service.Save(ctx, p, djs.KeyChange{}); !errors.Is(err, djs.ErrInvalid) {
		t.Fatal("stale provider selection accepted", err)
	}
	p.SpeechVoiceChange = &djs.VoiceSelection{Connection: "lan", Voice: ""}
	p, err = service.Save(ctx, p, djs.KeyChange{})
	if err != nil || p.SpeechVoices["lan"] != "" || p.SpeechVoices["nano"] != "warm" || p.Voice != "marin" {
		t.Fatal("default reset lost other choices", p, err)
	}
	// List projections must include the same choices used for actual playback.
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range list {
		if item.ID == p.ID {
			found = item.SpeechVoices["nano"] == "warm"
		}
	}
	if !found {
		t.Fatal("list lost voice selections")
	}
}
