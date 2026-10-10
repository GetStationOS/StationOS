package persistence

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"stationos.local/stationos/internal/djs"
	"strings"
	"testing"
)

func TestDJCredentialsStayEncryptedAndSurviveEditing(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	r, err := NewDJs(db, bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	service := djs.Service{Repo: r}
	p, err := service.Save(ctx, djs.Profile{Name: "The Archivist", Model: "gpt-6.1-sol", Prompt: "Introduce music", Vibe: djs.ExampleVibe}, djs.KeyChange{Value: "sk-test-not-a-real-key"})
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	db.QueryRow(`SELECT api_key_cipher FROM djs WHERE id=?`, p.ID).Scan(&raw)
	if bytes.Contains(raw, []byte("sk-test")) || !p.HasKey || p.Voice != "cedar" {
		t.Fatal("credential stored in plaintext or metadata status missing")
	}
	key, err := r.APIKey(ctx, p.ID)
	if err != nil || key != "sk-test-not-a-real-key" {
		t.Fatal("credential failed round trip", err)
	}
	p.Name = "Renamed DJ"
	p.Prompt = "Keep these instructions"
	p, err = service.Save(ctx, p, djs.KeyChange{})
	if err != nil || !p.HasKey {
		t.Fatal("blank edit lost credential", err)
	}
	key, _ = r.APIKey(ctx, p.ID)
	if key != "sk-test-not-a-real-key" {
		t.Fatal("blank edit replaced key")
	}
	_, err = service.Save(ctx, p, djs.KeyChange{Value: "new-key", Clear: true})
	if !errors.Is(err, djs.ErrInvalid) {
		t.Fatal("conflicting key changes accepted")
	}
	p, err = service.Save(ctx, p, djs.KeyChange{Value: "replacement-key"})
	if err != nil {
		t.Fatal(err)
	}
	key, _ = r.APIKey(ctx, p.ID)
	if key != "replacement-key" {
		t.Fatal("replacement not saved")
	}
	// Binding ciphertext to identity rejects swapped credential rows.
	_, err = db.Exec(`INSERT INTO djs(id,name,model,api_key_cipher) SELECT 'other','Other','gpt-6.1-sol',api_key_cipher FROM djs WHERE id=?`, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.APIKey(ctx, "other"); err == nil {
		t.Fatal("ciphertext can be transplanted between DJs")
	}
	r.Delete(ctx, "other")
	p, err = service.Save(ctx, p, djs.KeyChange{Clear: true})
	if err != nil || p.HasKey {
		t.Fatal("key removal failed", err)
	}
	if err = (Catalogue{DB: db}).Clear(ctx, ""); err != nil {
		t.Fatal(err)
	}
	p, err = r.Get(ctx, p.ID)
	if err != nil || p.Prompt != "Keep these instructions" {
		t.Fatal("catalogue rebuild lost DJ", err)
	}
	if err = r.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Get(ctx, p.ID); !errors.Is(err, djs.ErrNotFound) {
		t.Fatal("delete left profile")
	}
}
func TestDJSecretFileCreationReuseAndLoss(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "credentials.secrets-key")
	r, err := OpenDJs(ctx, db, path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential file is not private")
	}
	p, err := (djs.Service{Repo: r}).Save(ctx, djs.Profile{Name: "Night DJ", Model: "gpt-6-luna"}, djs.KeyChange{Value: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDJs(ctx, db, path)
	if err != nil {
		t.Fatal(err)
	}
	key, err := reopened.APIKey(ctx, p.ID)
	if err != nil || key != "sk-test" {
		t.Fatal("restart lost credentials", err)
	}
	os.Chmod(path, 0644)
	if _, err = OpenDJs(ctx, db, path); err == nil {
		t.Fatal("insecure credential file accepted")
	}
	os.Remove(path)
	if _, err = OpenDJs(ctx, db, path); err == nil {
		t.Fatal("missing encryption key silently replaced")
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("replacement key created despite existing ciphertext")
	}
}

func TestPastedLongAndWrappedKeys(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	repo, err := NewDJs(db, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	service := djs.Service{Repo: repo}
	p, err := repo.Get(ctx, "default-pop")
	if err != nil {
		t.Fatal(err)
	}
	key := "sk-proj-" + strings.Repeat("a", 350) + "sk-" + strings.Repeat("a", 350)
	for _, input := range []string{key, " \n" + key + "\r\n", key[:150] + "\n \t\u00a0" + key[150:]} {
		saved, err := service.Save(ctx, p, djs.KeyChange{Value: input})
		if err != nil || !saved.HasKey {
			t.Fatal("valid pasted key rejected", err)
		}
		got, err := repo.APIKey(ctx, p.ID)
		if err != nil || got != key {
			t.Fatal("key changed during paste normalization", err)
		}
	}
	for _, input := range []string{"sk-first sk-second", "two tokens", strings.Repeat("x", djs.MaxKeyLength+1), "sk-proj-invalid\x00"} {
		if _, err := service.Save(ctx, p, djs.KeyChange{Value: input}); !errors.Is(err, djs.ErrInvalid) {
			t.Fatal("invalid key accepted")
		}
		got, _ := repo.APIKey(ctx, p.ID)
		if got != key {
			t.Fatal("invalid submission changed saved credential")
		}
	}
	if _, err := service.Save(ctx, p, djs.KeyChange{Value: key, Clear: true}); !errors.Is(err, djs.ErrInvalid) || !strings.Contains(err.Error(), "Untick") {
		t.Fatal("ambiguous conflict message", err)
	}
}
