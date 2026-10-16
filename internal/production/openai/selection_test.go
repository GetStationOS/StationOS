package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/production"
)

func selectionRequest() production.SelectionRequest {
	return production.SelectionRequest{Show: profile(), Current: content.AudioItem{ID: "current", Title: "Current recording"}, Candidates: []content.AudioItem{{ID: "opaque-a", Title: "First", Artist: "Artist", Resources: []content.Resource{{URL: "http://private-source"}}}, {ID: "opaque-b", Title: "Second"}}, RemainingSeconds: 240}
}
func TestLocalSingleTrackChoice(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		valid       bool
	}{
		{"valid", `{"track_id":"t02"}`, true},
		{"unoffered", `{"track_id":"t99"}`, false},
		{"batch", `{"track_ids":["t01","t02"]}`, false},
		{"malformed", `{`, false},
		{"trailing", `{"track_id":"t01"} {}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := &localText{reply: tc.reply}
			p := New(noKey{}, nil, testAudio{})
			p.Text = local
			id, err := p.SelectTrack(context.Background(), selectionRequest())
			if (err == nil) != tc.valid || (tc.valid && id != "opaque-b") || len(local.requests) != 1 {
				t.Fatal(id, err, local.requests)
			}
			q := local.requests[0]
			for _, want := range []string{"Current recording", "First", "Artist", "240", "Fictional amateur pianist"} {
				if !strings.Contains(q.Input, want) {
					t.Fatal("missing musical context", q.Input)
				}
			}
			for _, secret := range []string{"opaque-", "private-source", "private-portrait"} {
				if strings.Contains(q.Input, secret) {
					t.Fatal("private metadata in selection", q.Input)
				}
			}
			if q.Role != "curation" || !strings.Contains(q.Instructions, "Curate with musical expertise") || !strings.Contains(string(q.Schema), `"enum":["t01","t02"]`) {
				t.Fatal(q)
			}
		})
	}
}
func TestLunaSingleTrackDecision(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		valid       bool
	}{
		{"valid", `{"answers":[{"type":"choice","name":"next_track","choice":"t02"}]}`, true},
		{"unknown", `{"answers":[{"type":"choice","name":"next_track","choice":"t99"}]}`, false},
		{"refused", `{"answers":[{"type":"refusal","name":"next_track"}]}`, false},
		{"missing", `{"answers":[]}`, false},
		{"wrong-question", `{"answers":[{"type":"choice","name":"other","choice":"t02"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := New(testKeys{}, nil, testAudio{})
			p.Models = configuredModels{"curation": "gpt-6-luna"}
			calls := 0
			p.Client.Transport = transport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/v1/decisions" || r.Header.Get("Authorization") != "Bearer test-secret" {
					t.Fatal(r.URL)
				}
				var body struct {
					Model, Input string
					Questions    []struct {
						Type, Name, Instructions string
						Choices                  []struct{ Value string }
					}
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "gpt-6-luna" || len(body.Questions) != 1 || len(body.Questions[0].Choices) != 2 || body.Questions[0].Type != "choice" {
					t.Fatal(body)
				}
				if strings.Contains(body.Input, "private-source") || !strings.Contains(body.Questions[0].Instructions, "Curate with musical expertise") {
					t.Fatal(body)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.reply))}, nil
			})
			id, err := p.SelectTrack(context.Background(), selectionRequest())
			if (err == nil) != tc.valid || (tc.valid && id != "opaque-b") || calls != 1 {
				t.Fatal(id, err, calls)
			}
		})
	}
}
func TestOneEligibleTrackNeedsNoInference(t *testing.T) {
	p := New(noKey{}, nil, testAudio{})
	local := &localText{}
	p.Text = local
	q := selectionRequest()
	q.Candidates = q.Candidates[:1]
	id, err := p.SelectTrack(context.Background(), q)
	if err != nil || id != "opaque-a" || len(local.requests) != 0 {
		t.Fatal(id, err)
	}
}

type selectionRoutes struct {
	configuredModels
	ai.Store
}

func (selectionRoutes) Routes(context.Context) (ai.Routes, error) {
	return ai.Routes{Curation: ai.Selection{Connection: "curation"}, Speech: ai.Selection{Connection: "speech"}}, nil
}
func (selectionRoutes) Credential(_ context.Context, id string) (string, error) {
	if id == "curation" {
		return "curation-key", nil
	}
	return "", nil
}
func TestDecisionUsesCurationCredentialWithoutSpeechKey(t *testing.T) {
	p := New(noKey{}, nil, testAudio{})
	p.Models = selectionRoutes{configuredModels: configuredModels{"curation": "gpt-6-luna"}}
	p.Client.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer curation-key" {
			t.Fatal("speech credential used for curation")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"answers":[{"type":"choice","name":"next_track","choice":"t01"}]}`))}, nil
	})
	id, err := p.SelectTrack(context.Background(), selectionRequest())
	if err != nil || id != "opaque-a" {
		t.Fatal(id, err)
	}
}

func TestOtherOpenAIModelUsesConstrainedSingleChoice(t *testing.T) {
	p := New(testKeys{}, nil, testAudio{})
	calls := 0
	p.Client.Transport = transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v1/responses" {
			t.Fatal("unsupported model sent to Decisions", r.URL)
		}
		var body struct {
			Model string
			Text  struct {
				Format struct{ Schema json.RawMessage }
			}
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "chosen-model" || !strings.Contains(string(body.Text.Format.Schema), `"enum":["t01","t02"]`) {
			t.Fatal(body)
		}
		return answer(`{"track_id":"t02"}`), nil
	})
	id, err := p.SelectTrack(context.Background(), selectionRequest())
	if err != nil || id != "opaque-b" || calls != 1 {
		t.Fatal(id, err, calls)
	}
}
