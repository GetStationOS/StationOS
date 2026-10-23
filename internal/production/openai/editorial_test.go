package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

type speechMemory struct{}

func (speechMemory) Context(context.Context, schedule.Occurrence) (string, error) {
	return `{"Theme":"Evening","Notes":"Keep it gentle","Plan":["private-plan"],"Aired":["full-history"],"recent_session_turns":["unbroadcast draft"],"recent_aired_speech":["A familiar glowing ballad, written by Joseph Brooks."]}`, nil
}
func (speechMemory) Aired(context.Context, schedule.Occurrence, content.AudioItem, string) error {
	return nil
}
func (speechMemory) Remember(context.Context, schedule.Occurrence, string, string) error { return nil }

func TestSpeechRequestsUseOnlyRelevantContext(t *testing.T) {
	for _, tc := range []struct {
		name                string
		handover, presenter bool
	}{
		{"normal", false, true}, {"handover", true, true}, {"continuous", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := &localText{reply: `{"speech":"Over to Sarah."}`}
			p := New(testKeys{}, nil, testAudio{})
			p.Text = local
			p.Session = speechMemory{}
			p.Prompts = promptOverrides{"link": "CUSTOM OLDER LINK PROMPT: introduce the incoming recording, then hand over."}
			p.Client = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("speech"))}, nil
			})}
			following := schedule.Occurrence{Title: "Afternoon Lounge"}
			if tc.presenter {
				following.DJ = djs.Profile{ID: "sarah", Name: "Sarah"}
			}
			_, err := p.Link(context.Background(), production.LinkRequest{Show: profile(), Following: following, Handover: tc.handover, Current: content.AudioItem{ID: "current", Title: "Previous", Artist: "Artist"}, Next: content.AudioItem{ID: "next", Title: "Upcoming"}})
			if err != nil {
				t.Fatal(err)
			}
			q := local.requests[0]
			var input map[string]json.RawMessage
			if err = json.Unmarshal([]byte(q.Input), &input); err != nil {
				t.Fatal(err)
			}
			if !q.Options.Stateless || !strings.Contains(q.Input, "Joseph Brooks") || !strings.Contains(q.Input, "Keep it gentle") {
				t.Fatal(q)
			}
			for _, unneeded := range []string{"private-plan", "full-history", "unbroadcast draft", "remaining_seconds", "next_track_id", "playout_sequence", "audience"} {
				if strings.Contains(q.Input, unneeded) {
					t.Fatal("irrelevant context", unneeded, q.Input)
				}
			}
			if !strings.Contains(q.Instructions, "CUSTOM OLDER LINK PROMPT") || !strings.Contains(q.Instructions, "overrides conflicting editorial instructions") {
				t.Fatal(q.Instructions)
			}
			_, current := input["current"]
			_, next := input["next"]
			_, incoming := input["following_dj"]
			if current == tc.handover || next == tc.handover || incoming != (tc.handover && tc.presenter) {
				t.Fatal(q.Input)
			}
			if tc.handover {
				if !strings.Contains(q.Instructions, "Do not introduce any recording") || strings.Contains(q.Instructions, "Composer only as writer") {
					t.Fatal(q.Instructions)
				}
			} else if strings.Contains(q.Instructions, "following_dj") || !strings.Contains(q.Instructions, "Composer only as writer") {
				t.Fatal(q.Instructions)
			}
		})
	}
}

type restartedSpeechMemory struct{ speechMemory }

func (restartedSpeechMemory) Context(context.Context, schedule.Occurrence) (string, error) {
	return `{"Theme":"Afternoon lounge","Notes":"A relaxed mix","recent_aired_speech":["Here are the rich acoustic textures of Buena Vista Social Club."]}`, nil
}
func TestRestartLinkUsesCurrentRecordingWithoutPriorRunRemarks(t *testing.T) {
	local := &localText{reply: `{"speech":"Ahmad Jamal Trio, followed by Earth, Wind and Fire."}`}
	p := New(testKeys{}, nil, testAudio{})
	p.Text = local
	p.Session = restartedSpeechMemory{}
	p.Prompts = promptOverrides{"link": "Acknowledge the last recording discussed in the conversation."}
	p.Client = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("speech"))}, nil
	})}
	req := production.LinkRequest{FreshStart: true, Show: profile(), Current: content.AudioItem{ID: "ahmad", Title: "The Surrey With The Fringe On Top", Artist: "Ahmad Jamal Trio"}, Next: content.AudioItem{ID: "ewf", Title: "Got to Get You Into My Life", Artist: "Earth, Wind & Fire"}}
	for _, fresh := range []bool{true, false} {
		req.FreshStart = fresh
		if _, err := p.Link(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		q := local.requests[len(local.requests)-1]
		var input struct {
			Current, Next speechRecording
			Episode       speechCheckpoint `json:"episode_session"`
		}
		if err := json.Unmarshal([]byte(q.Input), &input); err != nil {
			t.Fatal(err)
		}
		if input.Current.Performers != "Ahmad Jamal Trio" || input.Next.Performers != "Earth, Wind & Fire" || input.Episode.Notes != "A relaxed mix" {
			t.Fatal(q.Input)
		}
		if strings.Contains(q.Input, "Buena Vista") == fresh {
			t.Fatal("prior-run wording leaked into first link or normal memory was lost", q.Input)
		}
		if !q.Options.Stateless || !strings.Contains(q.Instructions, "Acknowledge only current as just played") || !strings.Contains(q.Instructions, "not playback order") {
			t.Fatal(q)
		}
	}
}
