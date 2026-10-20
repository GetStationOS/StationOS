package producer_test

import (
	"context"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/producer"
	"testing"
)

type toolVoices struct{}

func (toolVoices) Voices(context.Context) (djs.VoiceCatalog, error) {
	return djs.VoiceCatalog{Connection: "nano", Provider: "chatterbox", DefaultVoice: "default", Voices: []djs.Voice{{ID: "warm", Name: "Warm"}}}, nil
}
func TestAssistantDiscoversAndSelectsChatterboxVoice(t *testing.T) {
	_, _, stations, _, _, repo := fixture(t)
	registry := producer.NewRegistry(producer.Dependencies{Stations: stations, DJs: repo, Voices: toolVoices{}})
	catalog := call(t, registry, "list_voices", map[string]any{}).Data.(djs.VoiceCatalog)
	if catalog.Connection != "nano" || catalog.Voices[0].ID != "warm" {
		t.Fatal(catalog)
	}
	p, err := (djs.Service{Repo: repo}).Save(context.Background(), djs.Profile{Name: "Voice DJ", Voice: "onyx"}, djs.KeyChange{})
	if err != nil {
		t.Fatal(err)
	}
	result := call(t, registry, "save_dj", map[string]any{"id": p.ID, "speech_voice": map[string]string{"connection": "nano", "voice": "warm"}}).Data.(djs.Profile)
	if result.Voice != "onyx" || result.SpeechVoices["nano"] != "warm" {
		t.Fatal(result)
	}
	result = call(t, registry, "save_dj", map[string]any{"id": p.ID, "voice": "cedar"}).Data.(djs.Profile)
	if result.Voice != "cedar" || result.SpeechVoices["nano"] != "warm" {
		t.Fatal("OpenAI update lost Chatterbox", result)
	}
}
