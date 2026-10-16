package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/prompts"
	"stationos.local/stationos/internal/schedule"
	"strings"
	"testing"
)

type promptOverrides map[string]string

func (m promptOverrides) Prompt(_ context.Context, id string) (string, error) {
	if s, ok := m[id]; ok {
		return s, nil
	}
	return prompts.Default(id)
}
func (m promptOverrides) SavePrompt(context.Context, string, string) error { return nil }
func (m promptOverrides) ResetPrompt(context.Context, string) error        { return nil }

type promptCache map[string]production.Prepared

func (c promptCache) Get(_ context.Context, key string) (production.Prepared, error) {
	v, ok := c[key]
	if !ok {
		return v, errors.New("missing")
	}
	return v, nil
}
func (c promptCache) Put(_ context.Context, key, _ string, v production.Prepared) error {
	c[key] = v
	return nil
}
func TestEditedPromptsReachGenerationAndInvalidateShowCache(t *testing.T) {
	ctx := context.Background()
	local := &localText{reply: `{"intro":"Welcome","outro":"Goodbye"}`}
	overrides := promptOverrides{"show": "CUSTOM SHOW ", "background": " CUSTOM BACKGROUND", "selection": "CUSTOM SELECTION ", "link": "CUSTOM LINK ", "audience-link": " CUSTOM AUDIENCE", "audience": "CUSTOM REVIEW"}
	p := New(testKeys{}, promptCache{}, testAudio{})
	p.Text = local
	p.Prompts = overrides
	p.Client.Transport = transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("speech"))}, nil
	})
	for i := 0; i < 2; i++ {
		if _, err := p.Prepare(ctx, profile(), schedule.Occurrence{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(local.requests) != 1 || !strings.Contains(local.requests[0].Instructions, "CUSTOM SHOW") || !strings.Contains(local.requests[0].Instructions, "CUSTOM BACKGROUND") {
		t.Fatal(local.requests)
	}
	overrides["show"] = "UPDATED SHOW "
	if _, err := p.Prepare(ctx, profile(), schedule.Occurrence{}); err != nil {
		t.Fatal(err)
	}
	if len(local.requests) != 2 || !strings.Contains(local.requests[1].Instructions, "UPDATED SHOW") {
		t.Fatal("stale prepared speech", local.requests)
	}
	local.reply = `{"track_id":"t01"}`
	if _, err := p.SelectTrack(ctx, selectionRequest()); err != nil {
		t.Fatal(err)
	}
	q := local.requests[len(local.requests)-1]
	if !strings.Contains(q.Instructions, "CUSTOM SELECTION") || !strings.Contains(q.Instructions, profile().DJ.Prompt) {
		t.Fatal(q)
	}
	local.reply = `{"speech":"Hello Sam. Here is the music.","used_ids":["listener"]}`
	if _, _, err := p.AudienceLink(ctx, production.LinkRequest{Show: profile(), Audience: []production.AudienceItem{{ID: "listener", Kind: audience.Shoutout, Text: "Hello"}}}); err != nil {
		t.Fatal(err)
	}
	q = local.requests[len(local.requests)-1]
	if !strings.Contains(q.Instructions, "CUSTOM LINK") || !strings.Contains(q.Instructions, "CUSTOM AUDIENCE") {
		t.Fatal(q)
	}
	local.reply = `{"decisions":[{"id":"listener","decision":"approve"}]}`
	if _, err := p.Review(ctx, profile(), []production.AudienceItem{{ID: "listener", Kind: audience.Shoutout}}); err != nil {
		t.Fatal(err)
	}
	q = local.requests[len(local.requests)-1]
	if q.Instructions != "CUSTOM REVIEW" {
		t.Fatal(q)
	}
}
func TestEditedDecisionModerationPrompt(t *testing.T) {
	p := New(testKeys{}, nil, testAudio{})
	p.Prompts = promptOverrides{"audience-decisions": "CUSTOM REVIEW {{submission_id}}"}
	p.Client.Transport = transport(func(r *http.Request) (*http.Response, error) {
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Fatal(err)
		}
		q := b["questions"].([]any)[0].(map[string]any)
		if q["instructions"] != "CUSTOM REVIEW listener" {
			t.Fatal(q)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"answers":[{"type":"predicate","name":"listener","probability":0.99}]}`))}, nil
	})
	if _, err := p.Review(context.Background(), profile(), []production.AudienceItem{{ID: "listener", Kind: audience.Shoutout}}); err != nil {
		t.Fatal(err)
	}
}
