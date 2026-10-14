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
