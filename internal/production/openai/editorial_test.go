package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

type speechMemory struct{}

func (speechMemory) Context(context.Context, schedule.Occurrence) (string, error) {
	return `{"recent_aired_speech":["A familiar glowing ballad, written by Joseph Brooks."]}`, nil
}
func (speechMemory) Aired(context.Context, schedule.Occurrence, content.AudioItem, string) error {
	return nil
}
func (speechMemory) Remember(context.Context, schedule.Occurrence, string, string) error { return nil }

func TestLinkCarriesActualPlaybackSequenceDespiteSavedPrompt(t *testing.T) {
	for _, tc := range []struct {
		name                string
		handover, presenter bool
		sequence            []string
	}{{"normal", false, true, []string{"spoken_link", "next_recording"}}, {"handover", true, true, []string{"outgoing_signoff", "incoming_show_introduction", "next_recording"}}, {"continuous", true, false, []string{"outgoing_signoff", "continuous_music"}}} {
		t.Run(tc.name, func(t *testing.T) {
			local := &localText{reply: "Over to Sarah."}
			p := New(testKeys{}, nil, testAudio{})
			p.Text = local
			p.Session = speechMemory{}
			p.Prompts = promptOverrides{"link": "CUSTOM OLDER LINK PROMPT: introduce the incoming recording, then hand over. "}
			p.Client = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("speech"))}, nil
			})}
			following := schedule.Occurrence{Title: "Afternoon Lounge"}
			if tc.presenter {
				following.DJ = djs.Profile{ID: "sarah", Name: "Sarah"}
			}
			_, err := p.Link(context.Background(), production.LinkRequest{Show: profile(), Following: following, Handover: tc.handover, Current: content.AudioItem{ID: "current"}, Next: content.AudioItem{ID: "next"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(local.requests) != 1 {
				t.Fatal(local.requests)
			}
			request := local.requests[0]
			var payload struct {
				Sequence  []string `json:"playout_sequence"`
				Session   string   `json:"episode_session"`
				Presenter string   `json:"next_recording_presenter"`
			}
			if err := json.Unmarshal([]byte(request.Input), &payload); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(payload.Sequence, tc.sequence) || !strings.Contains(payload.Session, "Joseph Brooks") {
				t.Fatal(payload)
			}
			if tc.handover && tc.presenter && payload.Presenter != "Sarah" {
				t.Fatal(payload)
			}
			if !strings.Contains(request.Instructions, "CUSTOM OLDER LINK PROMPT") || !strings.Contains(request.Instructions, "overrides conflicting timing requests") || !strings.Contains(request.Instructions, "Do not repeat the same composer or album facts") {
				t.Fatal("saved prompt did not receive playback and continuity contracts", request.Instructions)
			}
		})
	}
}
