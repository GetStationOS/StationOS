package web

import (
	"errors"
	"net/http"
	"stationos.local/stationos/internal/prompts"
)

type promptsPageView struct {
	pageView
	AssistantMode string
	Definitions   []prompts.Definition
	Prompt        prompts.Definition
	Text, Problem string
}

func (s *Server) promptsPage(w http.ResponseWriter, r *http.Request) {
	v := promptsPageView{pageView: s.baseView(r), AssistantMode: "prompts", Definitions: prompts.Definitions()}
	v.Title = "Prompts"
	v.AssistantSection = "prompts"
	s.renderPage(w, r, http.StatusOK, "prompts-page", v)
}
func (s *Server) promptPage(w http.ResponseWriter, r *http.Request) {
	d, ok := prompts.Find(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if s.d.Prompts == nil {
		http.Error(w, "Prompt settings unavailable", http.StatusServiceUnavailable)
		return
	}
	v := promptsPageView{pageView: s.baseView(r), AssistantMode: "prompts", Prompt: d}
	v.Title = d.Name
	v.AssistantSection = "prompts"
	status := http.StatusOK
	saveAttempted := false
	var err error
	if r.Method == http.MethodPost {
		v.Text = r.PostForm.Get("prompt")
		switch r.PostForm.Get("action") {
		case "reset":
			saveAttempted = true
			err = s.d.Prompts.ResetPrompt(r.Context(), d.ID)
		case "save":
			err = prompts.Validate(d.ID, v.Text)
			if err == nil {
				saveAttempted = true
				err = s.d.Prompts.SavePrompt(r.Context(), d.ID, v.Text)
			}
		default:
			err = errors.New("Choose Save or Restore default")
		}
		if err == nil {
			s.redirect(w, r, "/producer/assistant/prompts/"+d.ID+"/edit?notice=prompt-saved")
			return
		}
		v.Problem = err.Error()
		if saveAttempted {
			v.Problem = "The prompt could not be saved. Your changes are retained; try again."
			s.d.Logger.Error("Save AI prompt", "prompt", d.ID, "error", err)
		}
		status = http.StatusUnprocessableEntity
		if saveAttempted {
			status = http.StatusInternalServerError
		}
	} else {
		v.Text, err = s.d.Prompts.Prompt(r.Context(), d.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	if r.URL.Query().Get("notice") == "prompt-saved" {
		v.Notice = "Prompt saved. New AI requests will use these instructions."
	}
	s.renderPage(w, r, status, "prompt-page", v)
}
