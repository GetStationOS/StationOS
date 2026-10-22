package persistence

import (
	"context"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"stationos.local/stationos/internal/changefeed"
	"strings"
	"time"

	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/producer"
)

// Producer shares the existing secrets-key file, with independent associated data.
type Producer struct {
	Changes *changefeed.Bus
	DB      *sql.DB
	secret  cipher.AEAD
}

func NewProducer(d DJs) Producer { return Producer{DB: d.DB, secret: d.secret} }

var _ producer.Store = Producer{}

func (p Producer) Settings(ctx context.Context) (producer.Settings, error) {
	var s producer.Settings
	e := p.DB.QueryRowContext(ctx, `SELECT model,curation_model,writing_model,speech_model,audience_model,image_model,length(api_key_cipher)>0 FROM producer_settings WHERE id=1`).Scan(&s.Model, &s.CurationModel, &s.WritingModel, &s.SpeechModel, &s.AudienceModel, &s.ImageModel, &s.HasKey)
	if e == nil {
		s.Routes, e = p.Routes(ctx)
		if e == nil {
			c, err := p.Connection(ctx, s.Routes.Assistant.Connection)
			e = err
			s.TextReady = err == nil && (c.Type != "openai" || c.HasKey)
		}
	}
	return s, e
}

// SaveSettings preserves role-specific model choices for existing callers.
func (p Producer) SaveSettings(ctx context.Context, model string, k producer.KeyChange) error {
	settings, e := p.Settings(ctx)
	if e != nil {
		return e
	}
	settings.Model = model
	return p.Configure(ctx, settings, k)
}
func (p Producer) Configure(ctx context.Context, settings producer.Settings, k producer.KeyChange) error {
	k = producer.NormalizeKey(k)
	if settings.WritingModel == "" {
		current, err := p.Settings(ctx)
		if err != nil {
			return err
		}
		settings.WritingModel = current.WritingModel
	}
	if settings.ImageModel == "" {
		current, err := p.Settings(ctx)
		if err != nil {
			return err
		}
		settings.ImageModel = current.ImageModel
	}
	for _, model := range []string{settings.Model, settings.CurationModel, settings.WritingModel, settings.SpeechModel, settings.AudienceModel, settings.ImageModel} {
		if e := producer.ValidateSettings(model, k); e != nil {
			return e
		}
	}
	if e := p.validateRoutes(ctx, settings.Routes); e != nil {
		return e
	}
	routesJSON, _ := json.Marshal(settings.Routes)
	encrypted := []byte{}
	if k.Value != "" {
		nonce := make([]byte, p.secret.NonceSize())
		if _, e := rand.Read(nonce); e != nil {
			return e
		}
		encrypted = p.secret.Seal(nonce, nonce, []byte(k.Value), []byte("stationos:producer"))
	}
	tx, e := p.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `UPDATE producer_settings SET model=?,curation_model=?,writing_model=?,speech_model=?,audience_model=?,image_model=?,provider_routes=?,ai_revision=ai_revision+1,api_key_cipher=CASE WHEN ? THEN ? ELSE api_key_cipher END WHERE id=1`, settings.Model, settings.CurationModel, settings.WritingModel, settings.SpeechModel, settings.AudienceModel, settings.ImageModel, string(routesJSON), k.Clear || k.Value != "", encrypted); e != nil {
		return e
	}
	if k.Clear || k.Value != "" {
		if _, e = tx.ExecContext(ctx, `UPDATE djs SET api_key_cipher=X''`); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// ProducerKeys adapts the single credential to the production credential port.
type ProducerKeys struct{ Producer Producer }

func (p ProducerKeys) APIKey(ctx context.Context, _ string) (string, error) {
	if id := djs.SpeechConnection(ctx); id != "" {
		return p.Producer.Credential(ctx, id)
	}
	r, e := p.Producer.Routes(ctx)
	if e != nil {
		return "", e
	}
	return p.Producer.Credential(ctx, r.Speech.Connection)
}
func (p Producer) Model(ctx context.Context, role string) (string, error) {
	settings, e := p.Settings(ctx)
	if e != nil {
		return "", e
	}
	switch role {
	case "curation":
		return settings.CurationModel, nil
	case "writing":
		return settings.WritingModel, nil
	case "speech":
		return settings.SpeechModel, nil
	case "image":
		return settings.ImageModel, nil
	case "audience":
		return settings.AudienceModel, nil
	default:
		return settings.Model, nil
	}
}

// AdoptLegacyKey migrates an unambiguous historical DJ key when no shared key is
// saved. If old DJs have different keys, settings must choose the shared key.
func (p Producer) AdoptLegacyKey(ctx context.Context) error {
	settings, e := p.Settings(ctx)
	if e != nil {
		return e
	}
	if settings.HasKey {
		_, e = p.DB.ExecContext(ctx, `UPDATE djs SET api_key_cipher=X''`)
		return e
	}
	rows, e := p.DB.QueryContext(ctx, `SELECT id,api_key_cipher FROM djs WHERE length(api_key_cipher)>0`)
	if e != nil {
		return e
	}
	unique := map[string]bool{}
	for rows.Next() {
		var id string
		var encrypted []byte
		if e = rows.Scan(&id, &encrypted); e != nil {
			rows.Close()
			return e
		}
		n := p.secret.NonceSize()
		if len(encrypted) < n {
			rows.Close()
			return errors.New("Invalid legacy credential")
		}
		raw, e := p.secret.Open(nil, encrypted[:n], encrypted[n:], []byte(id))
		if e != nil {
			rows.Close()
			return errors.New("Legacy credential could not be decrypted")
		}
		unique[string(raw)] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if len(unique) == 1 {
		for key := range unique {
			return p.Configure(ctx, settings, producer.KeyChange{Value: key})
		}
	}
	return nil
}
func (p Producer) APIKey(ctx context.Context) (string, error) {
	r, e := p.Routes(ctx)
	if e != nil {
		return "", e
	}
	return p.Credential(ctx, r.Image.Connection)
}
func (p Producer) legacyKey(ctx context.Context) (string, error) {
	var b []byte
	e := p.DB.QueryRowContext(ctx, `SELECT api_key_cipher FROM producer_settings WHERE id=1`).Scan(&b)
	if e != nil || len(b) == 0 {
		return "", e
	}
	n := p.secret.NonceSize()
	if len(b) < n {
		return "", errors.New("Invalid encrypted assistant credential")
	}
	v, e := p.secret.Open(nil, b[:n], b[n:], []byte("stationos:producer"))
	if e != nil {
		return "", errors.New("Assistant credential could not be decrypted; restore the secrets-key file")
	}
	return string(v), nil
}
func (p Producer) Create(ctx context.Context, c producer.Conversation) (changeErr error) {
	defer func() {
		if changeErr == nil {
			p.Changes.Publish("assistant")
		}
	}()
	_, e := p.DB.ExecContext(ctx, `INSERT INTO producer_conversations(id,station_id,title,created) VALUES(?,?,?,?)`, c.ID, c.StationID, c.Title, time.Now().UTC().Format(time.RFC3339Nano))
	return e
}
func (p Producer) List(ctx context.Context, stationID string) ([]producer.Conversation, error) {
	rows, e := p.DB.QueryContext(ctx, `SELECT id,station_id,title,status FROM producer_conversations WHERE station_id=? ORDER BY created DESC LIMIT 30`, stationID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []producer.Conversation{}
	for rows.Next() {
		var c producer.Conversation
		if e = rows.Scan(&c.ID, &c.StationID, &c.Title, &c.Status); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (p Producer) Get(ctx context.Context, id string) (producer.Conversation, error) {
	var c producer.Conversation
	var raw, taskJSON string
	e := p.DB.QueryRowContext(ctx, `SELECT id,station_id,title,status,transcript,task_json FROM producer_conversations WHERE id=?`, id).Scan(&c.ID, &c.StationID, &c.Title, &c.Status, &raw, &taskJSON)
	if errors.Is(e, sql.ErrNoRows) {
		return c, producer.ErrNotFound
	}
	if e != nil {
		return c, e
	}
	c.Transcript = []byte(raw)
	if e = json.Unmarshal([]byte(taskJSON), &c.Task); e != nil {
		return c, e
	}
	rows, e := p.DB.QueryContext(ctx, `SELECT id,role,text,url FROM (SELECT id,role,text,url FROM producer_messages WHERE conversation_id=? ORDER BY id DESC LIMIT 100) ORDER BY id`, id)
	if e != nil {
		return c, e
	}
	defer rows.Close()
	for rows.Next() {
		var m producer.Message
		if e = rows.Scan(&m.ID, &m.Role, &m.Text, &m.URL); e != nil {
			return c, e
		}
		c.Messages = append(c.Messages, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return c, e
	}
	// Human requirements are authoritative and are not limited by the UI's last
	// 100 messages or a recent action-log window.
	requests, e := p.DB.QueryContext(ctx, `SELECT text FROM producer_messages WHERE conversation_id=? AND role='user' ORDER BY id`, id)
	if e != nil {
		return c, e
	}
	defer requests.Close()
	for requests.Next() {
		var value string
		if e = requests.Scan(&value); e != nil {
			return c, e
		}
		c.Requests = append(c.Requests, value)
	}
	return c, requests.Err()
}
func (p Producer) Begin(ctx context.Context, id, requestID, text string) (changeErr error) {
	defer func() {
		if changeErr == nil {
			p.Changes.Publish("assistant")
		}
	}()
	tx, e := p.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var status, raw string
	if e = tx.QueryRowContext(ctx, `SELECT status,transcript FROM producer_conversations WHERE id=?`, id).Scan(&status, &raw); errors.Is(e, sql.ErrNoRows) {
		return producer.ErrNotFound
	} else if e != nil {
		return e
	}
	var n int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM producer_turns WHERE conversation_id=? AND request_id=?`, id, requestID).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return producer.ErrDuplicate
	}
	if status == "running" {
		return producer.ErrBusy
	}

	var items []json.RawMessage
	if e = json.Unmarshal([]byte(raw), &items); e != nil {
		return e
	}
	if len(items) == 0 {
		// Recover legacy failures without losing the original request beyond recent action records.
		rows, e := tx.QueryContext(ctx, `SELECT role,text FROM producer_messages WHERE conversation_id=? AND (role='user' OR id IN (SELECT id FROM producer_messages WHERE conversation_id=? ORDER BY id DESC LIMIT 30)) ORDER BY id`, id, id)
		if e != nil {
			return e
		}
		var history strings.Builder
		for rows.Next() {
			var role, value string
			if e = rows.Scan(&role, &value); e != nil {
				rows.Close()
				return e
			}
			if role == "reasoning" {
				continue
			}
			if role == "user" {
				b, _ := json.Marshal(map[string]any{"role": "user", "content": value})
				items = append(items, b)
			} else {
				history.WriteString(role + ": " + value + "\n")
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if history.Len() > 0 {
			b, _ := json.Marshal(map[string]any{"role": "assistant", "content": "Earlier conversation and recorded actions (inspect current state before continuing):\n" + history.String()})
			items = append(items, b)
		}
	}
	if status == "failed" {
		// Include recent intent/outcome records even if a process died before its next checkpoint.
		rows, e := tx.QueryContext(ctx, `SELECT role,text,url FROM (SELECT id,role,text,url FROM producer_messages WHERE conversation_id=? AND role NOT IN ('user','reasoning') ORDER BY id DESC LIMIT 30) ORDER BY id`, id)
		if e != nil {
			return e
		}
		var recovery strings.Builder
		for rows.Next() {
			var role, value, link string
			if e = rows.Scan(&role, &value, &link); e != nil {
				rows.Close()
				return e
			}
			recovery.WriteString(role + ": " + value + " " + link + "\n")
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if recovery.Len() > 0 {
			b, _ := json.Marshal(map[string]any{"role": "assistant", "content": "Recent interrupted-run action records; inspect current state before repeating any uncertain action:\n" + recovery.String()})
			items = append(items, b)
		}
	}
	b, _ := json.Marshal(map[string]any{"role": "user", "content": text})
	items = append(items, b)
	transcript, _ := json.Marshal(items)
	if len(transcript) > producer.MaxTranscript {
		return errors.New("This conversation's saved instructions exceed the context limit. The original requests remain stored.")
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO producer_turns VALUES(?,?)`, id, requestID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO producer_messages(conversation_id,role,text,created) VALUES(?,'user',?,?)`, id, text, time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
		return e
	}
	title := text
	if len(title) > 80 {
		title = string([]rune(title)[:min(60, len([]rune(title)))]) + "…"
	}
	if _, e = tx.ExecContext(ctx, `UPDATE producer_conversations SET status='running',turn_id=?,transcript=?,title=CASE WHEN title='New conversation' THEN ? ELSE title END WHERE id=?`, requestID, string(transcript), title, id); e != nil {
		return e
	}
	return tx.Commit()
}
func (p Producer) Append(ctx context.Context, id string, m producer.Message) (changeErr error) {
	defer func() {
		if changeErr == nil {
			p.Changes.Publish("assistant")
		}
	}()
	if len(m.Text) > 32000 {
		return errors.New("Assistant message exceeds the limit")
	}
	_, e := p.DB.ExecContext(ctx, `INSERT INTO producer_messages(conversation_id,role,text,url,created) VALUES(?,?,?,?,?)`, id, m.Role, m.Text, m.URL, time.Now().UTC().Format(time.RFC3339Nano))
	return e
}
func (p Producer) Finish(ctx context.Context, id, status string, raw json.RawMessage, message producer.Message) (changeErr error) {
	defer func() {
		if changeErr == nil {
			p.Changes.Publish("assistant")
		}
	}()
	if status != "idle" && status != "failed" {
		return errors.New("Invalid conversation state")
	}
	if len(raw) == 0 {
		raw = []byte(`[]`)
	}
	if len(raw) > producer.MaxTranscript || !json.Valid(raw) {
		status = "failed"
		raw = []byte(`[]`)
	}
	tx, e := p.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `UPDATE producer_conversations SET status=?,transcript=? WHERE id=?`, status, string(raw), id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO producer_messages(conversation_id,role,text,url,created) VALUES(?,?,?,?,?)`, id, message.Role, message.Text, message.URL, time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
		return e
	}
	return tx.Commit()
}
func (p Producer) Interrupt(ctx context.Context) (changeErr error) {
	defer func() {
		if changeErr == nil {
			p.Changes.Publish("assistant")
		}
	}()
	tx, e := p.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `INSERT INTO producer_messages(conversation_id,role,text,created) SELECT id,'assistant','The server restarted during this request. Completed actions remain saved. Inspect current state before continuing.',? FROM producer_conversations WHERE status='running'`, time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE producer_conversations SET status='failed' WHERE status='running'`); e != nil {
		return e
	}
	return tx.Commit()
}

func (p Producer) Checkpoint(ctx context.Context, id string, raw json.RawMessage, task producer.Task) (changeErr error) {
	defer func() {
		if changeErr == nil {
			p.Changes.Publish("assistant")
		}
	}()
	if !json.Valid(raw) || len(raw) > producer.MaxTranscript {
		return errors.New("Invalid assistant checkpoint")
	}
	if task.Brief != "" {
		if e := task.Validate(); e != nil {
			return e
		}
	}
	taskJSON, e := json.Marshal(task)
	if e != nil {
		return e
	}
	result, e := p.DB.ExecContext(ctx, `UPDATE producer_conversations SET transcript=?,task_json=? WHERE id=? AND status='running'`, string(raw), string(taskJSON), id)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e == nil && n != 1 {
		return producer.ErrNotFound
	}
	return e
}
