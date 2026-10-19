package web

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
)

type producerPage interface {
	common() pageView
	player() playerView
}
type pageFrame struct {
	pageView
	Content template.HTML
	Player  playerView
}

// A history cache miss needs a complete document, even if HX-Request is present.
func fragmentRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true"
}
func fragmentTarget(r *http.Request, target string) bool {
	return target != "" && fragmentRequest(r) && r.Header.Get("HX-Target") == target
}

// Only successful server template output is promoted to trusted HTML for the shell.
// Template names come from handlers, never from request parameters.
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, status int, name string, data producerPage) {
	content, err := s.executeHTML(name, data)
	if err != nil {
		s.fail(w, err)
		return
	}
	frame := pageFrame{pageView: data.common(), Content: template.HTML(content), Player: data.player()}
	if frame.Station.ID != "" {
		frame.ActiveIncidents = s.operationalSummary(r, frame.Station.ID).NeedsReview
	}
	shell := "layout"
	if fragmentRequest(r) {
		shell = "fragment"
	}
	s.renderHTML(w, status, shell, frame)
}

// Render fully before committing headers or a status. Failed templates cannot
// leak partial fragments, nor turn a validation or rendering failure into a 200.
func (s *Server) renderHTML(w http.ResponseWriter, status int, name string, data any) bool {
	body, err := s.executeHTML(name, data)
	if err != nil {
		s.fail(w, err)
		return false
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		s.d.Logger.Error("HTML write failed", "template", name, "error", err)
		return false
	}
	return true
}

// Streaming handlers buffer each event with this helper before writing it.
func (s *Server) executeHTML(name string, data any) ([]byte, error) {
	var body bytes.Buffer
	if err := s.t.ExecuteTemplate(&body, name, data); err != nil {
		return nil, fmt.Errorf("render %s: %w", name, err)
	}
	return body.Bytes(), nil
}
