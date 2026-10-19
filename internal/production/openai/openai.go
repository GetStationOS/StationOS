// Package openai is the external preparation adapter. Credentials never enter a plan or log.
package openai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/prompts"
	"stationos.local/stationos/internal/schedule"
	"strings"
	"time"
)

type Producer struct {
	Session production.EditorialSession
	Prompts prompts.Store
	Text    ai.Text
	Keys    production.Credentials
	Store   production.Store
	Audio   production.Normalizer
	Client  *http.Client
	Models  production.ModelSettings
}

func New(keys production.Credentials, store production.Store, audio production.Normalizer) Producer {
	return Producer{Keys: keys, Store: store, Audio: audio, Client: &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("OpenAI redirects are not allowed") }}}
}

type temporaryFailure struct{ error }

func (e *temporaryFailure) Unwrap() error { return e.error }

// Retry only temporary transport and service failures, within the caller's deadline.
// Each request has at most three attempts; cancellation never waits for the backoff.
func (p Producer) call(ctx context.Context, path, key string, body any, limit int64) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		data, err := p.callOnce(ctx, path, key, body, limit)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var temporary *temporaryFailure
		if err == nil || attempt == 2 || ctx.Err() != nil || !errors.As(err, &temporary) {
			return data, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
func (p Producer) callOnce(ctx context.Context, path, key string, body any, limit int64) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/"+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	started := time.Now()
	response, err := p.Client.Do(req)
	if err != nil {
		// Preserve cancellation for the worker's normal playback-boundary cleanup.
		// Never log the transport error itself: it can contain credentials or URLs.
		elapsed := time.Since(started).Round(time.Millisecond)
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("OpenAI %s cancelled after %s: %w", path, elapsed, context.Canceled)
		}
		var networkError net.Error
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
			return nil, &temporaryFailure{fmt.Errorf("OpenAI %s timed out after %s: %w", path, elapsed, context.DeadlineExceeded)}
		}
		var dnsError *net.DNSError
		if errors.As(err, &dnsError) {
			return nil, &temporaryFailure{fmt.Errorf("OpenAI %s DNS lookup failed after %s", path, elapsed)}
		}
		return nil, &temporaryFailure{fmt.Errorf("OpenAI %s connection failed after %s", path, elapsed)}
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		err := fmt.Errorf("OpenAI %s returned HTTP %d", path, response.StatusCode)
		if response.StatusCode == 408 || response.StatusCode == 429 || response.StatusCode >= 500 {
			return nil, &temporaryFailure{err}
		}
		return nil, err
	}
	data, err := production.ReadBounded(response.Body, limit)
	if err == nil {
		return data, nil
	}
	elapsed := time.Since(started).Round(time.Millisecond)
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return nil, fmt.Errorf("OpenAI %s response cancelled after %s: %w", path, elapsed, context.Canceled)
	}
	var networkError net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
		return nil, &temporaryFailure{fmt.Errorf("OpenAI %s response timed out after %s: %w", path, elapsed, context.DeadlineExceeded)}
	}
	return nil, fmt.Errorf("OpenAI %s response incomplete or exceeds limits", path)
}
func (p Producer) Prepare(parent context.Context, show, next schedule.Occurrence) (production.Prepared, error) {
	if show.DJ.ID == "" || (!show.DJ.HasKey && p.Text == nil) {
		return production.Prepared{}, production.ErrUnavailable
	}
	if starter, ok := p.Session.(interface {
		Begin(context.Context, schedule.Occurrence) error
	}); ok {
		if err := starter.Begin(parent, show); err != nil {
			return production.Prepared{}, err
		}
	}
	budget, err := p.textBudget(parent, "curation", 0)
	if err != nil {
		return production.Prepared{}, err
	}
	ctx, cancel := context.WithTimeout(parent, budget+production.Timeout)
	defer cancel()
	key, err := p.Keys.APIKey(ctx, show.DJ.ID)
	if err != nil || key == "" {
		production.ReportInference(ctx, "Spoken links are unavailable because the selected speech connection has no key.")
		return production.Prepared{}, production.ErrUnavailable
	}
	speechModel, err := p.model(ctx, "speech", "gpt-4o-mini-tts")
	if err != nil {
		return production.Prepared{}, err
	}
	identity := ""
	if p.Text != nil {
		identity, err = p.Text.Identity(ctx, "curation")
		if err != nil {
			return production.Prepared{}, err
		}
	}
	basePrompt, err := prompts.Load(ctx, p.Prompts, "show")
	if err != nil {
		return production.Prepared{}, err
	}
	background, err := prompts.Load(ctx, p.Prompts, "background")
	if err != nil {
		return production.Prepared{}, err
	}
	instructions := basePrompt + "DJ prompt:\n" + show.DJ.Prompt + background
	cacheKey := modelCacheKey(production.CacheKey(show, next), speechModel+identity+instructions)
	if p.Store != nil {
		if prepared, err := p.Store.Get(ctx, cacheKey); err == nil && production.ValidatePresentation(prepared.Plan) == nil {
			if p.Session != nil {
				raw, _ := json.Marshal(prepared.Plan)
				_ = p.Session.Remember(ctx, show, "prepared presentation (not yet aired)", string(raw))
			}
			return prepared, nil
		}
	}
	input, _ := json.Marshal(map[string]any{"episode_session": p.episodeContext(ctx, show), "musical_direction": show.MusicDirection, "show": show.Title, "dj": show.DJ.Name, "fictional_background": show.DJ.Background, "next_show": next.Title, "next_dj": next.DJ.Name})
	schema := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"intro", "outro"}, "properties": map[string]any{"intro": map[string]any{"type": "string"}, "outro": map[string]any{"type": "string"}}}

	body := map[string]any{"model": show.DJ.Model, "instructions": instructions, "input": string(input), "max_output_tokens": 512, "store": false, "text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "radio_show", "strict": true, "schema": schema}}}
	plan, err := p.generatePlan(ctx, key, body)
	if err != nil {
		return production.Prepared{}, err
	}
	plan.NextKey = next.Key
	if err = production.ValidatePresentation(plan); err != nil {
		return production.Prepared{}, err
	}
	prepared := production.Prepared{Plan: plan}
	speech := func(text string) ([]byte, error) {
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		raw, err := p.call(ctx, "audio/speech", key, map[string]any{"model": speechModel, "voice": show.DJ.Voice, "input": text, "instructions": show.DJ.Vibe, "speed": show.DJ.ConversationSpeed, "response_format": "mp3"}, 1<<20)
		if err != nil {
			return nil, err
		}
		return p.Audio.MP3(ctx, raw)
	}
	if prepared.Intro, err = speech(plan.IntroText); err != nil {
		return production.Prepared{}, err
	}
	if prepared.Outro, err = speech(plan.OutroText); err != nil {
		return production.Prepared{}, err
	}
	if p.Store != nil {
		if err = p.Store.Put(ctx, cacheKey, show.StationID, prepared); err != nil {
			return production.Prepared{}, errors.New("DJ assets could not be saved")
		}
	}
	if p.Session != nil {
		raw, _ := json.Marshal(prepared.Plan)
		_ = p.Session.Remember(ctx, show, "prepared presentation (not yet aired)", string(raw))
	}
	return prepared, nil
}

