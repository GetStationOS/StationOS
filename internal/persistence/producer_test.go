package persistence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/producer"
)

func TestProducerCredentialEncryptionAndRecovery(t *testing.T) {
	ctx := context.Background()
	db, e := Open(ctx, ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	dj, e := NewDJs(db, make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	p := NewProducer(dj)
	if e = p.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "private-key"}); e != nil {
		t.Fatal(e)
	}
	var ciphertext []byte
	if e = db.QueryRow(`SELECT api_key_cipher FROM producer_settings`).Scan(&ciphertext); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(ciphertext), "private-key") {
		t.Fatal("plaintext key")
	}
	if e = p.SaveSettings(ctx, "other-model", producer.KeyChange{}); e != nil {
		t.Fatal(e)
	}
	key, e := p.APIKey(ctx)
	if e != nil || key != "private-key" {
		t.Fatal(key, e)
	}
	wrong, _ := NewDJs(db, []byte("12345678901234567890123456789012"))
	if _, e = NewProducer(wrong).APIKey(ctx); e == nil {
		t.Fatal("accepted wrong key")
	}
	path := filepath.Join(t.TempDir(), "missing.secrets-key")
	if _, e = OpenDJs(ctx, db, path); e == nil {
		t.Fatal("replaced missing key when only assistant had a credential")
	}
	if _, e = os.Stat(path); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("key file created", e)
	}
	if e = p.SaveSettings(ctx, "other-model", producer.KeyChange{Clear: true}); e != nil {
		t.Fatal(e)
	}
	s, _ := p.Settings(ctx)
	if s.HasKey {
		t.Fatal("key not cleared")
	}
}
func TestProducerRestartDoesNotReplayPendingActions(t *testing.T) {
	ctx := context.Background()
	db, e := Open(ctx, ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	dj, _ := NewDJs(db, make([]byte, 32))
	p := NewProducer(dj)
	id := producer.ID()
	_ = p.Create(ctx, producer.Conversation{ID: id, Title: "Test"})
	request := producer.ID()
	if e = p.Begin(ctx, id, request, "Make a station"); e != nil {
		t.Fatal(e)
	}
	_ = p.Append(ctx, id, producer.Message{Role: "action", Text: "Saved station", URL: "/producer"})
	if e = p.Interrupt(ctx); e != nil {
		t.Fatal(e)
	}
	c, e := p.Get(ctx, id)
	if e != nil || c.Status != "failed" || len(c.Messages) != 3 {
		t.Fatal(c, e)
	}
	if e = p.Begin(ctx, id, request, "Make a station"); !errors.Is(e, producer.ErrDuplicate) {
		t.Fatal(e)
	}
	if e = p.Begin(ctx, id, producer.ID(), "Inspect current state"); e != nil {
		t.Fatal(e)
	}
	c, _ = p.Get(ctx, id)
	if !strings.Contains(string(c.Transcript), "Saved station") {
		t.Fatal("lost recovery context")
	}
}

func TestProducerPastedKeyNormalization(t *testing.T) {
	ctx := context.Background()
	db, e := Open(ctx, ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	dj, e := NewDJs(db, make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	p := NewProducer(dj)
	const expected = "sk-proj-example-wrapped-credential"
	for _, pasted := range []string{"  sk-proj-example-wrapped-credential\n", "sk-proj-example-\nwrapped-credential", "sk-proj-example-\twrapped-credential", "sk-proj-example-\u00a0wrapped-credential"} {
		if e = p.SaveSettings(ctx, "test-model", producer.KeyChange{Value: pasted}); e != nil {
			t.Fatal("wrapped key rejected", e)
		}
		got, e := p.APIKey(ctx)
		if e != nil || got != expected {
			t.Fatal("normalized key was not saved correctly", e)
		}
	}
	for _, invalid := range []string{"sk-first-key sk-second-key", "key: sk-proj-example-wrapped-credential", "unrecognized credential", "sk-proj-example\x00wrapped-credential"} {
		if e = p.SaveSettings(ctx, "test-model", producer.KeyChange{Value: invalid}); e == nil {
			t.Fatal("invalid key accepted")
		}
		got, e := p.APIKey(ctx)
		if e != nil || got != expected {
			t.Fatal("invalid paste changed saved credential", e)
		}
	}
	if e = p.SaveSettings(ctx, "test-model", producer.KeyChange{Value: " ", Clear: false}); e != nil {
		t.Fatal(e)
	}
	got, e := p.APIKey(ctx)
	if e != nil || got != expected {
		t.Fatal("blank replaced saved credential", e)
	}
}

func TestLegacyRecoveryKeepsOriginalBriefBeyondRecentActions(t *testing.T) {
	ctx := context.Background()
	db, e := Open(ctx, ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	dj, _ := NewDJs(db, make([]byte, 32))
	store := NewProducer(dj)
	id := producer.ID()
	_ = store.Create(ctx, producer.Conversation{ID: id, Title: "Legacy"})
	brief := "Friday and Sunday shows at 22:00, with four presenters and a default playlist."
	if e = store.Begin(ctx, id, producer.ID(), brief); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 150; i++ {
		_ = store.Append(ctx, id, producer.Message{Role: "action", Text: "Completed an earlier action"})
	}
	if e = store.Finish(ctx, id, "failed", []byte(`[]`), producer.Message{Role: "assistant", Text: "Old action limit reached"}); e != nil {
		t.Fatal(e)
	}
	c, e := store.Get(ctx, id)
	if e != nil || len(c.Requests) != 1 || c.Requests[0] != brief {
		t.Fatal("original request was not retrieved", e)
	}
	if e = store.Begin(ctx, id, producer.ID(), "Continue"); e != nil {
		t.Fatal(e)
	}
	c, _ = store.Get(ctx, id)
	if !strings.Contains(string(c.Transcript), brief) {
		t.Fatal("legacy recovery lost schedule requirements")
	}
}
func TestCheckpointAndTaskSurviveRestart(t *testing.T) {
	ctx := context.Background()
	db, e := Open(ctx, ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	dj, _ := NewDJs(db, make([]byte, 32))
	store := NewProducer(dj)
	id := producer.ID()
	_ = store.Create(ctx, producer.Conversation{ID: id, Title: "Task"})
	_ = store.Begin(ctx, id, producer.ID(), "Create the full station")
	task := producer.Task{Brief: "Create the full station", Completed: []string{"Station created with ID saved-station"}, Remaining: []string{"Schedule Friday show", "Assign default playlist"}}
	raw := []byte(`[{"role":"user","content":"Create the full station"},{"role":"assistant","content":"Saved station ID: saved-station"}]`)
	if e = store.Checkpoint(ctx, id, raw, task); e != nil {
		t.Fatal(e)
	}
	if e = store.Interrupt(ctx); e != nil {
		t.Fatal(e)
	}
	c, e := store.Get(ctx, id)
	if e != nil || c.Status != "failed" || len(c.Task.Remaining) != 2 || !strings.Contains(string(c.Transcript), "saved-station") {
		t.Fatal("checkpoint was discarded", c, e)
	}
}
func TestRoleModelsAndSharedKeyAreIndependent(t *testing.T) {
	ctx := context.Background()
	db, e := Open(ctx, ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	dj, _ := NewDJs(db, make([]byte, 32))
	store := NewProducer(dj)
	settings := producer.Settings{Model: "producer-model", CurationModel: "curation-model", SpeechModel: "speech-model", AudienceModel: "review-model", ImageModel: "art-model"}
	if e = store.Configure(ctx, settings, producer.KeyChange{Value: "shared-test-key"}); e != nil {
		t.Fatal(e)
	}
	for role, want := range map[string]string{"producer": "producer-model", "curation": "curation-model", "speech": "speech-model", "audience": "review-model"} {
		got, e := store.Model(ctx, role)
		if e != nil || got != want {
			t.Fatal(role, got, e)
		}
	}
	keys := ProducerKeys{Producer: store}
	for _, id := range []string{"dj-one", "dj-two", "audience"} {
		got, e := keys.APIKey(ctx, id)
		if e != nil || got != "shared-test-key" {
			t.Fatal("services did not share the key", e)
		}
	}
	if e = store.SaveSettings(ctx, "replacement-producer", producer.KeyChange{}); e != nil {
		t.Fatal(e)
	}
	got, _ := store.Settings(ctx)
	if got.SpeechModel != settings.SpeechModel || got.AudienceModel != settings.AudienceModel || got.CurationModel != settings.CurationModel || got.ImageModel != settings.ImageModel {
		t.Fatal("producer edit overwrote role models", got)
	}
}
func TestUnambiguousLegacyDJKeyMigratesWithoutDuplicatingSecrets(t *testing.T) {
	ctx := context.Background()
	db, e := Open(ctx, ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	dj, _ := NewDJs(db, make([]byte, 32))
	store := NewProducer(dj)
	for _, id := range []string{"default-pop", "default-jazz"} {
		nonce := make([]byte, dj.secret.NonceSize())
		encrypted := dj.secret.Seal(nonce, nonce, []byte("sk-legacy-key"), []byte(id))
		if _, e = db.Exec(`UPDATE djs SET api_key_cipher=? WHERE id=?`, encrypted, id); e != nil {
			t.Fatal(e)
		}
	}
	if e = store.AdoptLegacyKey(ctx); e != nil {
		t.Fatal(e)
	}
	got, e := store.APIKey(ctx)
	if e != nil || got != "sk-legacy-key" {
		t.Fatal("legacy key was not migrated", e)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM djs WHERE length(api_key_cipher)>0`).Scan(&count); e != nil || count != 0 {
		t.Fatal("DJ credential copies remain", count, e)
	}
}

func TestWritingModelAndConnectionAreIndependent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dj, err := NewDJs(db, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	p := NewProducer(dj)
	s, err := p.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.CurationModel != "gpt-6-luna" {
		t.Fatal("new installations must use Decisions for OpenAI planning", s)
	}
	s.CurationModel = "gpt-6-luna"
	s.WritingModel = "independent-writer"
	conn := ai.Connection{ID: "writer", Name: "Writing", Type: "ollama", Address: "http://localhost:11434", ContextTokens: 8192, OutputTokens: 2048}
	if err = p.SaveConnection(ctx, conn, ai.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	s.Routes.Writing.Connection = "writer"
	if err = p.Configure(ctx, s, producer.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	if err = p.DeleteConnection(ctx, "writer"); err == nil {
		t.Fatal("removed endpoint selected only for writing")
	}
	got, err := p.Settings(ctx)
	if err != nil || got.Routes.Writing.Connection != "writer" || got.Routes.Curation.Connection == "writer" {
		t.Fatal(got, err)
	}
	writing, err := p.Model(ctx, "writing")
	if err != nil || writing != "independent-writer" {
		t.Fatal(writing, err)
	}
	planning, err := p.Model(ctx, "curation")
	if err != nil || planning != "gpt-6-luna" {
		t.Fatal(planning, err)
	}
}
