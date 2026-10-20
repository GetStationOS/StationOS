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
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

type localText struct {
	reply    string
	requests []ai.TextRequest
	err      error
}

func (l *localText) Generate(_ context.Context, q ai.TextRequest) (string, error) {
	l.requests = append(l.requests, q)
	if l.err != nil {
		return "", l.err
	}
	return l.reply, nil
}
func (l *localText) Identity(context.Context, string) (string, error) {
	return "local-provider-identity", nil
}
func (l *localText) Kind(context.Context, string) (string, error) { return "ollama", nil }

type noKey struct{}

func (noKey) APIKey(context.Context, string) (string, error) { return "", nil }
func TestLocalShowPresentationUsesIndependentSpeechKey(t *testing.T) {
	for _, speech := range []bool{true, false} {
		t.Run(map[bool]string{true: "speech", false: "no-key"}[speech], func(t *testing.T) {
			local := &localText{reply: `{"intro":"Welcome","outro":"Goodbye"}`}
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
			prepared, e := p.Prepare(context.Background(), show, schedule.Occurrence{})
			if !speech {
				if !errors.Is(e, production.ErrUnavailable) || count != 0 || len(local.requests) != 0 {
					t.Fatal("show presentation ran without speech credentials", e, count, local.requests)
				}
				return
			}
			if e != nil || prepared.Plan.IntroText != "Welcome" || len(local.requests) != 1 || len(local.requests[0].Schema) == 0 {
				t.Fatal(prepared, e)
			}
			if count != 2 {
				t.Fatal("speech missing")
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

func TestLocalSelectionDoesNotReplayAmbiguousProviderFailure(t *testing.T) {
	l := &localText{err: context.DeadlineExceeded}
	p := New(noKey{}, nil, testAudio{})
	p.Text = l
	_, err := p.SelectTrack(context.Background(), selectionRequest())
	if !errors.Is(err, context.DeadlineExceeded) || len(l.requests) != 1 {
		t.Fatal("ambiguous request replayed", err, len(l.requests))
	}
}

func TestDJTasksShareDatedSessionAndNewEpisodeResets(t *testing.T) {
	ctx := context.Background()
	local := &localText{reply: `{"intro":"Welcome","outro":"Goodbye"}`}
	p := New(testKeys{}, nil, testAudio{})
	p.Text = local
	p.Client = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("mp3"))}, nil
	})}
	show := profile()
	show.Key = "show:2026-10-20T09:00:00Z:1"
	if _, err := p.Prepare(ctx, show, schedule.Occurrence{}); err != nil {
		t.Fatal(err)
	}
	local.reply = "Here is the next recording."
	if _, err := p.Link(ctx, production.LinkRequest{Show: show}); err != nil {
		t.Fatal(err)
	}
	selection := selectionRequest()
	selection.Show = show
	selection.Show.Key = "show:2026-10-20T09:00:00Z:2" // template revision preserves session
	local.reply = `{"track_id":"t02"}`
	if _, err := p.SelectTrack(ctx, selection); err != nil {
		t.Fatal(err)
	}
	for _, q := range local.requests {
		if q.Session != "show:2026-10-20T09:00:00Z" {
			t.Fatal("task escaped dated session", q.Session)
		}
	}
	show.Key = "show:2026-10-21T09:00:00Z:1"
	local.reply = "New episode link"
	if _, err := p.Link(ctx, production.LinkRequest{Show: show}); err != nil {
		t.Fatal(err)
	}
	if local.requests[len(local.requests)-1].Session == local.requests[0].Session {
		t.Fatal("episode context leaked")
	}
}
