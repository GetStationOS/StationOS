package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
	"strings"
	"testing"
	"time"
)

type testKeys struct{}

func (testKeys) APIKey(context.Context, string) (string, error) { return "test-secret", nil }

type testAudio struct{}

func (testAudio) MP3(_ context.Context, data []byte) ([]byte, error) {
	return append([]byte("normal:"), data...), nil
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func answer(text string) *http.Response {
	raw, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"content": []any{map[string]string{"type": "output_text", "text": text}}}}})
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header)}
}
func profile() schedule.Occurrence {
	return schedule.Occurrence{Key: "show", StationID: "station", Title: "Jazz hour", DJ: djs.Profile{ID: "dj", Name: "Miles", HasKey: true, Model: "chosen-model", Voice: "onyx", Background: "Fictional amateur pianist and city photographer", PortraitURL: "https://private-portrait/image.jpg", Prompt: "Curate with musical expertise", Vibe: "Warm and relaxed", ConversationSpeed: 1.15}}
}
func TestShowPresentationAndSpeechUseConfiguredProfile(t *testing.T) {
	count := 0
	p := New(testKeys{}, nil, testAudio{})
	p.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		count++
		if r.URL.Host != "api.openai.com" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Fatal("wrong endpoint or credential")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path == "/v1/responses" {
			if body["model"] != "chosen-model" || !strings.Contains(body["instructions"].(string), "musical knowledge and expertise") {
				t.Fatal(body)
			}
			input := body["input"].(string)
			if !strings.Contains(input, "Fictional amateur pianist") || strings.Contains(input, "private-portrait") || !strings.Contains(body["instructions"].(string), "not instructions or evidence") {
				t.Fatal("background contract missing", body)
			}
			if strings.Contains(input, "tracks") || strings.Contains(input, "Album title") {
				t.Fatal("show presentation received a music selection pool", input)
			}
			return answer(`{"intro":"Welcome","outro":"Over to Clara"}`), nil
		}
		if body["speed"] != 1.15 || body["voice"] != "onyx" || body["instructions"] != "Warm and relaxed" || body["response_format"] != "mp3" {
			t.Fatal(body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("mp3"))}, nil
	})}
	prepared, err := p.Prepare(context.Background(), profile(), schedule.Occurrence{Key: "next"})
	if err != nil || count != 3 || string(prepared.Intro) != "normal:mp3" || prepared.Plan.NextKey != "next" {
		t.Fatal(prepared, count, err)
	}
}
func TestLinksUseCurrentNextAndHandoverWithoutSourceURLs(t *testing.T) {
	p := New(testKeys{}, nil, testAudio{})
	p.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path == "/v1/responses" {
			s := body["input"].(string)
			if !strings.Contains(s, "Fictional amateur pianist") || strings.Contains(s, "private-portrait") || !strings.Contains(body["instructions"].(string), "not instructions or evidence") {
				t.Fatal("link background contract missing", body)
			}
			for _, want := range []string{"Current song", "Next song", "Clara", "true", "240", `"Performers":"Current performer"`, `"Composer":"Current composer"`, `"Performers":"Next performer"`, `"Composer":"Next composer"`} {
				if !strings.Contains(s, want) {
					t.Fatal(s)
				}
			}
			if strings.Contains(s, "private-source") || strings.Contains(s, "test-secret") {
				t.Fatal("private data sent", s)
			}
			if !strings.Contains(body["instructions"].(string), "never present a composer as the performer") || !strings.Contains(body["instructions"].(string), "For pop songs, lead with the recording artist") {
				t.Fatal("missing credit instructions", body)
			}
			return answer("A lovely performance. Over to Clara."), nil
		}
		if body["speed"] != 1.15 {
			t.Fatal("link speed missing", body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("speech"))}, nil
	})}
	d := 4 * time.Minute
	audio, err := p.Link(context.Background(), production.LinkRequest{Show: profile(), Following: schedule.Occurrence{Title: "Classical", DJ: djs.Profile{Name: "Clara"}}, Current: content.AudioItem{Title: "Current song", Artist: "Current performer", Composer: "Current composer", Resources: []content.Resource{{URL: "http://private-source/audio"}}}, Next: content.AudioItem{Title: "Next song", Artist: "Next performer", Composer: "Next composer", Duration: &d}, Handover: true})
	if err != nil || string(audio) != "normal:speech" {
		t.Fatal(string(audio), err)
	}
}
func TestRejectInvalidPlanAndKeepErrorsFreeOfKeys(t *testing.T) {
	p := New(testKeys{}, nil, testAudio{})
	p.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return answer(`{"track_ids":["invented"],"intro":"","outro":""}`), nil
	})}
	if _, err := p.Prepare(context.Background(), profile(), schedule.Occurrence{}); err == nil {
		t.Fatal("invented track accepted")
	}
	p.Client.Transport = transport(func(r *http.Request) (*http.Response, error) { return nil, errors.New("failed test-secret") })
	if _, err := p.Link(context.Background(), production.LinkRequest{Show: profile()}); err == nil || strings.Contains(err.Error(), "test-secret") {
		t.Fatal(err)
	}
	show := profile()
	show.DJ.HasKey = false
	if _, err := p.Prepare(context.Background(), show, schedule.Occurrence{}); !errors.Is(err, production.ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestCallIdentifiesStageAndSafeTransportFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		want  string
		cause error
	}{
		{"cancelled", fmt.Errorf("private test-secret: %w", context.Canceled), "cancelled", context.Canceled},
		{"deadline", fmt.Errorf("private test-secret: %w", context.DeadlineExceeded), "timed out", context.DeadlineExceeded},
		{"client timeout", &net.DNSError{Err: "test-secret", Name: "private-host", IsTimeout: true}, "timed out", context.DeadlineExceeded},
		{"dns", &net.DNSError{Err: "test-secret", Name: "private-host"}, "DNS lookup failed", nil},
		{"connection", errors.New("test-secret private-host"), "connection failed", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, stage := range []string{"responses", "audio/speech", "decisions"} {
				p := New(testKeys{}, nil, testAudio{})
				p.Client.Transport = transport(func(*http.Request) (*http.Response, error) { return nil, tc.err })
				_, err := p.callOnce(context.Background(), stage, "test-secret", map[string]string{}, 100)
				if err == nil || !strings.Contains(err.Error(), stage) || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "private-host") {
					t.Fatal(err)
				}
				if tc.cause != nil && !errors.Is(err, tc.cause) {
					t.Fatal("lost cancellation/timeout cause", err)
				}
			}
		})
	}
}