func (p Producer) Link(ctx context.Context, link production.LinkRequest) ([]byte, error) {
	link.Audience = nil
	audio, _, err := p.audienceLink(ctx, link)
	return audio, err
}
func (p Producer) AudienceLink(ctx context.Context, link production.LinkRequest) ([]byte, []string, error) {
	if len(link.Audience) > production.MaxLinkShoutouts || (link.Handover && len(link.Audience) > 0) {
		return nil, nil, errors.New("invalid audience link scope")
	}
	for _, item := range link.Audience {
		if item.Kind == audience.Request && len(link.Audience) != 1 {
			return nil, nil, errors.New("request must have its own introduction")
		}
	}
	return p.audienceLink(ctx, link)
}
func (p Producer) audienceLink(parent context.Context, link production.LinkRequest) ([]byte, []string, error) {
	if link.Show.DJ.ID == "" || (!link.Show.DJ.HasKey && p.Text == nil) {
		return nil, nil, production.ErrUnavailable
	}
	budget, err := p.textBudget(parent, "curation", 0)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(parent, budget+production.Timeout)
	defer cancel()
	key, err := p.Keys.APIKey(ctx, link.Show.DJ.ID)
	if err != nil || key == "" {
		production.ReportInference(ctx, "Spoken links are unavailable because the selected speech connection has no key.")
		return nil, nil, production.ErrUnavailable
	}
	speechModel, err := p.model(ctx, "speech", "gpt-4o-mini-tts")
	if err != nil {
		return nil, nil, err
	}
	rawInput, _ := json.Marshal(map[string]any{"episode_session": p.episodeContext(ctx, link.Show), "musical_direction": link.Show.MusicDirection, "show": link.Show.Title, "dj": link.Show.DJ.Name, "fictional_background": link.Show.DJ.Background, "following_show": link.Following.Title, "following_dj": link.Following.DJ.Name, "current": songDetails(link.Current), "next": songDetails(link.Next), "next_track_id": link.Next.ID, "handover": link.Handover, "remaining_seconds": link.RemainingSeconds, "audience": link.Audience})
	basePrompt, err := prompts.Load(ctx, p.Prompts, "link")
	if err != nil {
		return nil, nil, err
	}
	background, err := prompts.Load(ctx, p.Prompts, "background")
	if err != nil {
		return nil, nil, err
	}
	instructions := basePrompt + "DJ prompt:\n" + link.Show.DJ.Prompt + background
	body := map[string]any{"model": link.Show.DJ.Model, "instructions": instructions, "input": string(rawInput), "max_output_tokens": 512, "store": false}
	if len(link.Audience) > 0 {
		audiencePrompt, err := prompts.Load(ctx, p.Prompts, "audience-link")
		if err != nil {
			return nil, nil, err
		}
		body["instructions"] = instructions + audiencePrompt
		if len(link.Audience) > 1 {
			body["instructions"] = strings.Replace(body["instructions"].(string), "maximum 45 words", "maximum 75 words", 1)
		}
		for _, item := range link.Audience {
			if item.Kind == audience.Request && item.TrackID != link.Next.ID {
				return nil, nil, errors.New("request does not match queued recording")
			}
		}
		body["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "audience_link", "strict": true, "schema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"speech": map[string]any{"type": "string"}, "used_ids": map[string]any{"type": "array", "maxItems": len(link.Audience), "items": map[string]any{"type": "string"}}}, "required": []string{"speech", "used_ids"}}}}
	}
	raw, err := p.textCall(ctx, key, body, 1<<20)
	if err != nil {
		return nil, nil, err
	}
	var response struct {
		Status     string
		Incomplete struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Output []struct{ Content []struct{ Type, Text string } }
	}
	if err = json.Unmarshal(raw, &response); err != nil {
		return nil, nil, errors.New("invalid presenter link response")
	}
	if response.Status != "completed" {
		return nil, nil, incompleteError("presenter link", response.Incomplete.Reason)
	}
	var text strings.Builder
	for _, o := range response.Output {
		for _, c := range o.Content {
			if c.Type == "output_text" {
				text.WriteString(c.Text)
			}
		}
	}
	spoken := strings.TrimSpace(text.String())
	var used []string
	if len(link.Audience) > 0 {
		var result struct {
			Speech string   `json:"speech"`
			Used   []string `json:"used_ids"`
		}
		if json.Unmarshal([]byte(spoken), &result) != nil || len(result.Used) > len(link.Audience) {
			return nil, nil, errors.New("invalid audience link")
		}
		seen := map[string]bool{}
		for _, id := range result.Used {
			if seen[id] {
				return nil, nil, errors.New("duplicate audience acknowledgement")
			}
			seen[id] = true
			found := false
			for _, item := range link.Audience {
				if item.ID == id && (item.Kind == audience.Shoutout || item.TrackID == link.Next.ID) {
					found = true
				}
			}
			if !found {
				return nil, nil, errors.New("unoffered audience item")
			}
		}
		for _, item := range link.Audience {
			if item.Kind == audience.Request && (len(result.Used) != 1 || result.Used[0] != item.ID) {
				return nil, nil, errors.New("request introduction omitted")
			}
		}
		if !seen[link.Audience[0].ID] {
			return nil, nil, errors.New("approved shout-out omitted")
		}
		spoken = strings.TrimSpace(result.Speech)
		used = result.Used
	}
	maxSpeechBytes := 600
	if len(link.Audience) > 1 {
		maxSpeechBytes = 900
	}
	if len(spoken) == 0 || len(spoken) > maxSpeechBytes {
		return nil, nil, errors.New("presenter link exceeds limits")
	}
	raw, err = p.call(ctx, "audio/speech", key, map[string]any{"model": speechModel, "voice": link.Show.DJ.Voice, "input": spoken, "instructions": link.Show.DJ.Vibe, "speed": link.Show.DJ.ConversationSpeed, "response_format": "mp3"}, 1<<20)
	if err != nil {
		return nil, nil, err
	}
	audio, err := p.Audio.MP3(ctx, raw)
	if err != nil {
		return nil, nil, err
	}
	if p.Session != nil {
		_ = p.Session.Remember(ctx, link.Show, "prepared speech (not yet aired)", spoken)
	}
	production.ReportSpeechTranscript(parent, spoken)
	return audio, used, nil
}

