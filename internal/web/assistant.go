package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/station"
)

func (s *Server) assistantPage(w http.ResponseWriter, r *http.Request) {
	s.assistantRender(w, r, "", "")
}
func (s *Server) assistantRender(w http.ResponseWriter, r *http.Request, message, problem string) {
	if s.d.Assistant == nil {
		http.Error(w, "Assistant unavailable", 503)
		return
	}
	v, err := s.baseView(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.AssistantMode = "chat"
	v.Title = "Producer assistant"
	v.Section = "assistant"
	v.AssistantInput = message
	v.AssistantError = problem
	v.AssistantRequestID = producer.ID()
	v.AssistantSettings, err = s.d.Assistant.Store.Settings(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	id := r.URL.Query().Get("conversation")
	stationID := r.URL.Query().Get("station")
	if id != "" {
		v.Conversation, err = s.d.Assistant.Store.Get(r.Context(), id)
		if errors.Is(err, producer.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		stationID = v.Conversation.StationID
	}
	if stationID != "" {
		v.Station, err = s.d.Stations.Get(r.Context(), stationID)
		if err != nil && !errors.Is(err, station.ErrNotFound) {
			s.fail(w, err)
			return
		}
		if errors.Is(err, station.ErrNotFound) && id == "" {
			http.NotFound(w, r)
			return
		}
		v.StationID = stationID
	}
	v.Conversations, err = s.d.Assistant.Store.List(r.Context(), stationID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if id == "" && len(v.Conversations) > 0 {
		v.Conversation, err = s.d.Assistant.Store.Get(r.Context(), v.Conversations[0].ID)
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	switch v.Conversation.Status {
	case "running":
		v.AssistantState = "Working"
		if activity, active := s.d.Assistant.Activity(v.Conversation.ID); active {
			v.AssistantActivity = activity.Stage
			v.AssistantTiming = fmt.Sprintf("Running for %s · Current activity: %s · %d checks and actions finished", assistantDuration(time.Since(activity.Started)), assistantDuration(time.Since(activity.Changed)), activity.Finished)
			if activity.Usage.Input > 0 || activity.Usage.Output > 0 {
				v.AssistantTiming += fmt.Sprintf(" · AI tokens: %d input, %d output", activity.Usage.Input, activity.Usage.Output)
			}
		} else {
			v.AssistantState = "Stopped unexpectedly"
			v.AssistantActivity = "There is no active worker for this request. Stop the assistant to save a paused state, then continue the saved request."
		}
	case "failed":
		v.AssistantState = "Paused"
		v.AssistantActivity = "The assistant has stopped. Review its last message, then use Continue saved request to resume. Completed changes remain saved."
	case "idle":
		if len(v.Conversation.Task.Blockers) > 0 {
			v.AssistantState = "Needs your input"
			v.AssistantActivity = "The assistant has finished the work it can do. Reply with the information requested below."
		} else if len(v.Conversation.Messages) > 0 {
			v.AssistantState = "Finished"
			v.AssistantActivity = "The assistant has finished this request. Review its reply and the completed actions below."
		}
	}
	for _, message := range v.Conversation.Messages {
		rendered := assistantMessage{Role: message.Role, Text: message.Text, URL: message.URL}
		if message.Role == "assistant" {
			rendered.HTML = assistantMarkdown(message.Text)
		}
		v.AssistantMessages = append(v.AssistantMessages, rendered)
	}
	if r.PathValue("id") != "" {
		// Poll only the messages/status region, preserving the compose form and focus.
		var body strings.Builder
		if err = s.t.ExecuteTemplate(&body, "assistant-messages", v); err != nil {
			s.fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body.String()))
		return
	}
	s.render(w, r, v)
}
func assistantURL(id string) string { return "/producer/assistant?conversation=" + url.QueryEscape(id) }
func (s *Server) assistantMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	q.Set("conversation", r.PathValue("id"))
	r.URL.RawQuery = q.Encode()
	s.assistantRender(w, r, "", "")
}
func (s *Server) newConversation(w http.ResponseWriter, r *http.Request) {
	if s.d.Assistant == nil {
		http.Error(w, "Assistant unavailable", 503)
		return
	}
	stationID := r.PostForm.Get("station")
	if stationID != "" {
		if _, e := s.d.Stations.Get(r.Context(), stationID); e != nil {
			http.NotFound(w, r)
			return
		}
	}
	id, e := s.d.Assistant.Create(r.Context(), stationID)
	if e != nil {
		s.fail(w, e)
		return
	}
	s.redirect(w, r, assistantURL(id))
}
func (s *Server) assistantSend(w http.ResponseWriter, r *http.Request) {
	if s.d.Assistant == nil {
		http.Error(w, "Assistant unavailable", 503)
		return
	}
	id := r.PostForm.Get("conversation")
	text := r.PostForm.Get("message")
	if id == "" {
		requestID := r.PostForm.Get("request_id")
		if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(requestID) {
			http.Error(w, "Invalid message identifier; reload the page", 400)
			return
		}
		stationID := r.PostForm.Get("station")
		if stationID != "" {
			if _, e := s.d.Stations.Get(r.Context(), stationID); e != nil {
				http.NotFound(w, r)
				return
			}
		}
		// The first form submission has a stable ID, so retries cannot create a second conversation.
		id = requestID
		existing, e := s.d.Assistant.Store.Get(r.Context(), id)
		if errors.Is(e, producer.ErrNotFound) {
			e = s.d.Assistant.Store.Create(r.Context(), producer.Conversation{ID: id, StationID: stationID, Title: "New conversation"})
			if e != nil {
				existing, e = s.d.Assistant.Store.Get(r.Context(), id)
			}
		}
		if e != nil {
			s.fail(w, e)
			return
		}
		if existing.ID != "" && existing.StationID != stationID {
			http.Error(w, "Conversation context changed; reload the page", 409)
			return
		}
	}

	err := s.d.Assistant.Start(r.Context(), id, r.PostForm.Get("request_id"), text)
	if errors.Is(err, producer.ErrDuplicate) {
		s.redirect(w, r, assistantURL(id))
		return
	}
	if err != nil {
		if errors.Is(err, producer.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		q.Set("conversation", id)
		r.URL.RawQuery = q.Encode()
		r.URL.Path = "/producer/assistant"
		s.assistantRender(w, r, text, err.Error())
		return
	}
	s.redirect(w, r, assistantURL(id))
}
func (s *Server) assistantSettings(w http.ResponseWriter, r *http.Request) {
	if s.d.Assistant == nil {
		http.Error(w, "Assistant unavailable", 503)
		return
	}
	v, e := s.baseView(r)
	if e != nil {
		s.fail(w, e)
		return
	}
	v.AssistantMode = "settings"
	v.Title = "Assistant settings"
	v.AssistantSettings, e = s.d.Assistant.Store.Settings(r.Context())
	if e != nil {
		s.fail(w, e)
		return
	}
	if r.Method == http.MethodPost {
		model := strings.TrimSpace(r.PostForm.Get("model"))
		k := producer.KeyChange{Value: strings.TrimSpace(r.PostForm.Get("api_key")), Clear: r.PostForm.Get("clear_key") == "yes"}
		e = producer.ValidateSettings(model, k)
		if e == nil {
			settings := v.AssistantSettings
			settings.Model = model
			if r.PostForm.Has("curation_model") {
				settings.CurationModel = strings.TrimSpace(r.PostForm.Get("curation_model"))
			}
			if r.PostForm.Has("speech_model") {
				settings.SpeechModel = strings.TrimSpace(r.PostForm.Get("speech_model"))
			}
			if r.PostForm.Has("image_model") {
				settings.ImageModel = strings.TrimSpace(r.PostForm.Get("image_model"))
			}
			if r.PostForm.Has("audience_model") {
				settings.AudienceModel = strings.TrimSpace(r.PostForm.Get("audience_model"))
			}
			v.AssistantSettings = settings
			e = s.d.Assistant.Store.Configure(r.Context(), settings, k)
		}
		if e != nil {
			v.AssistantSettings.Model = model
			v.AssistantError = e.Error()
		} else {
			s.redirect(w, r, "/producer/assistant/settings?notice=assistant-saved")
			return
		}
	}
	if r.URL.Query().Get("notice") == "assistant-saved" {
		v.Notice = "Assistant settings saved."
	}
	s.render(w, r, v)
}

func (s *Server) assistantStop(w http.ResponseWriter, r *http.Request) {
	if s.d.Assistant == nil {
		http.Error(w, "Assistant unavailable", 503)
		return
	}
	id := r.PathValue("id")
	if err := s.d.Assistant.Stop(r.Context(), id); err != nil {
		if errors.Is(err, producer.ErrNotFound) {
			http.NotFound(w, r)
		} else {
			s.fail(w, err)
		}
		return
	}
	s.redirect(w, r, assistantURL(id))
}
func (s *Server) assistantContinue(w http.ResponseWriter, r *http.Request) {
	if s.d.Assistant == nil {
		http.Error(w, "Assistant unavailable", 503)
		return
	}
	id := r.PathValue("id")
	err := s.d.Assistant.Start(r.Context(), id, r.PostForm.Get("request_id"), "Continue the saved request.")
	if errors.Is(err, producer.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil && !errors.Is(err, producer.ErrDuplicate) {
		q := r.URL.Query()
		q.Set("conversation", id)
		r.URL.RawQuery = q.Encode()
		r.URL.Path = "/producer/assistant"
		s.assistantRender(w, r, "", err.Error())
		return
	}
	s.redirect(w, r, assistantURL(id))
}

func assistantDuration(d time.Duration) string {
	seconds := max(0, int(d.Seconds()))
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
}
