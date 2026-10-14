package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

type localText struct {
	reply    string
	requests []ai.TextRequest
	replies  []string
	err      error
}

func (l *localText) Generate(_ context.Context, q ai.TextRequest) (string, error) {
	l.requests = append(l.requests, q)
	if l.err != nil {
		return "", l.err
	}
	if len(l.replies) > 0 {
		reply := l.replies[0]
		l.replies = l.replies[1:]
		return reply, nil
	}
	return l.reply, nil
}
func (l *localText) Identity(context.Context, string) (string, error) {
	return "local-provider-identity", nil
}
func (l *localText) Kind(context.Context, string) (string, error) { return "ollama", nil }

type noKey struct{}

func (noKey) APIKey(context.Context, string) (string, error) { return "", nil }
func TestLocalTextSeparateExternalSpeechAndKeylessCuration(t *testing.T) {
	for _, speech := range []bool{true, false} {
		t.Run(map[bool]string{true: "speech", false: "no-key"}[speech], func(t *testing.T) {
			local := &localText{reply: `{"track_ids":["t01"],"intro":"Welcome","outro":"Goodbye"}`}
			p := New(testKeys{}, nil, testAudio{})
			p.Text = local
			if !speech {
				p.Keys = noKey{}
			}
			count := 0
			p.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				count++
				if r.URL.Path != "/v1/audio/speech" || r.Header.Get("Authorization") != "Bearer test-secret" {
					t.Error("text reached speech provider")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("mp3"))}, nil
			})}
			show := profile()
			show.DJ.HasKey = speech
			show.DJ.TextReady = true
			prepared, e := p.Prepare(context.Background(), show, schedule.Occurrence{}, []content.AudioItem{{ID: "track"}})
			if e != nil || len(prepared.Plan.TrackIDs) != 1 || len(local.requests) != 1 || len(local.requests[0].Schema) == 0 {
				t.Fatal(prepared, e)
			}
			if speech && count != 2 {
				t.Fatal("speech missing")
			}
			if !speech && (count != 0 || len(prepared.Intro) > 0) {
				t.Fatal("keyless plan tried speech")
			}
			local.reply = `{"track_ids":["unoffered"],"intro":"","outro":""}`
			if _, e = p.Prepare(context.Background(), show, schedule.Occurrence{}, []content.AudioItem{{ID: "track"}}); e == nil {
				t.Fatal("unoffered track accepted")
			}
		})
	}
}
func TestLocalAudienceReviewValidationWithoutSpeechKey(t *testing.T) {
	l := &localText{}
	p := New(noKey{}, nil, testAudio{})
	p.Text = l
	items := []production.AudienceItem{{ID: "one"}, {ID: "two"}}
	for _, reply := range []string{`{"decisions":[{"id":"one","decision":"approve"},{"id":"two","decision":"uncertain"}]}`, `{"decisions":[{"id":"one","decision":"approve"},{"id":"one","decision":"approve"}]}`, `{"decisions":[{"id":"one","decision":"approve"},{"id":"other","decision":"approve"}]}`, `{"decisions":[{"id":"one","decision":"unknown"},{"id":"two","decision":"approve"}]}`} {
		l.reply = reply
		v, e := p.Review(context.Background(), schedule.Occurrence{}, items)
		var raw struct {
			Decisions []struct{ ID, Decision string }
		}
		_ = json.Unmarshal([]byte(reply), &raw)
		valid := raw.Decisions[1].Decision == "uncertain"
		if valid && (e != nil || len(v) != 2 || !v[0].Approve || !v[1].Uncertain) {
			t.Fatal(v, e)
		}
		if !valid && e == nil {
			t.Fatal("invalid review accepted", reply)
		}
	}
}

func TestLocalCurationCorrectsChoicesOnceBeforeSpeech(t *testing.T) {
	cases := []struct {
		name, first, second string
		wantErr             error
	}{
		{"unknown", `{"track_ids":["t99"],"intro":"Welcome","outro":"Bye"}`, `{"track_ids":["t02","t01"],"intro":"Welcome","outro":"Bye"}`, nil},
		{"duplicate", `{"track_ids":["t01","t01"],"intro":"Welcome","outro":"Bye"}`, `{"track_ids":["t02","t01"],"intro":"Welcome","outro":"Bye"}`, nil},
		{"still-invalid", `{"track_ids":["t01","t01"],"intro":"Welcome","outro":"Bye"}`, `{"track_ids":["t01","t01"],"intro":"Welcome","outro":"Bye"}`, production.ErrDuplicateTrack},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			l := &localText{replies: []string{test.first, test.second}}
			p := New(testKeys{}, nil, testAudio{})
			p.Text = l
			speech := 0
			p.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				speech++
				if len(l.requests) != 2 {
					t.Error("speech started before corrected plan validation")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("mp3"))}, nil
			})}
			pool := []content.AudioItem{{ID: "opaque-catalogue-track-a", Title: "First"}, {ID: "opaque-catalogue-track-b", Title: "Second"}}
			plan, err := p.Prepare(context.Background(), profile(), schedule.Occurrence{}, pool)
			if len(l.requests) != 2 {
				t.Fatal("correction was not exactly one new request", len(l.requests))
			}
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) || speech != 0 {
					t.Fatal("invalid plan reached speech", err, speech)
				}
				return
			}
			if err != nil || speech != 2 {
				t.Fatal(plan, err, speech)
			}
			var input struct{ Tracks []struct{ ID, Title string } }
			if json.Unmarshal([]byte(l.requests[0].Input), &input) != nil {
				t.Fatal("invalid request metadata")
			}
			actualByTitle := map[string]string{"First": pool[0].ID, "Second": pool[1].ID}
			byReference := map[string]string{}
			for _, track := range input.Tracks {
				byReference[track.ID] = actualByTitle[track.Title]
			}
			if len(plan.Plan.TrackIDs) != 2 || plan.Plan.TrackIDs[0] != byReference["t02"] || plan.Plan.TrackIDs[1] != byReference["t01"] {
				t.Fatal("references did not preserve the selected recordings/order", plan.Plan)
			}
			if strings.Contains(l.requests[0].Input, "opaque-catalogue-track") {
				t.Fatal("local model asked to reproduce opaque IDs")
			}
			var schema struct {
				Properties struct {
					Tracks struct {
						Max   int `json:"maxItems"`
						Items struct{ Enum []string }
					} `json:"track_ids"`
				}
			}
			if json.Unmarshal(l.requests[0].Schema, &schema) != nil || schema.Properties.Tracks.Max != 2 || len(schema.Properties.Tracks.Items.Enum) != 2 {
				t.Fatal("offered choices absent from schema", string(l.requests[0].Schema))
			}
			if !strings.Contains(l.requests[1].Instructions, "failed StationOS validation") {
				t.Fatal("correction did not explain the known validation failure")
			}
		})
	}
}
func TestLocalCurationDoesNotCorrectAmbiguousProviderFailure(t *testing.T) {
	l := &localText{err: context.DeadlineExceeded}
	p := New(noKey{}, nil, testAudio{})
	p.Text = l
	_, err := p.Prepare(context.Background(), profile(), schedule.Occurrence{}, []content.AudioItem{{ID: "track"}})
	if !errors.Is(err, context.DeadlineExceeded) || len(l.requests) != 1 {
		t.Fatal("ambiguous request replayed", err, len(l.requests))
	}
}
