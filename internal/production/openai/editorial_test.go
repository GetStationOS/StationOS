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
