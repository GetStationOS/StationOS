package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"stationos.local/stationos/internal/announcements"
	"stationos.local/stationos/internal/production"
	"strings"
	"testing"
)

func TestExactAnnouncementBypassesWriterAndPreservesScript(t *testing.T) {
	p := New(testKeys{}, nil, testAudio{})
	writer := &localText{reply: "should not be called"}
	p.Text = writer
	script := "News: Sunday, 25 October. Tickets cost £12.50."
	var spoken string
	p.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/audio/speech" {
			t.Fatal("exact script sent to writer")
		}
		var body struct{ Input string }
		json.NewDecoder(r.Body).Decode(&body)
		spoken = body.Input
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("mp3"))}, nil
	})}
	var transcript string
	ctx := production.WithSpeechTranscript(context.Background(), func(s string) { transcript = s })
	audio, err := p.Announcement(ctx, announcements.Message{Mode: "exact", Text: script}, profile().DJ)
	if err != nil || len(audio) == 0 || spoken != script || transcript != script || len(writer.requests) != 0 {
		t.Fatal("verbatim contract", err, spoken, transcript, len(writer.requests))
	}
}
func TestAdaptedAnnouncementRequiresValidScript(t *testing.T) {
	for _, reply := range []string{`{"speech":"Tonight at eight: the jazz special."}`, `{"speech":""}`, `{"other":"invented"}`} {
		t.Run(reply, func(t *testing.T) {
			p := New(testKeys{}, nil, testAudio{})
			writer := &localText{reply: reply}
			p.Text = writer
			calls := 0
			p.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("mp3"))}, nil
			})}
			_, err := p.Announcement(context.Background(), announcements.Message{Mode: "adapt", Text: "Jazz special tonight at 20:00"}, profile().DJ)
			if reply == `{"speech":"Tonight at eight: the jazz special."}` {
				if err != nil || calls != 1 {
					t.Fatal(err, calls)
				}
			} else if err == nil || calls != 0 {
				t.Fatal("invalid script synthesized", err, calls)
			}
			if len(writer.requests) != 1 || !strings.Contains(writer.requests[0].Instructions, "Use only the supplied facts") {
				t.Fatal("missing factual contract")
			}
		})
	}
}

func TestAnnouncementRetainsWrittenTextWhenAudioFails(t *testing.T) {
	p := New(testKeys{}, nil, testAudio{})
	p.Text = &localText{reply: `{"speech":"The prepared words."}`}
	p.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
	})}
	var text string
	ctx, cancel := context.WithCancel(production.WithSpeechTranscript(context.Background(), func(s string) { text = s; cancelled := false; _ = cancelled }))
	defer cancel()
	_, err := p.Announcement(ctx, announcements.Message{Mode: "adapt", Text: "Brief"}, profile().DJ)
	if err == nil || text != "The prepared words." {
		t.Fatal("written text lost after synthesis failure", err, text)
	}
}
