package persistence

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/producer"
	"strings"
	"testing"
)

func TestDJsUseOneSharedCredentialAndEditsCannotChangeIt(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	r, e := NewDJs(db, bytes.Repeat([]byte{1}, 32))
	if e != nil {
		t.Fatal(e)
	}
	shared := NewProducer(r)
	if e = shared.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "sk-shared-test"}); e != nil {
		t.Fatal(e)
	}
	service := djs.Service{Repo: r}
	p, e := service.Save(ctx, djs.Profile{Name: "The Archivist", Model: "chosen-model", Prompt: "Introduce music"}, djs.KeyChange{})
	if e != nil {
		t.Fatal(e)
	}
	other, e := service.Save(ctx, djs.Profile{Name: "Night DJ", Model: "different-model"}, djs.KeyChange{})
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{p.ID, other.ID} {
		key, e := r.APIKey(ctx, id)
		if e != nil || key != "sk-shared-test" {
			t.Fatal("DJ did not use shared key", e)
		}
		var cipher []byte
		_ = db.QueryRow(`SELECT api_key_cipher FROM djs WHERE id=?`, id).Scan(&cipher)
		if len(cipher) > 0 {
			t.Fatal("key duplicated in DJ row")
		}
	}
	p.Name = "Renamed DJ"
	p.Prompt = "Keep these instructions"
	p.ConversationSpeed = 1.15
	p, e = service.Save(ctx, p, djs.KeyChange{})
	if e != nil || !p.HasKey || p.ConversationSpeed != 1.15 {
		t.Fatal("profile edit lost shared setup", e)
	}
	if _, e = service.Save(ctx, p, djs.KeyChange{Value: "another-key"}); !errors.Is(e, djs.ErrInvalid) {
		t.Fatal("DJ accepted its own key", e)
	}
	if e = shared.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "replacement-key"}); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{p.ID, other.ID} {
		key, e := r.APIKey(ctx, id)
		if e != nil || key != "replacement-key" {
			t.Fatal("shared replacement not used", e)
		}
	}
	if e = shared.SaveSettings(ctx, "test-model", producer.KeyChange{Clear: true}); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{p.ID, other.ID} {
		profile, e := r.Get(ctx, id)
		if e != nil || profile.HasKey {
			t.Fatal("shared removal not reflected", e)
		}
	}
	if e = (Catalogue{DB: db}).Clear(ctx, ""); e != nil {
		t.Fatal(e)
	}
	p, e = r.Get(ctx, p.ID)
	if e != nil || p.Prompt != "Keep these instructions" {
		t.Fatal("catalogue rebuild lost profile", e)
	}
	if e = r.Delete(ctx, p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Get(ctx, p.ID); !errors.Is(e, djs.ErrNotFound) {
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
	if err = NewProducer(r).SaveSettings(ctx, "test-model", producer.KeyChange{Value: "sk-test"}); err != nil {
		t.Fatal(err)
	}
	p, err := (djs.Service{Repo: r}).Save(ctx, djs.Profile{Name: "Night DJ", Model: "gpt-6-luna"}, djs.KeyChange{})
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
	shared := NewProducer(repo)
	p, err := repo.Get(ctx, "default-pop")
	if err != nil {
		t.Fatal(err)
	}
	key := "sk-proj-" + strings.Repeat("a", 350) + "sk-" + strings.Repeat("a", 350)
	for _, input := range []string{key, " \n" + key + "\r\n", key[:150] + "\n \t\u00a0" + key[150:]} {
		err := shared.SaveSettings(ctx, "test-model", producer.KeyChange{Value: input})
		if err != nil {
			t.Fatal("valid pasted key rejected", err)
		}
		got, err := repo.APIKey(ctx, p.ID)
		if err != nil || got != key {
			t.Fatal("key changed during paste normalization", err)
		}
	}
	for _, input := range []string{"sk-first sk-second", "two tokens", strings.Repeat("x", djs.MaxKeyLength+1), "sk-proj-invalid\x00"} {
		if err := shared.SaveSettings(ctx, "test-model", producer.KeyChange{Value: input}); err == nil {
			t.Fatal("invalid key accepted")
		}
		got, _ := repo.APIKey(ctx, p.ID)
		if got != key {
			t.Fatal("invalid submission changed saved credential")
		}
	}
	if err := shared.SaveSettings(ctx, "test-model", producer.KeyChange{Value: key, Clear: true}); err == nil || !strings.Contains(err.Error(), "Untick") {
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
	if err = NewProducer(r).SaveSettings(ctx, "test-model", producer.KeyChange{Value: "sk-test"}); err != nil {
		t.Fatal(err)
	}
	if err = r.Save(ctx, p, djs.KeyChange{}); err != nil {
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
