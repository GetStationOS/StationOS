package web

import (
	"context"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stationos.local/stationos/internal/station"
)

func renderingServer(t *testing.T, source string) *Server {
	t.Helper()
	templates, err := template.New("test").Funcs(template.FuncMap{
		"fail": func() (string, error) { return "", errors.New("deliberate render failure") },
	}).Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{t: templates, d: Dependencies{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
}

func assertCleanRenderFailure(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "partial-fragment") || strings.Contains(w.Body.String(), "deliberate render failure") {
		t.Fatalf("failed rendering leaked output or diagnostics: %s", w.Body.String())
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Fatal(w.Header())
	}
}

func TestBufferedHTMLResponse(t *testing.T) {
	s := renderingServer(t, `{{define "good"}}<p>{{.}}</p>{{end}}{{define "bad"}}partial-fragment{{fail}}{{end}}`)
	for _, status := range []int{http.StatusOK, http.StatusUnprocessableEntity, http.StatusServiceUnavailable} {
		w := httptest.NewRecorder()
		if !s.renderHTML(w, status, "good", "<script>unsafe</script>") {
			t.Fatal("render failed")
		}
		if w.Code != status || w.Body.String() != "<p>&lt;script&gt;unsafe&lt;/script&gt;</p>" || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Fatal(w.Code, w.Header(), w.Body.String())
		}
		w = httptest.NewRecorder()
		if s.renderHTML(w, status, "bad", nil) {
			t.Fatal("broken template succeeded")
		}
		assertCleanRenderFailure(t, w)
	}
}

func TestBufferedPageAndShellFailures(t *testing.T) {
	for _, shell := range []string{"layout", "fragment"} {
		for _, broken := range []string{"page", shell} {
			t.Run(shell+"/"+broken, func(t *testing.T) {
				source := `{{define "page"}}<p>{{.Title}}</p>{{end}}{{define "layout"}}<!doctype html>{{.Content}}{{end}}{{define "fragment"}}<title>{{.Title}}</title>{{.Content}}{{end}}`
				// Parse the failing override separately, just as a template execution can fail at runtime.
				s := renderingServer(t, source)
				if _, err := s.t.Parse(`{{define "` + broken + `"}}partial-fragment{{fail}}{{end}}`); err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(http.MethodGet, "/producer", nil)
				if shell == "fragment" {
					r.Header.Set("HX-Request", "true")
				}
				w := httptest.NewRecorder()
				s.renderPage(w, r, http.StatusUnprocessableEntity, "page", pageView{Title: "Title"})
				assertCleanRenderFailure(t, w)
			})
		}
	}
}

func TestPageRenderingPreservesEscapingTitlesAndHistory(t *testing.T) {
	s := renderingServer(t, `{{define "page"}}<h1>{{.Title}}</h1>{{end}}{{define "layout"}}<!doctype html><title>{{.Title}}</title>{{.Content}}{{end}}{{define "fragment"}}<title>{{.Title}}</title>{{.Content}}{{end}}`)
	for _, test := range []struct{ hx, restore, full bool }{{false, false, true}, {true, false, false}, {true, true, true}} {
		r := httptest.NewRequest(http.MethodGet, "/producer", nil)
		if test.hx {
			r.Header.Set("HX-Request", "true")
		}
		if test.restore {
			r.Header.Set("HX-History-Restore-Request", "true")
		}
		w := httptest.NewRecorder()
		s.renderPage(w, r, http.StatusUnprocessableEntity, "page", pageView{Title: "<unsafe>"})
		body := w.Body.String()
		if w.Code != http.StatusUnprocessableEntity || strings.Contains(body, "<!doctype html>") != test.full || !strings.Contains(body, "<title>&lt;unsafe&gt;</title>") || !strings.Contains(body, "<h1>&lt;unsafe&gt;</h1>") || strings.Contains(body, "&amp;lt;") {
			t.Fatal(w.Code, body)
		}
	}
}

func TestFragmentHandlersNeverCommitPartialHTML(t *testing.T) {
	_, stations, sources, _ := setupWeb(t, "")
	if err := stations.Save(context.Background(), station.Station{ID: "radio", Name: "Radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		template string
		handler  func(*Server, http.ResponseWriter, *http.Request)
	}{
		{"station-live", (*Server).liveStation},
		{"station-cards", (*Server).liveCards},
		{"source-discoveries", (*Server).discoverSources},
	} {
		t.Run(test.template, func(t *testing.T) {
			s := renderingServer(t, `{{define "`+test.template+`"}}partial-fragment{{fail}}{{end}}`)
			s.d.Stations, s.d.Sources = stations, sources
			r := httptest.NewRequest(http.MethodGet, "/producer", nil)
			r.SetPathValue("id", "radio")
			r.Header.Set("HX-Request", "true")
			w := httptest.NewRecorder()
			test.handler(s, w, r)
			assertCleanRenderFailure(t, w)
		})
	}
}

func TestValidationRenderFailureCanStillReturn500(t *testing.T) {
	s := renderingServer(t, `{{define "new-station-page"}}partial-fragment{{fail}}{{end}}`)
	r := httptest.NewRequest(http.MethodPost, "/producer/station", nil)
	w := httptest.NewRecorder()
	s.stationFormError(w, r, station.Station{Name: "Retained draft"}, errors.New("invalid settings"))
	assertCleanRenderFailure(t, w)
}

func TestDialogHistoryRestoresFullDocument(t *testing.T) {
	h, stations, _, _ := setupWeb(t, "")
	if err := stations.Save(context.Background(), station.Station{ID: "radio", Name: "Radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/producer/stations/radio/delete", "/producer/djs/default-pop/delete"} {
		for _, history := range []bool{false, true} {
			headers := map[string]string{"HX-Request": "true", "HX-Target": "dialog-body"}
			if history {
				headers["HX-History-Restore-Request"] = "true"
			}
			w := request(h, http.MethodGet, path, nil, headers)
			if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "<!doctype html>") != history || !strings.Contains(w.Body.String(), `id="dialog-heading"`) {
				t.Fatalf("%s history=%v: %d %s", path, history, w.Code, w.Body.String())
			}
		}
	}
}
