package openai

import (
	"context"
	"encoding/json"
	"errors"
	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
	"strings"
	"testing"
	"time"
)

type cueSpeech struct {
	capabilities  djs.SpeechCapabilities
	capabilityErr error
	selectedModel string
	requests      []production.SpeechRequest
}

func (s *cueSpeech) SpeechCapabilities(_ context.Context, model string) (djs.SpeechCapabilities, error) {
	s.selectedModel = model
	return s.capabilities, s.capabilityErr
}
func (s *cueSpeech) Identity(context.Context, string) (string, error)      { return "cue-provider", nil }
func (s *cueSpeech) Budget(context.Context, string) (time.Duration, error) { return 0, nil }
func (s *cueSpeech) Synthesize(_ context.Context, q production.SpeechRequest) ([]byte, error) {
	s.requests = append(s.requests, q)
	return []byte("audio"), nil
}
func cueCatalogue() djs.SpeechCapabilities {
	return djs.SpeechCapabilities{InlineCues: []djs.InlineCue{{Tag: "[chuckle]", Name: "Chuckle"}, {Tag: "[clear throat]", Name: "Clear throat"}}}
}
func TestSpeechWriterUsesDJPromptWithoutInjectedCueInstructions(t *testing.T) {
	for _, kind := range []string{"normal", "handover", "presentation", "audience", "disabled", "discovery failure"} {
		t.Run(kind, func(t *testing.T) {
			speech := &cueSpeech{capabilities: cueCatalogue()}
			local := &localText{reply: `{"speech":"[chuckle] What a chorus. [whisper] Here is the next record. [clear throat]"}`}
			if kind == "disabled" {
				speech.capabilities = djs.SpeechCapabilities{}
			}
			if kind == "discovery failure" {
				speech.capabilityErr = errors.New("catalogue unavailable")
			}
			p := New(noKey{}, nil, testAudio{})
			p.Speech = speech
			p.Text = local
			show := profile()
			show.DJ.Prompt = "Include [chuckle] naturally in your spoken links."
			show.DJ.SpeechConnection = "selected"
			show.DJ.SpeechModel = "server-selected-model"
			if kind == "presentation" {
				local.reply = `{"intro":"[chuckle] Welcome. [clear throat]","outro":"[clear throat] Goodbye. [whisper]"}`
				prepared, err := p.Prepare(context.Background(), show, schedule.Occurrence{})
				if err != nil || len(speech.requests) != 2 || prepared.Plan.IntroText != speech.requests[0].Text || prepared.Plan.OutroText != speech.requests[1].Text {
					t.Fatal(prepared, speech.requests, err)
				}
			} else {
				var transcript string
				ctx := production.WithSpeechTranscript(context.Background(), func(text string) { transcript = text })
				request := production.LinkRequest{Show: show, Handover: kind == "handover", Following: schedule.Occurrence{DJ: djs.Profile{Name: "Next DJ"}}}
				var err error
				if kind == "audience" {
					local.reply = `{"speech":"[chuckle] Hello Sam. [clear throat] Here is the music.","used_ids":["approved"]}`
					request.Audience = []production.AudienceItem{{ID: "approved", Kind: audience.Shoutout, Text: "Hello"}}
					_, _, err = p.AudienceLink(ctx, request)
				} else {
					_, err = p.Link(ctx, request)
				}
				if err != nil || len(speech.requests) != 1 || transcript != speech.requests[0].Text {
					t.Fatal(transcript, speech.requests, err)
				}
			}
			if speech.selectedModel != "server-selected-model" || len(local.requests) != 1 {
				t.Fatal(speech.selectedModel, local.requests)
			}
			instructions := local.requests[0].Instructions
			enabled := kind != "disabled" && kind != "discovery failure"
			if !strings.Contains(instructions, show.DJ.Prompt) || strings.Contains(instructions, "Speech delivery capabilities") || strings.Contains(instructions, "INLINE CUE TEST") || strings.Contains(instructions, `"inline_cues"`) {
				t.Fatal("DJ prompt missing or server cue instructions injected", instructions)
			}
			for _, q := range speech.requests {
				if strings.Contains(q.Text, "[whisper]") || (enabled && strings.Count(q.Text, "[") == 0) {
					t.Fatal(q.Text)
				}
				if !enabled && strings.Contains(q.Text, "[") {
					t.Fatal("cue leaked to unsupported model", q.Text)
				}
			}
		})
	}
}
func TestCueOnlyAndMalformedLinksNeverReachSynthesis(t *testing.T) {
	for _, text := range []string{"[chuckle]", "[chuckle]...", "[unknown]", "[chuckle", "Hello [bad[cue]]"} {
		p := New(noKey{}, nil, testAudio{})
		speech := &cueSpeech{capabilities: cueCatalogue()}
		p.Speech = speech
		reply, _ := json.Marshal(map[string]string{"speech": text})
		p.Text = &localText{reply: string(reply)}
		if _, err := p.Link(context.Background(), production.LinkRequest{Show: profile()}); err == nil || len(speech.requests) != 0 {
			t.Fatal(text, speech.requests, err)
		}
	}
}

