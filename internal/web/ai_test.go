package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"stationos.local/stationos/internal/inference"
	"strings"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/producer"
)

func TestProviderSettingsConnectionsAndIndependentRoleSelection(t *testing.T) {
	h, store, _ := setupAssistant(t, "", &listingAssistantModel{assistantModel: &assistantModel{}})
	token := csrf(t, h)
	id := "private-provider"
	form := url.Values{"csrf": {token}, "connection_action": {"save"}, "connection_id": {id}, "connection_name": {"My Mac"}, "provider_type": {"ollama"}, "address": {"http://192.168.1.20:11434"}, "context_tokens": {"16384"}, "output_tokens": {"2048"}, "credential": {"private-secret"}}
	w := request(h, "POST", "/producer/assistant/settings?connection="+id, form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	settings := url.Values{"csrf": {token}, "model": {"gemma4:12b"}, "curation_model": {"gemma4:12b"}, "audience_model": {"gemma4:12b"}, "speech_model": {"speech-model"}, "image_model": {"image-model"}, "assistant_connection": {id}, "curation_connection": {id}, "audience_connection": {id}, "speech_connection": {"openai"}, "image_connection": {"openai"}}
	w = request(h, "POST", "/producer/assistant/settings", settings, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	s, e := store.Settings(context.Background())
	if e != nil || s.HasKey || !s.Enabled() || s.Routes.Curation.Connection != id {
		t.Fatal(s, e)
	}
	w = request(h, "GET", "/producer/assistant", nil, nil)
	if strings.Contains(w.Body.String(), "Add an API key") || !strings.Contains(w.Body.String(), `data-assistant-enabled="true"`) {
		t.Fatal("local assistant disabled")
	}
	w = request(h, "GET", "/producer/assistant/settings?connection="+id, nil, nil)
	if strings.Contains(w.Body.String(), "private-secret") || strings.Contains(w.Body.String(), "Discover models") || strings.Contains(w.Body.String(), "Test tools and JSON") {
		t.Fatal("connection form failed")
	}
	if key, _ := store.(ai.Store).Credential(context.Background(), id); key != "private-secret" {
		t.Fatal("lost encrypted key")
	}
	w = request(h, "POST", "/producer/assistant/settings", url.Values{"csrf": {token}, "connection_action": {"delete"}, "connection_id": {id}, "confirm": {"yes"}}, nil)
	if w.Code == 303 {
		t.Fatal("deleted selected connection")
	}
}

func TestDJFormCannotOverrideGlobalModel(t *testing.T) {
	h, store, _ := setupAssistant(t, "", &assistantModel{})
	ctx := context.Background()
	settings, _ := store.Settings(ctx)
	settings.CurationModel = "gemma4:12b"
	if e := store.Configure(ctx, settings, producer.KeyChange{}); e != nil {
		t.Fatal(e)
	}
	token := csrf(t, h)
	for _, path := range []string{"/producer/djs/new", "/producer/djs/default-pop"} {
		w := request(h, "GET", path, nil, nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), `name="model"`) || strings.Contains(w.Body.String(), `name="custom_model"`) || strings.Contains(w.Body.String(), "Current model:") {
			t.Fatal("DJ form still has an override", w.Code, w.Body.String())
		}
	}
	w := request(h, "POST", "/producer/djs", url.Values{"csrf": {token}, "id": {"default-pop"}, "name": {"Alex Bright"}, "voice": {"coral"}, "model": {"obsolete-model"}, "custom_model": {"another-model"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	after, _ := store.Settings(ctx)
	if after.CurationModel != "gemma4:12b" {
		t.Fatal("DJ save changed global model")
	}
}

func TestAssistantSetupSeparatesEndpointsModelsAndPreservesRoutingOnKeySave(t *testing.T) {
	h, store, _ := setupAssistant(t, "", &listingAssistantModel{assistantModel: &assistantModel{}})
	ctx := context.Background()
	settings, _ := store.Settings(ctx)
	settings.CurationModel = "custom-dj-model"
	if err := store.Configure(ctx, settings, producer.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/producer/assistant/endpoints", "/producer/assistant/settings", "/producer/assistant"} {
		for _, headers := range []map[string]string{nil, {"HX-Request": "true"}} {
			w := request(h, "GET", path, nil, headers)
			if w.Code != 200 {
				t.Fatal(path, w.Code, w.Body.String())
			}
			html := w.Body.String()
			if path == "/producer/assistant/endpoints" && (strings.Contains(html, `name="curation_model"`) || strings.Contains(html, `name="api_key"`)) {
				t.Fatal("endpoint page contains model form", html)
			}
			if path == "/producer/assistant/settings" && (strings.Contains(html, `name="api_key"`) || !strings.Contains(html, `name="curation_model"`)) {
				t.Fatal("model page contains credentials", html)
			}
		}
	}
	w := request(h, "POST", "/producer/assistant/endpoints", url.Values{"csrf": {csrf(t, h)}, "connection_action": {"shared-key"}, "api_key": {"new-secret"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Location"), "connection=openai") {
		t.Fatal("key save left its endpoint", w.Header())
	}
	keyPage := request(h, "GET", "/producer/assistant/endpoints?connection=openai", nil, nil)
	if keyPage.Code != 200 || !strings.Contains(keyPage.Body.String(), `name="api_key"`) || strings.Contains(keyPage.Body.String(), "new-secret") {
		t.Fatal("OpenAI key form missing or leaked")
	}
	after, _ := store.Settings(ctx)
	if after.CurationModel != settings.CurationModel || after.Model != settings.Model || after.Routes != settings.Routes || !after.HasKey {
		t.Fatal("credential update changed routing", after)
	}
	w = request(h, "GET", "/producer/assistant/settings?connection=new", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-assistant-section="endpoints"`) {
		t.Fatal("legacy endpoint URL failed")
	}
}

func TestEndpointSaveDiscoversModelsWithoutGenerating(t *testing.T) {
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/api/tags" {
			t.Error("discovery generated or tested models", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[{"name":"gemma4:12b"}]}`))
	}))
	defer provider.Close()
	h, store, _ := setupAssistant(t, "", &assistantModel{})
	form := url.Values{"csrf": {csrf(t, h)}, "connection_action": {"save"}, "connection_name": {"My local endpoint"}, "connection_id": {"new-local"}, "provider_type": {"ollama"}, "address": {provider.URL}, "context_tokens": {"16384"}, "output_tokens": {"2048"}}
	w := request(h, "POST", "/producer/assistant/endpoints", form, nil)
	if w.Code != 303 || calls != 1 {
		t.Fatal(w.Code, w.Body.String(), calls)
	}
	if _, err := store.(ai.Store).Connection(context.Background(), "new-local"); err != nil {
		t.Fatal("validated endpoint not saved", err)
	}
	w = request(h, "GET", "/producer/assistant/providers/new-local/models", nil, nil)
	var result struct{ Models []string }
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || len(result.Models) != 1 || result.Models[0] != "gemma4:12b" || calls != 2 {
		t.Fatal(w.Code, w.Body.String(), calls)
	}
}

type listingAssistantModel struct{ *assistantModel }

func (m *listingAssistantModel) Check(context.Context, ai.Connection, string, string, bool) (inference.Check, error) {
	return inference.Check{Reachable: true, Models: []string{"gemma4:12b"}}, nil
}

func TestFailedEndpointSaveRetainsExistingConfiguration(t *testing.T) {
	for _, payload := range []string{"unreachable", "not-json", "{}"} {
		t.Run(payload, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if payload == "unreachable" {
					w.WriteHeader(404)
				} else {
					w.Write([]byte(payload))
				}
			}))
			defer provider.Close()
			h, store, _ := setupAssistant(t, "", &assistantModel{})
			ctx := context.Background()
			saved := ai.Connection{ID: "existing", Name: "Saved endpoint", Type: "ollama", Address: "http://saved-server:11434", ContextTokens: 16384, OutputTokens: 2048}
			if err := store.(ai.Store).SaveConnection(ctx, saved, ai.KeyChange{Value: "saved-secret"}); err != nil {
				t.Fatal(err)
			}
			form := url.Values{"csrf": {csrf(t, h)}, "connection_action": {"save"}, "connection_id": {"existing"}, "connection_name": {"Edited name"}, "provider_type": {"ollama"}, "address": {provider.URL}, "context_tokens": {"16384"}, "output_tokens": {"2048"}}
			w := request(h, "POST", "/producer/assistant/endpoints?connection=existing", form, nil)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "Could not connect") || !strings.Contains(w.Body.String(), `value="Edited name"`) {
				t.Fatal(w.Code, w.Body.String())
			}
			actual, _ := store.(ai.Store).Connection(ctx, "existing")
			key, _ := store.(ai.Store).Credential(ctx, "existing")
			if actual.Name != saved.Name || actual.Address != saved.Address || key != "saved-secret" {
				t.Fatal("failed save changed configuration", actual)
			}
		})
	}
	// Invalid addresses fail before any provider request or persistence.
	h, store, _ := setupAssistant(t, "", &listingAssistantModel{assistantModel: &assistantModel{}})
	w := request(h, "POST", "/producer/assistant/endpoints", url.Values{"csrf": {csrf(t, h)}, "connection_action": {"save"}, "connection_id": {"invalid"}, "connection_name": {"Invalid endpoint"}, "provider_type": {"ollama"}, "address": {"invalid-address"}, "context_tokens": {"16384"}, "output_tokens": {"2048"}}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Enter an HTTP or HTTPS") {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := store.(ai.Store).Connection(context.Background(), "invalid"); err == nil {
		t.Fatal(errors.New("invalid endpoint saved"))
	}
}

