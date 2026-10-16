package web

import (
	"net/http"
	"net/url"
	"stationos.local/stationos/internal/prompts"
	"strings"
	"testing"
)

func TestPromptEditorSaveResetValidationAndHistory(t *testing.T) {
	h, _, _ := setupAssistant(t, "", &assistantModel{})
	token := csrf(t, h)
	list := request(h, "GET", "/producer/assistant/prompts", nil, nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), `data-assistant-nav="prompts" aria-current="page"`) {
		t.Fatal(list.Code, list.Body.String())
	}
	for _, d := range prompts.Definitions() {
		path := "/producer/assistant/prompts/" + d.ID + "/edit"
		for _, headers := range []map[string]string{nil, {"HX-Request": "true"}, {"HX-Request": "true", "HX-History-Restore-Request": "true"}} {
			got := request(h, "GET", path, nil, headers)
			if got.Code != 200 || !strings.Contains(got.Body.String(), `name="prompt"`) {
				t.Fatal(path, got.Code, got.Body.String())
			}
			if (strings.Contains(got.Body.String(), "<!doctype html>")) != (headers == nil || headers["HX-History-Restore-Request"] == "true") {
				t.Fatal("incorrect shell", path)
			}
		}
		custom := "Custom instruction <script>unsafe()</script> & café"
		form := url.Values{"csrf": {token}, "action": {"save"}, "prompt": {custom}}
		saved := request(h, "POST", path, form, map[string]string{"HX-Request": "true"})
		if saved.Code != http.StatusNoContent || saved.Header().Get("HX-Location") == "" {
			t.Fatal(saved.Code, saved.Body.String(), saved.Header())
		}
		got := request(h, "GET", path, nil, nil)
		if !strings.Contains(got.Body.String(), "Custom instruction &lt;script&gt;") || strings.Contains(got.Body.String(), "<script>unsafe()") {
			t.Fatal("saved prompt missing or unsafe", got.Body.String())
		}
		form.Set("prompt", "   ")
		bad := request(h, "POST", path, form, nil)
		if bad.Code != 422 || !strings.Contains(bad.Body.String(), `role="alert"`) {
			t.Fatal(bad.Code, bad.Body.String())
		}
		form.Set("action", "reset")
		reset := request(h, "POST", path, form, nil)
		if reset.Code != 303 {
			t.Fatal(reset.Code, reset.Body.String())
		}
		got = request(h, "GET", path, nil, nil)
		if strings.Contains(got.Body.String(), "Custom instruction") {
			t.Fatal("default not restored")
		}
	}
	path := "/producer/assistant/prompts/assistant/edit"
	if got := request(h, "POST", path, url.Values{"action": {"save"}, "prompt": {"No token"}}, nil); got.Code != 403 {
		t.Fatal("CSRF not enforced", got.Code)
	}
	if got := request(h, "GET", "/producer/assistant/prompts/unknown/edit", nil, nil); got.Code != 404 {
		t.Fatal(got.Code)
	}
	protected, _, _ := setupAssistant(t, "password", &assistantModel{})
	if got := request(protected, "GET", path, nil, nil); got.Code != 401 {
		t.Fatal("prompt editor unauthenticated", got.Code)
	}
}
