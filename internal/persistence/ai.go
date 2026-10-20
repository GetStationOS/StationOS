package persistence

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"stationos.local/stationos/internal/ai"
)

func (p Producer) Connections(ctx context.Context) ([]ai.Connection, error) {
	s, err := p.Settings(ctx)
	if err != nil {
		return nil, err
	}
	out := []ai.Connection{{ID: "openai", Name: "OpenAI", Type: "openai", Address: "https://api.openai.com/v1", HasKey: s.HasKey, ContextTokens: 32768, OutputTokens: 6000}}
	rows, err := p.DB.QueryContext(ctx, `SELECT id,name,provider_type,address,length(api_key_cipher)>0,context_tokens,output_tokens,request_timeout_seconds FROM ai_connections ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ai.Connection
		if err = rows.Scan(&c.ID, &c.Name, &c.Type, &c.Address, &c.HasKey, &c.ContextTokens, &c.OutputTokens, &c.RequestTimeoutSeconds); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (p Producer) Connection(ctx context.Context, id string) (ai.Connection, error) {
	if id == "" || id == "openai" {
		var key bool
		err := p.DB.QueryRowContext(ctx, `SELECT length(api_key_cipher)>0 FROM producer_settings WHERE id=1`).Scan(&key)
		return ai.Connection{ID: "openai", Name: "OpenAI", Type: "openai", Address: "https://api.openai.com/v1", HasKey: key, ContextTokens: 32768, OutputTokens: 6000}, err
	}
	var c ai.Connection
	err := p.DB.QueryRowContext(ctx, `SELECT id,name,provider_type,address,length(api_key_cipher)>0,context_tokens,output_tokens,request_timeout_seconds FROM ai_connections WHERE id=?`, id).Scan(&c.ID, &c.Name, &c.Type, &c.Address, &c.HasKey, &c.ContextTokens, &c.OutputTokens, &c.RequestTimeoutSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return c, errors.New("Provider connection no longer exists")
	}
	return c, err
}
func (p Producer) Credential(ctx context.Context, id string) (string, error) {
	if id == "" || id == "openai" {
		return p.legacyKey(ctx)
	}
	var b []byte
	err := p.DB.QueryRowContext(ctx, `SELECT api_key_cipher FROM ai_connections WHERE id=?`, id).Scan(&b)
	if err != nil || len(b) == 0 {
		return "", err
	}
	n := p.secret.NonceSize()
	if len(b) < n {
		return "", errors.New("Invalid provider credential")
	}
	raw, err := p.secret.Open(nil, b[:n], b[n:], []byte("stationos:provider:"+id))
	if err != nil {
		return "", errors.New("Provider credential could not be decrypted; restore the secrets-key file")
	}
	return string(raw), nil
}
func (p Producer) SaveConnection(ctx context.Context, c ai.Connection, k ai.KeyChange) error {
	if c.ID == "openai" {
		return errors.New("Use the shared OpenAI key field for the existing connection")
	}
	if c.RequestTimeoutSeconds == 0 {
		c.RequestTimeoutSeconds = int(c.RequestTimeout() / time.Second)
	}
	c.Name = strings.TrimSpace(c.Name)
	c.Address = strings.TrimRight(strings.TrimSpace(c.Address), "/")
	if err := c.Validate(); err != nil {
		return err
	}
	if len(k.Value) > 4096 || strings.IndexFunc(k.Value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 || (k.Clear && k.Value != "") {
		return errors.New("Enter one credential or remove the saved credential")
	}
	var encrypted = []byte{}
	if k.Value != "" {
		nonce := make([]byte, p.secret.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		encrypted = p.secret.Seal(nonce, nonce, []byte(k.Value), []byte("stationos:provider:"+c.ID))
	}
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var used int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM djs JOIN ai_connections c ON c.id=djs.speech_connection WHERE c.id=? AND c.provider_type!=?`, c.ID, c.Type).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return errors.New("This endpoint is used by DJs; choose their replacement endpoint before changing its provider type")
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO ai_connections(id,name,provider_type,address,api_key_cipher,context_tokens,output_tokens,request_timeout_seconds) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,provider_type=excluded.provider_type,address=excluded.address,context_tokens=excluded.context_tokens,output_tokens=excluded.output_tokens,request_timeout_seconds=excluded.request_timeout_seconds,api_key_cipher=CASE WHEN ? THEN excluded.api_key_cipher ELSE ai_connections.api_key_cipher END`, c.ID, c.Name, c.Type, c.Address, encrypted, c.ContextTokens, c.OutputTokens, c.RequestTimeoutSeconds, k.Clear || k.Value != "")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE producer_settings SET ai_revision=ai_revision+1 WHERE id=1`); err != nil {
		return err
	}
	return tx.Commit()
}
func (p Producer) DeleteConnection(ctx context.Context, id string) error {
	if id == "" || id == "openai" {
		return errors.New("The existing OpenAI connection cannot be removed")
	}
	var count int
	if err := p.DB.QueryRowContext(ctx, `SELECT count(*) FROM djs WHERE speech_connection=?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("Choose another speech endpoint for every DJ before removing this connection")
	}
	routes, err := p.Routes(ctx)
	if err != nil {
		return err
	}
	for _, role := range []string{"assistant", "curation", "audience", "image"} {
		s := routes.For(role)
		if s.Connection == id {
			return errors.New("Choose another connection for every role before removing this connection")
		}
	}
	result, err := p.DB.ExecContext(ctx, `DELETE FROM ai_connections WHERE id=? AND NOT EXISTS(SELECT 1 FROM djs WHERE speech_connection=?)`, id, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		if err = p.DB.QueryRowContext(ctx, `SELECT count(*) FROM djs WHERE speech_connection=?`, id).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return errors.New("Choose another speech endpoint for every DJ before removing this connection")
		}
	}
	return nil
}
func (p Producer) Routes(ctx context.Context) (ai.Routes, error) {
	var raw string
	var routes ai.Routes
	err := p.DB.QueryRowContext(ctx, `SELECT provider_routes FROM producer_settings WHERE id=1`).Scan(&raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &routes)
	}
	return routes, err
}
func (p Producer) validateRoutes(ctx context.Context, r ai.Routes) error {
	for _, role := range []string{"assistant", "curation", "audience", "image"} {
		s := r.For(role)
		c, err := p.Connection(ctx, s.Connection)
		if err != nil {
			return err
		}
		if role == "speech" && c.Type != "openai" && c.Type != "chatterbox" {
			return errors.New("Speech requires an OpenAI or Chatterbox connection")
		}
		if (role != "speech" && c.Type == "chatterbox") || (role == "image" && c.Type != "openai") {
			return errors.New("Chatterbox supports speech only; artwork requires OpenAI")
		}

	}
	return nil
}

// Queries return only configuration readiness, never credentials or inferred health.
const speechReadySQL = `(CASE WHEN djs.speech_connection='openai' THEN (SELECT length(api_key_cipher)>0 FROM producer_settings WHERE id=1) ELSE COALESCE((SELECT provider_type='chatterbox' OR length(api_key_cipher)>0 FROM ai_connections WHERE id=djs.speech_connection),0) END)`
const textReadySQL = `(CASE WHEN COALESCE(json_extract((SELECT provider_routes FROM producer_settings WHERE id=1),'$.Curation.Connection'),'') IN ('','openai') THEN (SELECT length(api_key_cipher)>0 FROM producer_settings WHERE id=1) ELSE COALESCE((SELECT provider_type!='openai' OR length(api_key_cipher)>0 FROM ai_connections WHERE id=json_extract((SELECT provider_routes FROM producer_settings WHERE id=1),'$.Curation.Connection')),0) END)`
