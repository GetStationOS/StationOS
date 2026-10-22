package web

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestDJFormsCredentialsAndDeletion(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	token := csrf(t, h)
	w := request(h, "GET", "/producer/djs/new", nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), `name="api_key"`) || strings.Contains(w.Body.String(), "All DJs use the curation") || strings.Contains(w.Body.String(), "Use example vibe") {
		t.Fatal(w.Code, w.Body.String())
	}
	key := "sk-test-do-not-expose"
	form := url.Values{"csrf": {token}, "name": {"<script>Night DJ</script>"}, "model": {"gpt-6.1-sol"}, "voice": {"onyx"}, "conversation_speed": {"1.15"}, "prompt": {"Introduce tracks"}, "vibe": {"Voice: Hushed"}, "background": {"Fictional record-shop curator <script>bad</script>"}, "portrait_url": {"/static/djs/alex-bright.jpg"}}
	if w = request(h, "POST", "/producer/djs", form, nil); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	var id string
	if err := st.DB.QueryRow(`SELECT id FROM djs WHERE name='<script>Night DJ</script>'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/producer/djs", "/producer/djs/" + id, "/producer/djs/" + id + "/delete"} {
		for _, headers := range []map[string]string{nil, {"HX-Request": "true"}} {
			w = request(h, "GET", path, nil, headers)
			if w.Code != 200 || strings.Contains(w.Body.String(), key) || strings.Contains(w.Body.String(), "<script>Night DJ") || strings.Contains(w.Body.String(), "<script>bad</script>") {
				t.Fatal("credential or unescaped metadata rendered", w.Code)
			}
		}
	}
	form.Set("id", id)
	form.Set("name", "Night DJ")
	form.Set("api_key", "")
	form.Set("model", "custom")
	form.Set("custom_model", "my-model-v2")
	w = request(h, "POST", "/producer/djs", form, map[string]string{"HX-Request": "true"})
	var destination struct{ Path, Target, Swap string }
	if err := json.Unmarshal([]byte(w.Header().Get("HX-Location")), &destination); err != nil {
		t.Fatal(err)
	}
	editURL := "/producer/djs/" + id + "?notice=dj-saved"
	if w.Code != 204 || destination.Path != editURL || destination.Target != "#main" || destination.Swap != "innerHTML show:none" {
		t.Fatal("DJ edit must return to its settings without scrolling", w.Code, w.Header())
	}
	if ordinary := request(h, "POST", "/producer/djs", form, nil); ordinary.Code != 303 || ordinary.Header().Get("Location") != editURL {
		t.Fatal("ordinary edit left DJ settings", ordinary.Code, ordinary.Header())
	}
	if html := request(h, "GET", editURL, nil, map[string]string{"HX-Request": "true"}).Body.String(); !strings.Contains(html, "DJ saved.") || !strings.Contains(html, "data-dj-form") {
		t.Fatal("saved DJ response lacks its edit form or success signal")
	}
	var speed float64
	st.DB.QueryRow(`SELECT conversation_speed FROM djs WHERE id=?`, id).Scan(&speed)
	if speed != 1.15 {
		t.Fatal("conversation speed not saved", speed)
	}
	var voice string
	st.DB.QueryRow(`SELECT voice FROM djs WHERE id=?`, id).Scan(&voice)
	if voice != "onyx" {
		t.Fatal("voice choice not saved")
	}
	var saved bool
	st.DB.QueryRow(`SELECT length(api_key_cipher)>0 FROM producer_settings WHERE id=1`).Scan(&saved)
	if saved {
		t.Fatal("DJ form wrote a shared key")
	}
	form.Set("voice", "unsupported")
	w = request(h, "POST", "/producer/djs", form, map[string]string{"HX-Request": "true"})
	if w.Code != 422 {
		t.Fatal("unsupported voice accepted")
	}
	st.DB.QueryRow(`SELECT voice FROM djs WHERE id=?`, id).Scan(&voice)
	if voice != "onyx" {
		t.Fatal("invalid voice altered saved choice")
	}
	form.Set("voice", "onyx")
	form.Set("name", "")
	w = request(h, "POST", "/producer/djs", form, map[string]string{"HX-Request": "true"})
	if w.Code != 422 || strings.Contains(w.Body.String(), key) {
		t.Fatal("invalid edit accepted")
	}
	w = request(h, "POST", "/producer/djs", form, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "Introduce tracks") || strings.Contains(w.Body.String(), key) {
		t.Fatal("ordinary validation lost input or exposed credential")
	}
	if w = request(h, "POST", "/producer/djs/"+id+"/delete", url.Values{"csrf": {token}}, nil); w.Code != 400 {
		t.Fatal("unconfirmed deletion")
	}
	if w = request(h, "POST", "/producer/djs/"+id+"/delete", url.Values{"confirm": {"yes"}}, nil); w.Code != 403 {
		t.Fatal("delete bypasses CSRF")
	}
	w = request(h, "GET", "/producer/djs/"+id+"/delete", nil, map[string]string{"HX-Request": "true", "HX-Target": "dialog-body"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `id="dialog-heading"`) || strings.Contains(w.Body.String(), "<!doctype") {
		t.Fatal("invalid delete dialog")
	}
	if w = request(h, "POST", "/producer/djs/"+id+"/delete", url.Values{"csrf": {token}, "confirm": {"yes"}}, nil); w.Code != 303 {
		t.Fatal(w.Code)
	}
	if w = request(h, "GET", "/producer/djs/"+id, nil, nil); w.Code != 404 {
		t.Fatal("deleted DJ remains")
	}
	var count int
	st.DB.QueryRowContext(context.Background(), `SELECT count(*) FROM djs`).Scan(&count)
	if count != 3 {
		t.Fatal("delete left data")
	}
}
func TestDJsProtectedByManagementAuthentication(t *testing.T) {
	h, _, _, _ := setupWeb(t, "secret")
	for _, path := range []string{"/producer/djs", "/producer/djs/new", "/producer/djs/missing"} {
		if w := request(h, "GET", path, nil, nil); w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
}

func TestDJFormCannotSetIndividualOrSharedKeys(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	token := csrf(t, h)
	key := "sk-ignored-dj-key"
	w := request(h, "POST", "/producer/djs", url.Values{"csrf": {token}, "id": {"default-pop"}, "name": {"Alex Bright"}, "model": {"gpt-6.1-sol"}, "voice": {"coral"}, "api_key": {key}}, nil)
	if w.Code != 422 {
		t.Fatal("DJ credential write accepted", w.Code)
	}
	var count int
	if e := st.DB.QueryRow(`SELECT (SELECT count(*) FROM djs WHERE length(api_key_cipher)>0)+(SELECT count(*) FROM producer_settings WHERE length(api_key_cipher)>0)`).Scan(&count); e != nil || count != 0 {
		t.Fatal("DJ form stored a key", e)
	}
	w = request(h, "GET", "/producer/djs/default-pop", nil, nil)
	if strings.Contains(w.Body.String(), key) || strings.Contains(w.Body.String(), `name="api_key"`) {
		t.Fatal("DJ form exposed key controls")
	}
}
