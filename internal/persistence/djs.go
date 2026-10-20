package persistence

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"stationos.local/stationos/internal/djs"
)

// DJs encrypts credentials separately from the metadata returned to the web UI.
type DJs struct {
	DB     *sql.DB
	secret cipher.AEAD
}

var _ djs.Repository = DJs{}

func NewDJs(db *sql.DB, key []byte) (DJs, error) {
	if len(key) != 32 {
		return DJs{}, errors.New("DJ credential key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return DJs{}, err
	}
	aead, err := cipher.NewGCM(block)
	return DJs{DB: db, secret: aead}, err
}

// OpenDJs keeps the encryption key beside the DB, owner-readable only. Never
// silently replace a missing key when encrypted credentials already exist.
func OpenDJs(ctx context.Context, db *sql.DB, path string) (DJs, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		var count int
		if err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM djs WHERE length(api_key_cipher)>0)+(SELECT count(*) FROM producer_settings WHERE length(api_key_cipher)>0)+(SELECT count(*) FROM ai_connections WHERE length(api_key_cipher)>0)`).Scan(&count); err != nil {
			return DJs{}, err
		}
		if count > 0 {
			return DJs{}, errors.New("DJ credential key is missing; restore the database's secrets-key file")
		}
		var key [32]byte
		if _, err = rand.Read(key[:]); err != nil {
			return DJs{}, err
		}
		f, err := os.CreateTemp(filepath.Dir(path), ".stationos-key-*")
		if err != nil {
			return DJs{}, err
		}
		defer os.Remove(f.Name())
		if _, err = f.Write(key[:]); err != nil {
			f.Close()
			return DJs{}, err
		}
		if err = f.Sync(); err != nil {
			f.Close()
			return DJs{}, err
		}
		if err = f.Close(); err != nil {
			return DJs{}, err
		}
		if err = os.Link(f.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
			return DJs{}, err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return DJs{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return DJs{}, errors.New("DJ credential key must be a private regular file (mode 0600)")
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return DJs{}, err
	}
	return NewDJs(db, key)
}
func (r DJs) Get(ctx context.Context, id string) (djs.Profile, error) {
	var p djs.Profile
	var voices string
	err := r.DB.QueryRowContext(ctx, `SELECT id,name,(SELECT curation_model FROM producer_settings WHERE id=1),voice,prompt,vibe,conversation_speed,background,portrait_url,`+speechReadySQL+`,`+textReadySQL+`,(SELECT ai_revision FROM producer_settings WHERE id=1),speech_voices FROM djs WHERE id=?`, id).Scan(&p.ID, &p.Name, &p.Model, &p.Voice, &p.Prompt, &p.Vibe, &p.ConversationSpeed, &p.Background, &p.PortraitURL, &p.HasKey, &p.TextReady, &p.AIRevision, &voices)
	if err == nil {
		err = json.Unmarshal([]byte(voices), &p.SpeechVoices)
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = djs.ErrNotFound
	}
	return p, err
}
func (r DJs) List(ctx context.Context) ([]djs.Profile, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id,name,(SELECT curation_model FROM producer_settings WHERE id=1),voice,prompt,vibe,conversation_speed,background,portrait_url,`+speechReadySQL+`,`+textReadySQL+`,(SELECT ai_revision FROM producer_settings WHERE id=1),speech_voices FROM djs ORDER BY name COLLATE NOCASE,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []djs.Profile{}
	for rows.Next() {
		var p djs.Profile
		var voices string
		if err = rows.Scan(&p.ID, &p.Name, &p.Model, &p.Voice, &p.Prompt, &p.Vibe, &p.ConversationSpeed, &p.Background, &p.PortraitURL, &p.HasKey, &p.TextReady, &p.AIRevision, &voices); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(voices), &p.SpeechVoices); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r DJs) Save(ctx context.Context, p djs.Profile, k djs.KeyChange) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.ID == "" {
		return errors.New("missing DJ ID")
	}
	if k.Value != "" || k.Clear {
		return errors.New("Manage the shared API key in Assistant settings")
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if p.Photo != nil {
		if p.Photo.Clear {
			p.PortraitURL = ""
		}
		if len(p.Photo.Data) > 0 {
			hash := sha256.Sum256(p.Photo.Data)
			p.PortraitURL = "/djs/" + url.PathEscape(p.ID) + "/photo?v=" + hex.EncodeToString(hash[:])
		}
	}
	voices := []byte("{}")
	if p.SpeechVoices != nil {
		voices, _ = json.Marshal(p.SpeechVoices)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO djs(id,name,voice,prompt,vibe,conversation_speed,background,portrait_url,speech_voices) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,voice=excluded.voice,prompt=excluded.prompt,vibe=excluded.vibe,conversation_speed=excluded.conversation_speed,background=excluded.background,portrait_url=excluded.portrait_url,speech_voices=CASE WHEN ? THEN excluded.speech_voices ELSE djs.speech_voices END`, p.ID, p.Name, p.Voice, p.Prompt, p.Vibe, p.ConversationSpeed, p.Background, p.PortraitURL, string(voices), p.SpeechVoices != nil)
	if err != nil {
		return err
	}
	if p.Photo != nil {
		if p.Photo.Clear {
			_, err = tx.ExecContext(ctx, `DELETE FROM dj_photos WHERE dj_id=?`, p.ID)
		}
		if len(p.Photo.Data) > 0 {
			_, err = tx.ExecContext(ctx, `INSERT INTO dj_photos(dj_id,data) VALUES(?,?) ON CONFLICT(dj_id) DO UPDATE SET data=excluded.data`, p.ID, p.Photo.Data)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// APIKey is a compatibility adapter for the shared credential; no DJ key is read.
func (r DJs) APIKey(ctx context.Context, id string) (string, error) {
	if _, e := r.Get(ctx, id); e != nil {
		return "", e
	}
	return NewProducer(r).APIKey(ctx)
}
func (r DJs) Delete(ctx context.Context, id string) error {
	result, err := r.DB.ExecContext(ctx, `DELETE FROM djs WHERE id=?`, id)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			return djs.ErrInUse
		}
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return djs.ErrNotFound
	}
	return err
}

func (r DJs) Photo(ctx context.Context, id string) ([]byte, error) {
	var data []byte
	err := r.DB.QueryRowContext(ctx, `SELECT data FROM dj_photos WHERE dj_id=?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		err = djs.ErrNotFound
	}
	return data, err
}
