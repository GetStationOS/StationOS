package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"stationos.local/stationos/internal/producer"
)

type imageSettings struct{}

func (imageSettings) Settings(context.Context) (producer.Settings, error) {
	return producer.Settings{ImageModel: "chosen-art-model", HasKey: true}, nil
}
func (imageSettings) APIKey(context.Context) (string, error) { return "shared-key", nil }
func TestImagesUseSharedKeyIndependentModelAndBoundedSingleGeneration(t *testing.T) {
	generator := NewImages(imageSettings{})
	generator.Client.Transport = transport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/images/generations" || r.Header.Get("Authorization") != "Bearer shared-key" || body["model"] != "chosen-art-model" || body["n"] != float64(1) || body["quality"] != "low" || body["output_format"] != "jpeg" {
			t.Fatal(body, r.URL)
		}
		if !strings.Contains(body["prompt"].(string), "entirely fictional adult") {
			t.Fatal(body)
		}
		b, _ := json.Marshal(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("JPEG DATA"))}}, "usage": map[string]int{"input_tokens": 11, "output_tokens": 20}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})
	result, err := generator.Generate(context.Background(), producer.ImageRequest{Kind: "dj", Name: "Morgan Field", Background: "Fictional jazz historian"})
	if err != nil || string(result.Data) != "JPEG DATA" || result.Usage.Input != 11 {
		t.Fatal(result, err)
	}
}
func TestImageFailuresDoNotExposeBodiesOrAutomaticallyRetry(t *testing.T) {
	generator := NewImages(imageSettings{})
	calls := 0
	generator.Client.Transport = transport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"insufficient_quota","message":"private shared-key"}}`))}, nil
	})
	_, err := generator.Generate(context.Background(), producer.ImageRequest{Kind: "station", Name: "Radio"})
	if err == nil || !strings.Contains(err.Error(), "quota") || strings.Contains(err.Error(), "shared-key") || calls != 1 {
		t.Fatal(err, calls)
	}
}

type separateImageSettings struct{}

func (separateImageSettings) Settings(context.Context) (producer.Settings, error) {
	return producer.Settings{ImageModel: "chosen-art-model", HasKey: false}, nil
}
func (separateImageSettings) APIKey(context.Context) (string, error) {
	return "artwork-connection-key", nil
}
func TestArtworkUsesSelectedConnectionWithoutLegacyKey(t *testing.T) {
	g := NewImages(separateImageSettings{})
	g.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer artwork-connection-key" {
			t.Error("wrong artwork credential")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aW1hZ2U="}]}`))}, nil
	})}
	_, err := g.Generate(context.Background(), producer.ImageRequest{Kind: "station"})
	if err != nil {
		t.Fatal("artwork incorrectly required the legacy key", err)
	}
}

type availabilityImageSettings struct {
	model, key string
	err        error
}

func (s availabilityImageSettings) Settings(context.Context) (producer.Settings, error) {
	return producer.Settings{ImageModel: s.model}, s.err
}
func (s availabilityImageSettings) APIKey(context.Context) (string, error) { return s.key, s.err }
func TestImageAvailabilityUsesConfiguredModelAndCredential(t *testing.T) {
	for _, test := range []struct {
		name, model, key string
		want             bool
	}{
		{name: "not configured"},
		{name: "missing credential", model: "image-model"},
		{name: "missing model", key: "image-key"},
		{name: "configured", model: "image-model", key: "image-key", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := NewImages(availabilityImageSettings{model: test.model, key: test.key})
			available, err := g.Available(context.Background())
			if err != nil || available != test.want {
				t.Fatal(available, err)
			}
		})
	}
}
