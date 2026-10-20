package persistence

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/inference"
)

func TestMigrationInheritsExistingGlobalSpeechSettings(t *testing.T) {
	for _, provider := range []string{"openai", "chatterbox"} {
		t.Run(provider, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "upgrade.db")
			db, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			// Recreate the frozen v38 DJ layout, retaining configured speech data.
			route := `{"Speech":{"Connection":"speech"}}`
			if _, err = db.Exec(`INSERT INTO ai_connections(id,name,provider_type,address) VALUES('speech','Existing speech',?,'http://server:8000')`, provider); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`UPDATE producer_settings SET provider_routes=?,speech_model='tts-1-hd' WHERE id=1`, route); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`UPDATE djs SET voice='onyx',conversation_speed=1.35,vibe='Saved direction',speech_voices='{"speech":"warm","other":"deep"}'`); err != nil {
				t.Fatal(err)
			}
			for _, column := range []string{"speech_settings", "speech_model", "speech_connection"} {
				if _, err = db.Exec(`ALTER TABLE djs DROP COLUMN ` + column); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = db.Exec(`DELETE FROM schema_migrations WHERE version=39`); err != nil {
				t.Fatal(err)
			}
			db.Close()
			db, err = Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			repo, _ := NewDJs(db, make([]byte, 32))
			p, err := repo.Get(ctx, "default-pop")
			if err != nil {
				t.Fatal(err)
			}
			if p.SpeechConnection != "speech" || p.Voice != "onyx" || p.ConversationSpeed != 1.35 || p.Vibe != "Saved direction" || p.SpeechVoices["other"] != "deep" {
				t.Fatal(p)
			}
			pref := p.ActiveSpeech()
			if provider == "chatterbox" {
				if pref.Model != "chatterbox-nano" || pref.Voice != "warm" {
					t.Fatal(pref)
				}
			} else if pref.Model != "tts-1-hd" || pref.Voice != "onyx" || pref.Speed != 1.35 || pref.Vibe != "Saved direction" {
				t.Fatal(pref)
			}
		})
	}
}
func TestDJSpeechEndpointPreferencesAndReadiness(t *testing.T) {
	ctx := context.Background()
	db := database(t)
	repo, _ := NewDJs(db, make([]byte, 32))
	store := NewProducer(repo)
	conn := ai.Connection{ID: "cloud", Name: "Other OpenAI", Type: "openai", Address: "https://api.openai.com/v1", ContextTokens: 8192, OutputTokens: 2048}
	if err := store.SaveConnection(ctx, conn, ai.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	service := djs.Service{Repo: repo, Voices: inference.New(store, nil)}
	p, err := service.Save(ctx, djs.Profile{Name: "Own speech", SpeechChange: &djs.SpeechSelection{Connection: "cloud", Settings: djs.SpeechPreference{Model: "tts-1", Voice: "onyx", Speed: 1.5, Vibe: "Warm"}}}, djs.KeyChange{})
	if err != nil {
		t.Fatal(err)
	}
	if p.HasKey {
		t.Fatal("unconfigured credential shown as ready")
	}
	if err = store.SaveConnection(ctx, conn, ai.KeyChange{Value: "secret"}); err != nil {
		t.Fatal(err)
	}
	p, _ = repo.Get(ctx, p.ID)
	if !p.HasKey {
		t.Fatal("selected connection credential ignored")
	}
	p.SpeechChange = &djs.SpeechSelection{Connection: "openai", Settings: djs.SpeechPreference{Model: "gpt-4o-mini-tts", Voice: "marin", Speed: 1}}
	p, err = service.Save(ctx, p, djs.KeyChange{})
	if err != nil {
		t.Fatal(err)
	}
	if p.HasKey {
		t.Fatal("credential leaked across endpoints")
	}
	if pref := p.SpeechSettings["cloud"]; pref.Model != "tts-1" || pref.Voice != "onyx" || pref.Speed != 1.5 {
		t.Fatal("previous endpoint settings lost", pref)
	}
	raw, _ := json.Marshal(p)
	if string(raw) == "" {
		t.Fatal("invalid profile")
	}
	p.SpeechChange = &djs.SpeechSelection{Connection: "cloud"}
	p, err = service.Save(ctx, p, djs.KeyChange{})
	if err != nil {
		t.Fatal(err)
	}
	if pref := p.ActiveSpeech(); pref.Model != "tts-1" || pref.Voice != "onyx" || pref.Speed != 1.5 || pref.Vibe != "Warm" {
		t.Fatal("endpoint-only switch did not restore saved settings", pref)
	}
	p.SpeechChange = &djs.SpeechSelection{Connection: "missing", Settings: djs.SpeechPreference{Model: "tts-1", Voice: "onyx", Speed: 1}}
	if _, err = service.Save(ctx, p, djs.KeyChange{}); err == nil {
		t.Fatal("missing speech endpoint accepted")
	}
}