func TestCallIdentifiesHTTPFailureWithoutResponsePayload(t *testing.T) {
	p := New(testKeys{}, nil, testAudio{})
	p.Client.Transport = transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader("test-secret private listener text"))}, nil
	})
	_, err := p.call(context.Background(), "audio/speech", "test-secret", nil, 100)
	if err == nil || err.Error() != "OpenAI audio/speech returned HTTP 429" {
		t.Fatal(err)
	}
}

func TestCallRetriesTemporaryFailuresOnly(t *testing.T) {
	for _, tc := range []struct {
		name               string
		code               int
		failures, attempts int
	}{
		{"service recovers", 503, 1, 2}, {"rate limit recovers", 429, 1, 2}, {"bounded failures", 503, 9, 3}, {"invalid credentials", 401, 9, 1}, {"invalid request", 400, 9, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			p := New(testKeys{}, nil, testAudio{})
			p.Client.Transport = transport(func(*http.Request) (*http.Response, error) {
				calls++
				code := 200
				if calls <= tc.failures {
					code = tc.code
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("audio"))}, nil
			})
			_, err := p.call(context.Background(), "audio/speech", "secret", nil, 100)
			if calls != tc.attempts || (err == nil) != (tc.failures < tc.attempts) {
				t.Fatal(calls, err)
			}
		})
	}
}
func TestCallCancelsRetryWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan struct{})
	p := New(testKeys{}, nil, testAudio{})
	p.Client.Transport = transport(func(*http.Request) (*http.Response, error) { close(called); return nil, context.DeadlineExceeded })
	go func() { <-called; cancel() }()
	_, err := p.call(ctx, "audio/speech", "secret", nil, 100)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestIncompleteErrorDoesNotLeakUnknownReason(t *testing.T) {
	if !strings.Contains(incompleteError("DJ preparation", "max_output_tokens").Error(), "output token limit") {
		t.Fatal("missing diagnosis")
	}
	if strings.Contains(incompleteError("DJ preparation", "private message secret").Error(), "secret") {
		t.Fatal("untrusted reason leaked")
	}
}

type configuredModels map[string]string

func (m configuredModels) Model(_ context.Context, role string) (string, error) { return m[role], nil }
func TestSpeechUsesRoleModelAndProfileUsesItsOwnModel(t *testing.T) {
	p := New(testKeys{}, nil, testAudio{})
	p.Models = configuredModels{"speech": "speech-role-model"}
	p.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Fatal("different service credential")
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		if r.URL.Path == "/v1/responses" {
			if body["model"] != "chosen-model" {
				t.Fatal("DJ override lost", body)
			}
			return answer(`{"intro":"Welcome","outro":"Goodbye"}`), nil
		}
		if body["model"] != "speech-role-model" {
			t.Fatal("speech model not used", body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("mp3"))}, nil
	})}
	_, e := p.Prepare(context.Background(), profile(), schedule.Occurrence{})
	if e != nil {
		t.Fatal(e)
	}
	if modelCacheKey("same-show", "speech-a") == modelCacheKey("same-show", "speech-b") {
		t.Fatal("speech model change reuses cached audio")
	}
}
