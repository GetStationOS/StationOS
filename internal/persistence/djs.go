package persistence

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
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
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM djs WHERE length(api_key_cipher)>0`).Scan(&count); err != nil {
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
	err := r.DB.QueryRowContext(ctx, `SELECT id,name,model,voice,prompt,vibe,conversation_speed,length(api_key_cipher)>0 FROM djs WHERE id=?`, id).Scan(&p.ID, &p.Name, &p.Model, &p.Voice, &p.Prompt, &p.Vibe, &p.ConversationSpeed, &p.HasKey)
	if errors.Is(err, sql.ErrNoRows) {
		err = djs.ErrNotFound
	}
	return p, err
}
func (r DJs) List(ctx context.Context) ([]djs.Profile, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id,name,model,voice,prompt,vibe,conversation_speed,length(api_key_cipher)>0 FROM djs ORDER BY name COLLATE NOCASE,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []djs.Profile{}
	for rows.Next() {
		var p djs.Profile
		if err = rows.Scan(&p.ID, &p.Name, &p.Model, &p.Voice, &p.Prompt, &p.Vibe, &p.ConversationSpeed, &p.HasKey); err != nil {
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
	encrypted := []byte{}
	if k.Value != "" {
		nonce := make([]byte, r.secret.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		encrypted = r.secret.Seal(nonce, nonce, []byte(k.Value), []byte(p.ID))
	}
	_, err := r.DB.ExecContext(ctx, `INSERT INTO djs(id,name,model,voice,prompt,vibe,conversation_speed,api_key_cipher) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,model=excluded.model,voice=excluded.voice,prompt=excluded.prompt,vibe=excluded.vibe,conversation_speed=excluded.conversation_speed,api_key_cipher=CASE WHEN ? THEN excluded.api_key_cipher ELSE djs.api_key_cipher END`, p.ID, p.Name, p.Model, p.Voice, p.Prompt, p.Vibe, p.ConversationSpeed, encrypted, k.Clear || k.Value != "")
	return err
}

// APIKey is reserved for server-side production adapters; metadata reads never decrypt it.
func (r DJs) APIKey(ctx context.Context, id string) (string, error) {
	var encrypted []byte
	err := r.DB.QueryRowContext(ctx, `SELECT api_key_cipher FROM djs WHERE id=?`, id).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return "", djs.ErrNotFound
	}
	if err != nil || len(encrypted) == 0 {
		return "", err
	}
	n := r.secret.NonceSize()
	if len(encrypted) < n {
		return "", errors.New("invalid encrypted DJ credential")
	}
	raw, err := r.secret.Open(nil, encrypted[:n], encrypted[n:], []byte(id))
	if err != nil {
		return "", errors.New("DJ credential could not be decrypted; check the secrets-key file")
	}
	return string(raw), nil
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
