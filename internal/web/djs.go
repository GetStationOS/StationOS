package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"stationos.local/stationos/internal/djs"
)

type djModel struct{ ID, Label string }

// Presets checked against https://developers.openai.com/api/docs/models on 2026-10-09.
func djModels() []djModel {
	return []djModel{{"gpt-6.1-sol", "GPT-6.1 Sol"}, {"gpt-6-luna", "GPT-6 Luna"}, {"gpt-6-astra", "GPT-6 Astra"}}
}
func knownDJModel(id string) bool {
	for _, m := range djModels() {
		if m.ID == id {
			return true
		}
	}
	return false
}
func (s *Server) djPage(w http.ResponseWriter, r *http.Request) {
	if s.d.DJs == nil {
		http.Error(w, "DJs unavailable", 503)
		return
	}
	v, err := s.baseView(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.Title = "DJs"
	v.DJMode = "list"
	v.DJVoices = djs.Voices()
	v.DJModels = djModels()
	v.ExampleVibe = djs.ExampleVibe
	switch r.URL.Query().Get("notice") {
	case "dj-saved":
		v.Notice = "DJ saved."
	case "dj-deleted":
		v.Notice = "DJ deleted."
	}
	id := r.PathValue("id")
	if id != "" {
		v.DJ, err = s.d.DJs.Get(r.Context(), id)
		if errors.Is(err, djs.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		v.Title = v.DJ.Name
		v.DJMode = "edit"
		v.DJCustomModel = !knownDJModel(v.DJ.Model)
		if strings.HasSuffix(r.URL.Path, "/delete") {
			v.Title = "Delete DJ"
			v.DJMode = "delete"
		}
	} else if r.URL.Path == "/djs/new" {
		v.Title = "Create DJ"
		v.DJMode = "new"
		v.DJ.Model = "gpt-6.1-sol"
		v.DJ.Voice = djs.DefaultVoice
		v.DJ.ConversationSpeed = djs.DefaultConversationSpeed
	} else {
		v.DJs, err = s.d.DJs.List(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	s.render(w, r, v)
}
func (s *Server) saveDJ(w http.ResponseWriter, r *http.Request) {
	if s.d.DJs == nil {
		http.Error(w, "DJs unavailable", 503)
		return
	}
	model := r.PostForm.Get("model")
	if model == "custom" {
		model = r.PostForm.Get("custom_model")
	}
	p := djs.Profile{ID: r.PostForm.Get("id"), Name: r.PostForm.Get("name"), Model: model, Voice: r.PostForm.Get("voice"), Prompt: r.PostForm.Get("prompt"), Vibe: r.PostForm.Get("vibe"), Background: r.PostForm.Get("background")}
	p.ConversationSpeed = djs.DefaultConversationSpeed
	if p.ID != "" {
		saved, err := s.d.DJs.Get(r.Context(), p.ID)
		if errors.Is(err, djs.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		p.PortraitURL = saved.PortraitURL
	}
	photo, err := s.readDJPhoto(r)
	p.Photo = photo
	if raw := r.PostForm.Get("conversation_speed"); raw != "" {
		var speedErr error
		p.ConversationSpeed, speedErr = strconv.ParseFloat(raw, 64)
		if speedErr != nil || p.ConversationSpeed == 0 {
			if err == nil {
				err = errors.Join(djs.ErrInvalid, errors.New("choose a conversation speed between 0.25× and 4.00×"))
			}
			p.ConversationSpeed = djs.DefaultConversationSpeed
		}
	}
	if err == nil {
		_, err = (djs.Service{Repo: s.d.DJs}).Save(r.Context(), p, djs.KeyChange{Value: r.PostForm.Get("api_key"), Clear: r.PostForm.Get("clear_key") == "yes"})
	}
	if errors.Is(err, djs.ErrInvalid) {
		message := strings.TrimPrefix(err.Error(), djs.ErrInvalid.Error()+"\n")
		if r.Header.Get("HX-Request") == "true" {
			http.Error(w, message, 422)
			return
		}
		// Normal form fallback retains non-secret input; credentials are never echoed.
		v, viewErr := s.baseView(r)
		if viewErr != nil {
			s.fail(w, viewErr)
			return
		}
		uploaded := p.Photo != nil && len(p.Photo.Data) > 0
		p.Photo = nil
		v.DJ = p
		v.DJMode = "new"
		v.Title = "Create DJ"
		v.DJError = message + " Re-enter an API key if you supplied one."
		if uploaded {
			v.DJError += " Choose the photo again before saving."
		}
		v.DJVoices = djs.Voices()
		v.DJModels = djModels()
		v.ExampleVibe = djs.ExampleVibe
		v.DJCustomModel = !knownDJModel(p.Model)
		if p.ID != "" {
			saved, getErr := s.d.DJs.Get(r.Context(), p.ID)
			if getErr != nil {
				s.fail(w, getErr)
				return
			}
			v.DJ.HasKey = saved.HasKey
			v.DJMode = "edit"
			v.Title = p.Name
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(422)
		s.render(w, r, v)
		return
	}
	if errors.Is(err, djs.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.redirect(w, r, "/djs?notice=dj-saved")
}
func (s *Server) deleteDJ(w http.ResponseWriter, r *http.Request) {
	if s.d.DJs == nil {
		http.Error(w, "DJs unavailable", 503)
		return
	}
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "confirm DJ deletion", 400)
		return
	}
	err := s.d.DJs.Delete(r.Context(), r.PathValue("id"))
	if errors.Is(err, djs.ErrInUse) {
		http.Error(w, err.Error(), 409)
		return
	}
	if errors.Is(err, djs.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.redirect(w, r, "/djs?notice=dj-deleted")
}
