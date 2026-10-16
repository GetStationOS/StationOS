package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/prompts"
	"strings"
	"testing"
)

type imagePrompts struct{}

func (imagePrompts) Prompt(_ context.Context, id string) (string, error) {
	if id == "logo" {
		return "CUSTOM LOGO", nil
	}
	if id == "portrait" {
		return "CUSTOM PORTRAIT", nil
	}
	return prompts.Default(id)
}
func (imagePrompts) SavePrompt(context.Context, string, string) error { return nil }
func (imagePrompts) ResetPrompt(context.Context, string) error        { return nil }
func TestArtworkUsesEditedPrompts(t *testing.T) {
	for _, kind := range []string{"station", "dj"} {
		g := NewImages(imageSettings{})
		g.Prompts = imagePrompts{}
		want := "CUSTOM LOGO"
		if kind == "dj" {
			want = "CUSTOM PORTRAIT"
		}
		g.Client.Transport = transport(func(r *http.Request) (*http.Response, error) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			s := body["prompt"].(string)
			if !strings.HasPrefix(s, want) || !strings.Contains(s, "Example") {
				t.Fatal(body)
			}
			raw, _ := json.Marshal(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("image"))}}})
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		})
		if _, err := g.Generate(context.Background(), producer.ImageRequest{Kind: kind, Name: "Example"}); err != nil {
			t.Fatal(err)
		}
	}
}