type credentialCheckingModel struct {
	*assistantModel
	received string
	failure  error
}

func (m *credentialCheckingModel) Check(_ context.Context, _ ai.Connection, key, _ string, _ bool) (inference.Check, error) {
	m.received = key
	return inference.Check{Reachable: true, Models: []string{"test-model"}}, m.failure
}

func TestOpenAIKeyDiscoveryUsesTheStoredClipboardNormalization(t *testing.T) {
	model := &credentialCheckingModel{assistantModel: &assistantModel{}}
	h, store, _ := setupAssistant(t, "", model)
	form := url.Values{"csrf": {csrf(t, h)}, "connection_action": {"shared-key"}, "connection_id": {"openai"}, "api_key": {" sk-project-clipboard\nwrapped-key\r\n "}}
	w := request(h, "POST", "/producer/assistant/endpoints?connection=openai", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, err := store.(ai.Store).Credential(context.Background(), "openai")
	if err != nil || saved != "sk-project-clipboardwrapped-key" || model.received != saved {
		t.Fatal("discovery and persistence normalized differently", err)
	}
}

func TestOpenAIKeySaveShowsSafeDiscoveryFailuresAndPreservesStoredKey(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		message string
	}{
		{"unauthorized", &inference.HTTPError{StatusCode: 401}, "rejected the credential (401)"},
		{"forbidden", &inference.HTTPError{StatusCode: 403}, "denied access to its model list (403)"},
		{"rate-limited", &inference.HTTPError{StatusCode: 429}, "limited the model-list request (429)"},
		{"unavailable", &inference.HTTPError{StatusCode: 503}, "temporarily unavailable"},
		{"timeout", context.DeadlineExceeded, "within 10 seconds"},
		{"unknown", errors.New("private-provider-response-with-secret"), "Could not connect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &credentialCheckingModel{assistantModel: &assistantModel{}, failure: tc.err}
			h, store, _ := setupAssistant(t, "", model)
			if err := store.SaveSettings(context.Background(), "test-model", producer.KeyChange{Value: "sk-original-secret"}); err != nil {
				t.Fatal(err)
			}
			form := url.Values{"csrf": {csrf(t, h)}, "connection_action": {"shared-key"}, "connection_id": {"openai"}, "api_key": {"sk-replacement-secret"}}
			w := request(h, "POST", "/producer/assistant/endpoints?connection=openai", form, nil)
			body := w.Body.String()
			if w.Code != 200 || !strings.Contains(body, tc.message) {
				t.Fatal(w.Code, body)
			}
			for _, secret := range []string{"sk-original-secret", "sk-replacement-secret", "private-provider-response-with-secret"} {
				if strings.Contains(body, secret) {
					t.Fatal("failure disclosed private data")
				}
			}
			saved, err := store.(ai.Store).Credential(context.Background(), "openai")
			if err != nil || saved != "sk-original-secret" {
				t.Fatal("failed validation replaced key", err)
			}
		})
	}
}

func TestChatterboxEndpointAndSpeechOnlySelection(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/health" {
			t.Error("endpoint save generated speech", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"status":"ok","model":"chatterbox-nano","device":"cpu"}`))
	}))
	defer provider.Close()
	h, _, _ := setupAssistant(t, "", &assistantModel{})
	form := url.Values{"csrf": {csrf(t, h)}, "connection_action": {"save"}, "connection_name": {"Nano"}, "connection_id": {"nano"}, "provider_type": {"chatterbox"}, "address": {provider.URL}, "context_tokens": {"16384"}, "output_tokens": {"2048"}}
	w := request(h, "POST", "/producer/assistant/endpoints", form, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/producer/assistant/settings", nil, nil)
	body := w.Body.String()
	if strings.Count(body, `value="nano"`) != 1 || !strings.Contains(body, `data-provider-kind="chatterbox"`) {
		t.Fatal("Nano must be offered only for speech", body)
	}
	if !strings.Contains(body, "DJ voice, vibe and speed settings apply to OpenAI only") {
		t.Fatal("default voice capabilities missing")
	}
}
