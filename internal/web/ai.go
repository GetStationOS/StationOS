package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/inference"
	"stationos.local/stationos/internal/producer"
)

type aiRoleView struct {
	Name, Label, Connection, Model, ModelField string
	Text                                       bool
}

func roleViews(s producer.Settings) []aiRoleView {
	out := []aiRoleView{}
	for _, r := range []struct{ Name, Label string }{{"assistant", "Producer assistant"}, {"curation", "Show planning and DJ writing"}, {"audience", "Audience review"}, {"image", "Artwork"}} {
		v := s.Routes.For(r.Name)
		model, field := s.Model, "model"
		switch r.Name {
		case "curation":
			model, field = s.CurationModel, "curation_model"
		case "audience":
			model, field = s.AudienceModel, "audience_model"
		case "speech":
			model, field = s.SpeechModel, "speech_model"
		case "image":
			model, field = s.ImageModel, "image_model"
		}
		out = append(out, aiRoleView{Name: r.Name, Label: r.Label, Connection: v.Connection, Model: model, ModelField: field, Text: r.Name != "speech" && r.Name != "image"})
	}
	return out
}
func routesFromForm(r *http.Request) ai.Routes {
	get := func(role string) ai.Selection {
		return ai.Selection{Connection: r.PostForm.Get(role + "_connection")}
	}
	return ai.Routes{Assistant: get("assistant"), Curation: get("curation"), Audience: get("audience"), Speech: get("speech"), Image: get("image")}
}
func (s *Server) aiConnection(w http.ResponseWriter, r *http.Request, v assistantPageView) {
	store, ok := s.d.Assistant.Store.(ai.Store)
	if !ok {
		http.Error(w, "Provider settings unavailable", 503)
		return
	}
	id := r.PostForm.Get("connection_id")
	action := r.PostForm.Get("connection_action")
	var err error
	if action == "shared-key" {
		v.AIConnectionMode = true
		v.AIConnection, err = store.Connection(r.Context(), "openai")
		if err != nil {
			s.fail(w, err)
			return
		}
		v.AIConnectionSaved = true
		v.Title = v.AIConnection.Name
		keyChange := producer.NormalizeKey(producer.KeyChange{Value: r.PostForm.Get("api_key"), Clear: r.PostForm.Get("clear_key") == "yes"})
		err = producer.ValidateSettings(v.AssistantSettings.Model, keyChange)
		if err == nil && !keyChange.Clear {
			key := keyChange.Value
			if key == "" {
				key, err = store.Credential(r.Context(), "openai")
			}
			if err == nil {
				_, err = s.availableModels(r.Context(), v.AIConnection, key)
			}
		}
		if err == nil {
			err = storeKey(s, r, v.AssistantSettings)
		}
		if err == nil {
			s.redirect(w, r, "/producer/assistant/endpoints?connection=openai&notice=key-saved")
			return
		}
		v.AssistantError = err.Error()
		s.renderAssistantPage(w, r, http.StatusOK, v)
		return
	}
	v.AIConnectionMode = true
	if action == "save" {
		ctxTokens, _ := strconv.Atoi(r.PostForm.Get("context_tokens"))
		output, _ := strconv.Atoi(r.PostForm.Get("output_tokens"))
		timeout, _ := strconv.Atoi(r.PostForm.Get("request_timeout_seconds"))
		c := ai.Connection{ID: id, Name: strings.TrimSpace(r.PostForm.Get("connection_name")), Type: r.PostForm.Get("provider_type"), Address: strings.TrimRight(strings.TrimSpace(r.PostForm.Get("address")), "/"), ContextTokens: ctxTokens, OutputTokens: output, RequestTimeoutSeconds: timeout}
		v.AIConnection = c
		change := ai.KeyChange{Value: strings.TrimSpace(r.PostForm.Get("credential")), Clear: r.PostForm.Get("clear_credential") == "yes"}
		err = c.Validate()
		if err == nil && (len(change.Value) > 4096 || strings.IndexFunc(change.Value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 || (change.Clear && change.Value != "")) {
			err = errors.New("Enter one credential or remove the saved credential")
		}
		if saved, e := store.Connection(r.Context(), id); e == nil {
			v.AIConnection.HasKey = saved.HasKey
			v.AIConnectionSaved = true
		}
		if err == nil {
			key := change.Value
			if key == "" && !change.Clear && v.AIConnection.HasKey {
				key, err = store.Credential(r.Context(), id)
			}
			if err == nil {
				_, err = s.availableModels(r.Context(), c, key)
			}
		}
		if err == nil {
			err = store.SaveConnection(r.Context(), c, change)
		}
		if err == nil {
			s.redirect(w, r, "/producer/assistant/endpoints?notice=endpoint-saved&connection="+id)
			return
		}
	} else if action == "delete" {
		if r.PostForm.Get("confirm") != "yes" {
			http.Error(w, "Confirm connection deletion", 400)
			return
		}
		v.AIConnection, _ = store.Connection(r.Context(), id)
		err = store.DeleteConnection(r.Context(), id)
		if err == nil {
			s.redirect(w, r, "/producer/assistant/endpoints")
			return
		}
	} else {
		http.Error(w, "Invalid provider action", 400)
		return
	}
	if err != nil {
		v.AssistantError = err.Error()
	}
	v.Title = "Add AI endpoint"
	if v.AIConnectionSaved {
		v.Title = v.AIConnection.Name
	}
	s.renderAssistantPage(w, r, http.StatusOK, v)
}

func storeKey(s *Server, r *http.Request, settings producer.Settings) error {
	key := producer.KeyChange{Value: strings.TrimSpace(r.PostForm.Get("api_key")), Clear: r.PostForm.Get("clear_key") == "yes"}
	if err := producer.ValidateSettings(settings.Model, key); err != nil {
		return err
	}
	return s.d.Assistant.Store.Configure(r.Context(), settings, key)
}

func (s *Server) deleteAIPage(w http.ResponseWriter, r *http.Request) {
	if s.d.Assistant == nil {
		http.Error(w, "Assistant unavailable", 503)
		return
	}
	store, ok := s.d.Assistant.Store.(ai.Store)
	if !ok {
		http.Error(w, "Provider settings unavailable", 503)
		return
	}
	v := assistantPageView{pageView: s.baseView(r)}
	var err error
	v.AIConnection, err = store.Connection(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	v.AssistantMode = "delete-connection"
	v.Section = "assistant"
	v.Title = "Delete provider connection"
	s.renderAssistantPage(w, r, http.StatusOK, v)
}

// Model discovery reads provider metadata; it never generates or downloads models.
func (s *Server) discoverModels(w http.ResponseWriter, r *http.Request) {
	if s.d.Assistant == nil {
		s.json(w, 503, map[string]any{"error": "AI endpoints are unavailable."})
		return
	}
	store, ok := s.d.Assistant.Store.(ai.Store)
	if !ok {
		s.json(w, 503, map[string]any{"error": "AI endpoints are unavailable."})
		return
	}
	c, err := store.Connection(r.Context(), r.PathValue("id"))
	if err != nil {
		s.json(w, 404, map[string]any{"error": "This endpoint no longer exists."})
		return
	}
	key, err := store.Credential(r.Context(), c.ID)
	if err != nil {
		s.json(w, 400, map[string]any{"error": "The endpoint credential is unavailable."})
		return
	}
	models, err := s.availableModels(r.Context(), c, key)
	if err != nil {
		s.json(w, 502, map[string]any{"error": err.Error()})
		return
	}
	s.json(w, 200, map[string]any{"models": models})
}

func (s *Server) availableModels(parent context.Context, c ai.Connection, key string) ([]string, error) {
	if key == "" && (c.Type == "openai" || c.Type == "anthropic" || c.Type == "gemini") {
		return nil, errors.New("Enter a credential for this endpoint to discover its models.")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	result, err := s.modelListing(ctx, c, key)
	if err != nil {
		var providerError *inference.HTTPError
		if errors.As(err, &providerError) {
			switch providerError.StatusCode {
			case http.StatusUnauthorized:
				return nil, errors.New("The endpoint rejected the credential (401). Check that you pasted the complete API key, then save again.")
			case http.StatusForbidden:
				return nil, errors.New("The endpoint denied access to its model list (403). Check the key’s permissions and provider access settings.")
			case http.StatusTooManyRequests:
				return nil, errors.New("The endpoint limited the model-list request (429). Check provider usage limits and try saving again shortly.")
			default:
				if providerError.StatusCode >= 500 {
					return nil, errors.New("The endpoint is temporarily unavailable. Try saving again shortly.")
				}
			}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, errors.New("The endpoint did not return its model list within 10 seconds. Try saving again.")
		}
		return nil, errors.New("Could not connect to the endpoint or discover its models. Check the provider, server address and credential, then save again.")
	}
	models := []string{}
	for _, id := range result.Models {
		if ai.ValidModel(id) {
			models = append(models, id)
		}
	}
	return models, nil
}

func (s *Server) modelListing(ctx context.Context, c ai.Connection, key string) (inference.Check, error) {
	if checker, ok := s.d.Assistant.Model.(interface {
		Check(context.Context, ai.Connection, string, string, bool) (inference.Check, error)
	}); ok {
		return checker.Check(ctx, c, key, "", false)
	}
	return inference.NewClient().Check(ctx, c, key, "", false)
}
