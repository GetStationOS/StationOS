package web

import (
	"encoding/base64"
	"errors"
	"net/http"
	"stationos.local/stationos/internal/producer"
	"strconv"
	"strings"
	"time"

	"stationos.local/stationos/internal/djs"
)

func (s *Server) djPage(w http.ResponseWriter, r *http.Request) {
	if s.d.DJs == nil {
		http.Error(w, "DJs unavailable", 503)
		return
	}
	v := djPageView{pageView: s.baseView(r)}
	var err error
	if s.d.Assistant != nil {
		v.AssistantSettings, err = s.d.Assistant.Store.Settings(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	v.Title = "DJs"
	v.DJMode = "list"
	v.DJVoices = djs.Voices()
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
		if strings.HasSuffix(r.URL.Path, "/delete") {
			v.Title = "Delete DJ"
			v.DJMode = "delete"
		}
	} else if r.URL.Path == "/producer/djs/new" {
		v.Title = "Create DJ"
		v.DJMode = "new"
		if s.d.Assistant != nil {
			settings, e := s.d.Assistant.Store.Settings(r.Context())
			if e != nil {
				s.fail(w, e)
				return
			}
			v.AssistantSettings = settings
		}
		v.DJ.Voice = djs.DefaultVoice
		v.DJ.ConversationSpeed = djs.DefaultConversationSpeed
	} else {
		v.DJs, err = s.d.DJs.List(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	if v.DJMode == "new" || v.DJMode == "edit" {
		v.DJPhotoGeneration = s.imageGenerationAvailable(r.Context())
	}
	if v.DJMode == "list" {
		v.DJCards, err = s.djCards(r.Context(), v.DJs, time.Now())
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	s.renderDJPage(w, r, http.StatusOK, v)
}
func (s *Server) saveDJ(w http.ResponseWriter, r *http.Request) {
	if s.d.DJs == nil {
		http.Error(w, "DJs unavailable", 503)
		return
	}
	p := djs.Profile{ID: r.PostForm.Get("id"), Name: r.PostForm.Get("name"), Voice: r.PostForm.Get("voice"), Prompt: r.PostForm.Get("prompt"), Vibe: r.PostForm.Get("vibe"), Background: r.PostForm.Get("background")}
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
		if r.PostForm.Get("api_key") != "" || r.PostForm.Get("clear_key") == "yes" {
			err = errors.Join(djs.ErrInvalid, errors.New("Manage the shared OpenAI API key in Assistant settings."))
		} else if r.PostForm.Get("generate_photo") == "yes" {
			if p.Voice == "" {
				p.Voice = djs.DefaultVoice
			}
			if validation := p.Validate(); validation != nil {
				err = errors.Join(djs.ErrInvalid, validation)
			} else {
				draft, e := s.generateImageDraft(r.Context(), producer.ImageRequest{Kind: "dj", Name: p.Name, Background: boundedPortraitText(p.Background, 4000), Vibe: boundedPortraitText(p.Vibe, 2000)})
				if e != nil {
					err = errors.Join(djs.ErrInvalid, e)
				} else {
					s.renderDJPhotoDraft(w, r, p, draft)
					return
				}
			}
		} else {
			_, err = (djs.Service{Repo: s.d.DJs}).Save(r.Context(), p, djs.KeyChange{})
		}
	}
	if errors.Is(err, djs.ErrInvalid) {
		message := strings.TrimPrefix(err.Error(), djs.ErrInvalid.Error()+"\n")
		if r.Header.Get("HX-Request") == "true" {
			http.Error(w, message, 422)
			return
		}
		// Normal form fallback retains non-secret input; credentials are never echoed.
		v := djPageView{pageView: s.baseView(r)}
		uploaded := p.Photo != nil && len(p.Photo.Data) > 0
		p.Photo = nil
		v.DJ = p
		v.DJMode = "new"
		v.Title = "Create DJ"
		v.DJError = message
		v.DJPhotoGeneration = s.imageGenerationAvailable(r.Context())
		if value := r.PostForm.Get("generated_photo"); value != "" {
			if data, e := decodeImageDraft(value); e == nil {
				v.DJPhotoDraft = base64.StdEncoding.EncodeToString(data)
			}
		}
		if uploaded && v.DJPhotoDraft == "" {
			v.DJError += " Choose the photo again before saving."
		}
		v.DJVoices = djs.Voices()
		v.ExampleVibe = djs.ExampleVibe
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
		s.renderDJPage(w, r, http.StatusUnprocessableEntity, v)
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
	s.redirect(w, r, "/producer/djs?notice=dj-saved")
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
	s.redirect(w, r, "/producer/djs?notice=dj-deleted")
}

func (s *Server) renderDJPage(w http.ResponseWriter, r *http.Request, status int, v djPageView) {
	name := "dj-form-page"
	switch v.DJMode {
	case "list":
		name = "dj-list-page"
	case "delete":
		if fragmentTarget(r, "dialog-body") {
			s.renderHTML(w, status, "delete-dj", v)
			return
		}
		name = "dj-delete-page"
	}
	s.renderPage(w, r, status, name, v)
}

func boundedPortraitText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	return string(runes[:min(len(runes), limit)])
}

func (s *Server) renderDJPhotoDraft(w http.ResponseWriter, r *http.Request, p djs.Profile, draft string) {
	v := djPageView{pageView: s.baseView(r), DJ: p, DJPhotoDraft: draft, DJPhotoGeneration: true, DJVoices: djs.Voices(), ExampleVibe: djs.ExampleVibe, DJMode: "new"}
	v.Title = "Create DJ"
	if p.ID != "" {
		v.DJMode = "edit"
		v.Title = p.Name
	}
	if fragmentTarget(r, "dj-photo-editor") {
		s.renderHTML(w, http.StatusOK, "dj-photo-field", v)
		return
	}
	if s.d.Assistant != nil {
		var err error
		v.AssistantSettings, err = s.d.Assistant.Store.Settings(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	s.renderDJPage(w, r, http.StatusOK, v)
}