func TestCapabilityChangesInvalidatePreparedSpeech(t *testing.T) {
	speech := &cueSpeech{capabilities: cueCatalogue()}
	local := &localText{reply: `{"intro":"[chuckle] Welcome.","outro":"Goodbye."}`}
	p := New(noKey{}, promptCache{}, testAudio{})
	p.Speech = speech
	p.Text = local
	for i := 0; i < 2; i++ {
		if _, err := p.Prepare(context.Background(), profile(), schedule.Occurrence{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(local.requests) != 1 || len(speech.requests) != 2 {
		t.Fatal("unchanged capabilities bypassed cache", len(local.requests), len(speech.requests))
	}
	speech.capabilities = djs.SpeechCapabilities{}
	prepared, err := p.Prepare(context.Background(), profile(), schedule.Occurrence{})
	if err != nil || len(local.requests) != 2 || len(speech.requests) != 4 || strings.Contains(prepared.Plan.IntroText, "[") {
		t.Fatal("stale cue-bearing speech reused", prepared, err)
	}
}

func TestMissingGeneratorCueRemainsAbsentInSpeechAndTranscript(t *testing.T) {
	for _, kind := range []string{"normal", "handover", "audience", "presentation"} {
		t.Run(kind, func(t *testing.T) {
			speech := &cueSpeech{capabilities: cueCatalogue()}
			local := &localText{reply: `{"speech":"Toyah brought the mystery; Kim Wilde follows with “You Came,” a bright, melodic pop rush."}`}
			p := New(noKey{}, nil, testAudio{})
			p.Speech = speech
			p.Text = local
			if kind == "presentation" {
				local.reply = `{"intro":"Welcome to the show.","outro":"Goodbye for now."}`
				prepared, err := p.Prepare(context.Background(), profile(), schedule.Occurrence{})
				if err != nil || len(speech.requests) != 2 || prepared.Plan.IntroText != speech.requests[0].Text || prepared.Plan.OutroText != speech.requests[1].Text {
					t.Fatal(prepared, speech.requests, err)
				}
			} else {
				var transcript string
				ctx := production.WithSpeechTranscript(context.Background(), func(value string) { transcript = value })
				request := production.LinkRequest{Show: profile(), Handover: kind == "handover", Following: schedule.Occurrence{DJ: djs.Profile{Name: "Next"}}}
				var err error
				if kind == "audience" {
					local.reply = `{"speech":"Hello Sam. Here is the next record.","used_ids":["approved"]}`
					request.Audience = []production.AudienceItem{{ID: "approved", Kind: audience.Shoutout, Text: "Hello"}}
					_, _, err = p.AudienceLink(ctx, request)
				} else {
					_, err = p.Link(ctx, request)
				}
				if err != nil || len(speech.requests) != 1 || transcript != speech.requests[0].Text {
					t.Fatal(transcript, speech.requests, err)
				}
			}
			for _, q := range speech.requests {
				if strings.ContainsAny(q.Text, "[]") {
					t.Fatal("cue was inserted despite generator omission", q.Text)
				}
			}
		})
	}
}
func TestCueCleaningNeverInsertsAndPreservesGeneratorPlacement(t *testing.T) {
	caps := djs.SpeechCapabilities{InlineCues: []djs.InlineCue{{Tag: "[sigh]", Name: "Sigh"}, {Tag: "[laugh]", Name: "Laugh"}}}
	for _, tc := range []struct{ input, want string }{{"Welcome.", "Welcome."}, {"[laugh] Welcome.", "[laugh] Welcome."}, {"[laugh] Welcome. [sigh]", "[laugh] Welcome. [sigh]"}, {"[unsupported] Welcome.", "Welcome."}, {"Welcome. [sigh] What a night.", "Welcome. [sigh] What a night."}} {
		got, err := cleanSpeechCues(tc.input, caps)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}