// Bound prompt metadata without splitting a UTF-8 name.
func short(s string, limit int) string {
	r := []rune(s)
	if len(r) > limit {
		r = r[:limit]
	}
	return string(r)
}

// Only documented classification values enter logs, never raw model text or messages.
func incompleteError(stage, reason string) error {
	switch reason {
	case "max_output_tokens":
		return fmt.Errorf("%s incomplete: output token limit reached", stage)
	case "content_filter":
		return fmt.Errorf("%s incomplete: content filter", stage)
	case "max_messages":
		return fmt.Errorf("%s incomplete: message limit reached", stage)
	case "steered":
		return fmt.Errorf("%s incomplete: response steered", stage)
	default:
		return fmt.Errorf("%s incomplete: reason not supplied", stage)
	}
}

func (p Producer) model(ctx context.Context, role, fallback string) (string, error) {
	if p.Models == nil {
		return fallback, nil
	}
	return p.Models.Model(ctx, role)
}
func modelCacheKey(base, model string) string {
	sum := sha256.Sum256([]byte(base + "\nspeech_model=" + model))
	return hex.EncodeToString(sum[:])
}

// textCall translates the legacy preparation envelope into the text port. Speech
// remains a separate call with its own centrally selected credential and model.
func (p Producer) textCall(ctx context.Context, key string, body map[string]any, limit int64) ([]byte, error) {
	if p.Text == nil {
		return p.call(ctx, "responses", key, body, limit)
	}
	maxTokens, _ := body["max_output_tokens"].(int)
	q := ai.TextRequest{Options: ai.GenerationOptions{DisableThinking: true, MaxOutputTokens: maxTokens}, Role: "curation", Model: body["model"].(string), Instructions: body["instructions"].(string), Input: body["input"].(string)}
	if format, ok := body["text"].(map[string]any); ok {
		if f, ok := format["format"].(map[string]any); ok {
			q.Schema, _ = json.Marshal(f["schema"])
		}
	}
	text, err := p.Text.Generate(ctx, q)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"content": []any{map[string]any{"type": "output_text", "text": text}}}}})
}

