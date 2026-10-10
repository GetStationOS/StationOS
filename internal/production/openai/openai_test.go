package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
func TestCurationAndSpeechUseConfiguredProfile(t *testing.T) {
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
			if !strings.Contains(input, "Album title") || !strings.Contains(input, "180") || !strings.Contains(input, `"Performers":"Artist"`) || !strings.Contains(input, `"Composer":"Composer"`) {
				t.Fatal("missing album or duration", input)
			}
			return answer(`{"track_ids":["track"],"intro":"Welcome","outro":"Over to Clara"}`), nil
		}
		if body["speed"] != 1.15 || body["voice"] != "onyx" || body["instructions"] != "Warm and relaxed" || body["response_format"] != "mp3" {
			t.Fatal(body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("mp3"))}, nil
	})}
	d := 3 * time.Minute
	pool := []content.AudioItem{{ID: "track", Title: "Title", Artist: "Artist", Composer: "Composer", Album: "Album title", Duration: &d}}
	prepared, err := p.Prepare(context.Background(), profile(), schedule.Occurrence{Key: "next"}, pool)
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
	if _, err := p.Prepare(context.Background(), profile(), schedule.Occurrence{}, []content.AudioItem{{ID: "valid"}}); err == nil {
		t.Fatal("invented track accepted")
	}
	p.Client.Transport = transport(func(r *http.Request) (*http.Response, error) { return nil, errors.New("failed test-secret") })
	if _, err := p.Link(context.Background(), production.LinkRequest{Show: profile()}); err == nil || strings.Contains(err.Error(), "test-secret") {
		t.Fatal(err)
	}
	show := profile()
	show.DJ.HasKey = false
	if _, err := p.Prepare(context.Background(), show, schedule.Occurrence{}, nil); !errors.Is(err, production.ErrUnavailable) {
		t.Fatal(err)
	}
}
