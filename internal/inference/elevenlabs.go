package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
)

var elevenVoiceID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func (c *Client) elevenModels(ctx context.Context, conn ai.Connection, key string) ([]string, error) {
	raw, err := c.request(ctx, conn, key, "GET", "/v1/models", nil)
	if err != nil {
		return nil, err
	}
	var models []struct {
		ID  string `json:"model_id"`
		TTS bool   `json:"can_do_text_to_speech"`
	}
	if json.Unmarshal(raw, &models) != nil || models == nil {
		return nil, errors.New("Invalid ElevenLabs model catalogue")
	}
	out := []string{}
	for _, m := range models {
		if m.TTS && ai.ValidModel(m.ID) {
			out = append(out, m.ID)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("ElevenLabs returned no speech models")
	}
	return out, nil
}
func (r *Router) SpeechModels(ctx context.Context, id string) ([]string, error) {
	conn, err := r.Store.Connection(ctx, id)
	if err != nil {
		return nil, err
	}
	key, err := r.Store.Credential(ctx, id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if conn.Type != "elevenlabs" {
		return nil, nil
	}
	return r.Local.elevenModels(ctx, conn, key)
}
func (c *Client) elevenVoices(parent context.Context, conn ai.Connection, key string) (djs.VoiceCatalog, error) {
	out := djs.VoiceCatalog{Connection: conn.ID, Provider: conn.Type}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	if key == "" {
		return out, errors.New("Save an ElevenLabs API key before discovering voices")
	}
	token := ""
	seen := map[string]bool{}
	tokens := map[string]bool{}
	for page := 0; page < 20; page++ {
		path := "/v2/voices?page_size=100&include_total_count=false&voice_type=saved"
		if token != "" {
			path += "&next_page_token=" + url.QueryEscape(token)
		}
		raw, err := c.request(ctx, conn, key, "GET", path, nil)
		if err != nil {
			return out, err
		}
		var v struct {
			Voices []struct {
				ID   string `json:"voice_id"`
				Name string
			}
			More bool   `json:"has_more"`
			Next string `json:"next_page_token"`
		}
		if json.Unmarshal(raw, &v) != nil || v.Voices == nil {
			return out, errors.New("Invalid ElevenLabs voice catalogue")
		}
		for _, voice := range v.Voices {
			if !elevenVoiceID.MatchString(voice.ID) || seen[voice.ID] || strings.TrimSpace(voice.Name) == "" {
				return out, errors.New("Invalid ElevenLabs voice")
			}
			if len(out.Voices) >= 2000 {
				return out, errors.New("ElevenLabs voice catalogue exceeds limits")
			}
			seen[voice.ID] = true
			out.Voices = append(out.Voices, djs.Voice{ID: voice.ID, Name: voice.Name})
		}
		if !v.More {
			return out, nil
		}
		if v.Next == "" || tokens[v.Next] {
			return out, errors.New("Incomplete ElevenLabs voice catalogue")
		}
		token = v.Next
		tokens[token] = true
	}
	return out, errors.New("ElevenLabs voice catalogue exceeds limits")
}

// elevenSpeech uses the account voice's stored settings. No automatic replay of
// paid generation occurs when the outcome is uncertain.
func (c *Client) elevenSpeech(ctx context.Context, conn ai.Connection, key string, q production.SpeechRequest) ([]byte, error) {
	if key == "" {
		return nil, errors.New("Save an ElevenLabs API key")
	}
	if !elevenVoiceID.MatchString(q.Voice) || !ai.ValidModel(q.Model) || strings.TrimSpace(q.Text) == "" || len(q.Text) > 16000 {
		return nil, errors.New("Choose an ElevenLabs model and voice and supply speech text")
	}
	raw, _ := json.Marshal(map[string]string{"text": q.Text, "model_id": q.Model})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(conn.Address, "/")+"/v1/text-to-speech/"+url.PathEscape(q.Voice)+"?output_format=mp3_44100_128", bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("Invalid ElevenLabs address")
	}
	req.Header.Set("xi-api-key", key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")
	client := *c.HTTP
	client.Timeout = conn.RequestTimeout()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("ElevenLabs redirects are not allowed") }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return nil, fmt.Errorf("ElevenLabs speech timed out: %w", context.DeadlineExceeded)
		}
		return nil, errors.New("ElevenLabs speech request failed; check the endpoint and response deadline")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if resp.StatusCode == 429 {
			return nil, ai.ErrRateLimit
		}
		return nil, fmt.Errorf("ElevenLabs speech returned HTTP %d; check API key, credits, model and voice access", resp.StatusCode)
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "audio/mpeg" {
		return nil, errors.New("ElevenLabs did not return MP3 audio")
	}
	audio, err := production.ReadBounded(resp.Body, 16<<20)
	if err != nil {
		return nil, errors.New("ElevenLabs audio incomplete or exceeds limits")
	}
	if len(audio) < 4 || !(string(audio[:3]) == "ID3" || audio[0] == 0xff && audio[1]&0xe0 == 0xe0) {
		return nil, errors.New("ElevenLabs returned empty or invalid MP3 audio")
	}
	return audio, nil
}
