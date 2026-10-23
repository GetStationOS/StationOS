package openai

import (
	"context"
	"io"
	"net/http"
	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/production"
	"strings"
	"testing"
)

func TestOptionalLinkSkipAndSpeechValidation(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		skip, valid bool
	}{
		{"skip", `{"speech":""}`, true, true},
		{"whitespace skip", `{"speech":"   "}`, true, true},
		{"spoken link", `{"speech":"Here is the next recording."}`, false, true},
		{"legacy speech", "Here is the next recording.", false, true},
		{"missing field", `{}`, false, false},
		{"array response", `[]`, false, false},
		{"null response", `null`, false, false},
		{"incomplete array", `[`, false, false},
		{"null speech", `{"speech":null}`, false, false},
		{"unrecognised field", `{"speech":"","reason":"skip"}`, false, false},
		{"trailing object", `{"speech":""}{}`, false, false},
		{"empty provider response", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := &localText{reply: tc.reply}
			p := New(testKeys{}, nil, testAudio{})
			p.Text = local
			speechCalls := 0
			transcript := ""
			p.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				speechCalls++
				if r.URL.Path != "/v1/audio/speech" {
					t.Fatal(r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("speech"))}, nil
			})}
			ctx := production.WithSpeechTranscript(context.Background(), func(text string) { transcript = text })
			audio, err := p.Link(ctx, production.LinkRequest{Show: profile()})
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
			if tc.skip || !tc.valid {
				if speechCalls != 0 || len(audio) != 0 || transcript != "" {
					t.Fatal("skipped or invalid link reached speech", speechCalls, string(audio), transcript)
				}
			} else if speechCalls != 1 || len(audio) == 0 || transcript != "Here is the next recording." {
				t.Fatal(speechCalls, string(audio), transcript)
			}
			if len(local.requests) != 1 || len(local.requests[0].Schema) == 0 || !strings.Contains(local.requests[0].Instructions, "Empty speech") {
				t.Fatal("missing explicit skip contract")
			}
		})
	}
}

func TestRequiredLinksCannotBeSkipped(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		request     production.LinkRequest
	}{
		{"handover", "", production.LinkRequest{Show: profile(), Handover: true}},
		{"structured handover", `{"speech":""}`, production.LinkRequest{Show: profile(), Handover: true}},
		{"audience", `{"speech":"","used_ids":["listener"]}`, production.LinkRequest{Show: profile(), Audience: []production.AudienceItem{{ID: "listener", Kind: audience.Shoutout}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := &localText{reply: tc.reply}
			p := New(testKeys{}, nil, testAudio{})
			p.Text = local
			p.Client = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
				t.Fatal("required blank speech reached TTS")
				return nil, nil
			})}
			audio, _, err := p.AudienceLink(context.Background(), tc.request)
			if err == nil || len(audio) != 0 {
				t.Fatal("required speech was skipped", err)
			}
			if !strings.Contains(local.requests[0].Instructions, "Speech is required") && !strings.Contains(local.requests[0].Instructions, "handovers require non-empty speech") {
				t.Fatal("required speech missing from prompt")
			}
		})
	}
}
