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
	p.ConversationSpeed = 1.15
	p, err = service.Save(ctx, p, djs.KeyChange{})
	if err != nil || !p.HasKey || p.ConversationSpeed != 1.15 {
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

func TestBackgroundAndPortraitPreserveCredentialsAndReachSchedule(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	r, err := NewDJs(db, bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Get(ctx, "default-pop")
	if err != nil || p.Background == "" || p.PortraitURL != "/static/djs/alex-bright.jpg" {
		t.Fatal("starter biography missing", p, err)
	}
	p.ConversationSpeed = 1.25
	p.Background = "Fictional record-shop curator"
	if err = r.Save(ctx, p, djs.KeyChange{Value: "sk-test"}); err != nil {
		t.Fatal(err)
	}
	p.Background = "Changed fictional background"
	p.PortraitURL = "https://example.com/portrait.jpg"
	if err = r.Save(ctx, p, djs.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, p.ID)
	if err != nil || got.Background != p.Background || got.PortraitURL != p.PortraitURL || got.ConversationSpeed != 1.25 {
		t.Fatal(got, err)
	}
	key, err := r.APIKey(ctx, p.ID)
	if err != nil || key != "sk-test" {
		t.Fatal("profile edit lost key", err)
	}
	_, err = db.Exec(`INSERT INTO stations(id,name,description,timezone,locale,stream_slug) VALUES('s','Station','','UTC','en','station'); INSERT INTO playlists(id,station_id,name,description) VALUES('p','s','Pool',''); INSERT INTO station_shows(id,station_id,name,weekday,start_minute,end_minute,dj_id,playlist_id) VALUES('show','s','Show',0,0,60,'default-pop','p')`)
	if err != nil {
		t.Fatal(err)
	}
	show, err := (Schedule{DB: db}).Get(ctx, "s", "show")
	if err != nil || show.DJ.Background != p.Background || show.DJ.PortraitURL != p.PortraitURL || show.DJ.ConversationSpeed != 1.25 {
		t.Fatal("schedule lost profile", show, err)
	}
	if err = migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	got, _ = r.Get(ctx, p.ID)
	if got.Background != p.Background {
		t.Fatal("startup overwrote custom profile")
	}
}