func (p Producer) textBudget(ctx context.Context, role string, fallback time.Duration) (time.Duration, error) {
	if timed, ok := p.Text.(ai.TimedText); ok {
		return timed.Budget(ctx, role)
	}
	return fallback, nil
}

func (p Producer) generatePlan(ctx context.Context, key string, body map[string]any) (production.Plan, error) {
	raw, err := p.textCall(ctx, key, body, 1<<20)
	if err != nil {
		return production.Plan{}, err
	}
	var response struct {
		Status     string `json:"status"`
		Incomplete struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Output []struct {
			Content []struct{ Type, Text string } `json:"content"`
		} `json:"output"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return production.Plan{}, errors.New("invalid DJ response")
	}
	if response.Status != "completed" {
		return production.Plan{}, incompleteError("DJ preparation", response.Incomplete.Reason)
	}
	var text strings.Builder
	for _, output := range response.Output {
		for _, c := range output.Content {
			if c.Type == "output_text" {
				text.WriteString(c.Text)
			}
		}
	}
	var plan production.Plan
	decoder := json.NewDecoder(strings.NewReader(text.String()))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&plan); err != nil {
		return production.Plan{}, errors.New("invalid DJ plan")
	}
	return plan, nil
}

// Session state is explicit and provider-independent; cached preparation remains
// distinguishable from actual airtime in the episode's aired history.
func (p Producer) episodeContext(ctx context.Context, o schedule.Occurrence) string {
	if p.Session == nil {
		return ""
	}
	value, err := p.Session.Context(ctx, o)
	if err != nil {
		return "Episode context unavailable; do not claim earlier tracks aired."
	}
	return value
}
