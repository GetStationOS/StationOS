package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"strconv"

	"stationos.local/stationos/internal/djs"
)

func readSpeechForm(r *http.Request, p *djs.Profile, preview bool) error {
	draft := map[string]djs.SpeechPreference{}
	if raw := r.PostForm.Get("speech_draft"); raw != "" {
		if len(raw) > 128<<10 || json.Unmarshal([]byte(raw), &draft) != nil || len(draft) > 20 {
			return errors.New("Speech settings are unreadable; reopen the DJ")
		}
	}
	if draft == nil {
		draft = map[string]djs.SpeechPreference{}
	}
	fieldsID := r.PostForm.Get("speech_fields_connection")
	if fieldsID != "" {
		pref := djs.SpeechPreference{Model: r.PostForm.Get("speech_model"), Voice: r.PostForm.Get("voice"), Vibe: r.PostForm.Get("vibe"), Speed: 1}
		if r.PostForm.Get("speech_kind") == "chatterbox" || r.PostForm.Get("speech_kind") == "elevenlabs" {
			pref.Voice = r.PostForm.Get("speech_voice")
			pref.Vibe = ""
		} else {
			speed, err := strconv.ParseFloat(r.PostForm.Get("conversation_speed"), 64)
			if err != nil {
				return errors.New("Choose a valid conversation speed")
			}
			pref.Speed = speed
		}
		draft[fieldsID] = pref
	}
	id := r.PostForm.Get("speech_connection")
	p.SpeechDraft = draft
	if preview {
		p.SpeechPreviewConnection = id
		return nil
	}
	pref, ok := draft[id]
	if !ok {
		return errors.New("Update voice options for the selected speech endpoint before saving")
	}
	p.SpeechChange = &djs.SpeechSelection{Connection: id, Settings: pref}
	return nil
}

func (s *Server) djSpeechView(r *http.Request, v *djPageView) {
	source, ok := s.d.Voices.(djs.SpeechSource)
	if !ok {
		s.legacyDJSpeechView(r, v)
		return
	}
	v.SpeechFields = true
	endpoints, err := source.SpeechConnections(r.Context())
	v.SpeechEndpoints = endpoints
	if err != nil {
		v.SpeechVoiceError = "Could not load speech endpoints. Reopen this DJ to try again."
	}
	id := v.DJ.SpeechConnection
	if id == "" {
		id = "openai"
	}
	if v.DJ.SpeechPreviewConnection != "" {
		id = v.DJ.SpeechPreviewConnection
	}
	if v.DJ.SpeechChange != nil {
		id = v.DJ.SpeechChange.Connection
	}
	provider := "openai"
	found := false
	for _, endpoint := range endpoints {
		if endpoint.ID == id {
			provider = endpoint.Provider
			found = true
		}
	}
	if !found {
		v.SpeechEndpoints = append(v.SpeechEndpoints, djs.SpeechEndpoint{ID: id, Name: id + " (unavailable)"})
		v.SpeechVoiceError = "The saved speech endpoint is unavailable. Choose another endpoint."
	}
	v.ChatterboxVoice = provider == "chatterbox"
	v.ElevenLabsVoice = provider == "elevenlabs"
	if v.ElevenLabsVoice {
		if models, ok := source.(interface {
			SpeechModels(context.Context, string) ([]string, error)
		}); ok {
			v.SpeechModels, err = models.SpeechModels(r.Context(), id)
			if err != nil {
				v.SpeechVoiceError = "Could not load speech models; the saved model is retained."
			}
		}
	}
	v.SpeechPreference = v.DJ.Preference(id, provider)
	v.DJ.SpeechConnection = id
	if pref, ok := v.DJ.SpeechDraft[id]; ok {
		v.SpeechPreference = pref
	}
	if v.DJ.SpeechChange != nil {
		v.SpeechPreference = v.DJ.SpeechChange.Settings
	}
	catalog, err := source.Voices(djs.WithSpeechConnection(r.Context(), id))
	v.SpeechCatalog = catalog
	if err != nil {
		v.SpeechVoiceError = "Could not load speech voices. The saved choice is retained; reopen this DJ to try again."
	}
	v.SpeechVoice = v.SpeechPreference.Voice
	if v.SpeechVoice != "" {
		found := false
		for _, voice := range catalog.Voices {
			if voice.ID == v.SpeechVoice {
				found = true
			}
		}
		if !found {
			v.SpeechCatalog.Voices = append(v.SpeechCatalog.Voices, djs.Voice{ID: v.SpeechVoice, Name: v.SpeechVoice + " (unavailable)"})
		}
	}
	if v.ChatterboxVoice {
		if v.SpeechPreference.Model == "chatterbox-nano" || v.SpeechPreference.Model == "" {
			v.SpeechPreference.Model = "nano"
		}
		if models, ok := source.(djs.SpeechModelSource); ok {
			v.SpeechModelChoices, err = models.SpeechModelChoices(r.Context(), id)
			if err != nil {
				v.SpeechVoiceError = "Could not load Chatterbox models. The saved choice is retained; reopen this DJ to try again."
			}
		}
		found := false
		for _, m := range v.SpeechModelChoices {
			if m.ID == v.SpeechPreference.Model {
				found = true
			}
		}
		if !found {
			v.SpeechModelChoices = append(v.SpeechModelChoices, djs.SpeechModel{ID: v.SpeechPreference.Model, Name: v.SpeechPreference.Model + " (unavailable)"})
		}
	}
	raw, _ := json.Marshal(v.DJ.SpeechDraft)
	v.SpeechDraftJSON = string(raw)
}

// Preview switches only form controls, retaining unsaved endpoint settings and
// the rest of the DJ form. It performs no writes or generation.
func (s *Server) previewDJSpeech(w http.ResponseWriter, r *http.Request) {
	if s.d.DJs == nil {
		http.Error(w, "DJs unavailable", 503)
		return
	}
	p := djs.Profile{Voice: djs.DefaultVoice, ConversationSpeed: 1}
	id := r.PostForm.Get("id")
	if id != "" {
		saved, err := s.d.DJs.Get(r.Context(), id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		p = saved
		p.SpeechSettings = maps.Clone(saved.SpeechSettings)
	}
	p.ID = id
	p.Name = r.PostForm.Get("name")
	p.Background = r.PostForm.Get("background")
	p.Prompt = r.PostForm.Get("prompt")
	if err := readSpeechForm(r, &p, true); err != nil {
		http.Error(w, err.Error(), 422)
		return
	}
	v := djPageView{pageView: s.baseView(r), DJ: p, DJMode: "new", DJVoices: djs.Voices(), DJPhotoGeneration: s.imageGenerationAvailable(r.Context())}
	v.Title = "Create DJ"
	if id != "" {
		v.DJMode = "edit"
		v.Title = p.Name
	}
	if fragmentTarget(r, "dj-speech-settings") {
		s.djSpeechView(r, &v)
		s.renderHTML(w, 200, "dj-speech-settings", v)
		return
	}
	// Normal form fallback preserves uploads/drafts when changing provider.
	photo, err := s.readDJPhoto(r)
	if err != nil {
		v.DJError = err.Error()
	} else if photo != nil {
		v.DJ.Photo = photo
		if photo.Clear {
			v.DJ.PortraitURL = ""
		}
		if len(photo.Data) > 0 {
			v.DJPhotoDraft = base64.StdEncoding.EncodeToString(photo.Data)
		}
	}
	s.renderDJPage(w, r, 200, v)
}
