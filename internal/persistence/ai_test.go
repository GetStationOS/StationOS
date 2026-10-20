package persistence

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/producer"
)

func TestProviderCredentialsIsolationAndNoKeyLocalSettings(t *testing.T) {
	ctx := context.Background()
	db, e := Open(ctx, ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	d, _ := NewDJs(db, make([]byte, 32))
	p := NewProducer(d)
	c := ai.Connection{ID: "local", Name: "My Mac", Type: "ollama", Address: "http://127.0.0.1:11434", ContextTokens: 16384, OutputTokens: 2048}
	if e = p.SaveConnection(ctx, c, ai.KeyChange{}); e != nil {
		t.Fatal(e)
	}
	settings, _ := p.Settings(ctx)
	settings.Model = "gemma4:12b"
	settings.CurationModel = "gemma4:12b"
	settings.AudienceModel = "gemma4:12b"
	settings.Routes.Assistant.Connection = "local"
	settings.Routes.Curation.Connection = "local"
	settings.Routes.Audience.Connection = "local"
	if e = p.Configure(ctx, settings, producer.KeyChange{}); e != nil {
		t.Fatal(e)
	}
	s, e := p.Settings(ctx)
	if e != nil || s.HasKey || !s.Enabled() {
		t.Fatal(s, e)
	}
	if key, e := p.Credential(ctx, "local"); e != nil || key != "" {
		t.Fatal("local key inherited", e)
	}
	if e = p.SaveConnection(ctx, c, ai.KeyChange{Value: "local-secret"}); e != nil {
		t.Fatal(e)
	}
	if e = p.Configure(ctx, settings, producer.KeyChange{Value: "openai-secret"}); e != nil {
		t.Fatal(e)
	}
	if key, _ := p.Credential(ctx, "local"); key != "local-secret" {
		t.Fatal("credential crossed connections")
	}
	var cipher []byte
	_ = db.QueryRow(`SELECT api_key_cipher FROM ai_connections WHERE id='local'`).Scan(&cipher)
	if strings.Contains(string(cipher), "local-secret") {
		t.Fatal("plaintext secret")
	}
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("settings disclose secrets")
	}
	c.ID = "other"
	if e = p.SaveConnection(ctx, c, ai.KeyChange{}); e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`UPDATE ai_connections SET api_key_cipher=(SELECT api_key_cipher FROM ai_connections WHERE id='local') WHERE id='other'`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Credential(ctx, "other"); e == nil {
		t.Fatal("credential accepted with wrong connection identity")
	}
	if e = p.DeleteConnection(ctx, "local"); e == nil {
		t.Fatal("deleted selected connection")
	}
	if e = p.SaveConnection(ctx, c, ai.KeyChange{Clear: true}); e != nil {
		t.Fatal(e)
	}
	if e = p.Configure(ctx, settings, producer.KeyChange{Clear: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = OpenDJs(ctx, db, filepath.Join(t.TempDir(), "missing.secrets-key")); e == nil {
		t.Fatal("forgot private provider secret while protecting sidecar")
	}
}
func TestProviderRouteValidationAndSpeechIsolation(t *testing.T) {
	ctx := context.Background()
	db, e := Open(ctx, ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	d, _ := NewDJs(db, make([]byte, 32))
	p := NewProducer(d)
	c := ai.Connection{ID: "speech", Name: "Speech account", Type: "openai", Address: "https://api.openai.com/v1", ContextTokens: 8192, OutputTokens: 2048}
	if e = p.SaveConnection(ctx, c, ai.KeyChange{Value: "speech-secret"}); e != nil {
		t.Fatal(e)
	}
	s, _ := p.Settings(ctx)
	s.Routes.Speech.Connection = "speech"
	if e = p.Configure(ctx, s, producer.KeyChange{Value: "image-secret"}); e != nil {
		t.Fatal(e)
	}
	if key, _ := (ProducerKeys{Producer: p}).APIKey(ctx, "dj"); key != "speech-secret" {
		t.Fatal("wrong speech key")
	}
	if key, _ := p.APIKey(ctx); key != "image-secret" {
		t.Fatal("wrong artwork key")
	}
	s.Routes.Assistant.Connection = "missing"
	if e = p.Configure(ctx, s, producer.KeyChange{}); e == nil {
		t.Fatal("saved missing provider")
	}
}

func TestChatterboxSpeechRouteAndReadinessWithoutKey(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d, _ := NewDJs(db, make([]byte, 32))
	p := NewProducer(d)
	c := ai.Connection{ID: "nano", Name: "Chatterbox", Type: "chatterbox", Address: "http://chatterbox:8000/", ContextTokens: 8192, OutputTokens: 2048}
	if err = p.SaveConnection(ctx, c, ai.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	saved, err := p.Connection(ctx, "nano")
	if err != nil || saved.Address != "http://chatterbox:8000" || saved.HasKey {
		t.Fatal(saved, err)
	}
	settings, _ := p.Settings(ctx)
	settings.Routes.Speech.Connection = "nano"
	if err = p.Configure(ctx, settings, producer.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	var ready bool
	if err = db.QueryRow("SELECT " + speechReadySQL).Scan(&ready); err != nil || !ready {
		t.Fatal("Nano requires no key", ready, err)
	}
	for _, role := range []string{"assistant", "curation", "audience", "image"} {
		bad := settings
		switch role {
		case "assistant":
			bad.Routes.Assistant.Connection = "nano"
		case "curation":
			bad.Routes.Curation.Connection = "nano"
		case "audience":
			bad.Routes.Audience.Connection = "nano"
		case "image":
			bad.Routes.Image.Connection = "nano"
		}
		if err = p.Configure(ctx, bad, producer.KeyChange{}); err == nil {
			t.Fatal("speech-only provider accepted for", role)
		}
	}
	settings.Routes.Speech.Connection = "openai"
	if err = p.Configure(ctx, settings, producer.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT " + speechReadySQL).Scan(&ready); err != nil || ready {
		t.Fatal("OpenAI readiness changed", ready, err)
	}
	if err = p.Configure(ctx, settings, producer.KeyChange{Value: "keep-openai"}); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT " + speechReadySQL).Scan(&ready); err != nil || !ready {
		t.Fatal(ready, err)
	}
}
