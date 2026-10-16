package web

import (
	"context"
	"errors"
	"html"
	"image/color"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/persistence"
)

func setupDJPhotoGeneration(t *testing.T, g *testLogoGenerator) (http.Handler, persistence.DJs, djs.Profile) {
	t.Helper()
	_, st, src, cat := setupWeb(t, "")
	repo, err := persistence.NewDJs(st.DB, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	p, err := (djs.Service{Repo: repo}).Save(context.Background(), djs.Profile{Name: "Saved DJ", Background: "Saved background", Vibe: "Saved vibe", PortraitURL: "/static/djs/alex-bright.jpg"}, djs.KeyChange{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Dependencies{Images: g, DJs: repo, Stations: st, Sources: src, Catalogue: cat, Playlists: persistence.Playlists{DB: st.DB}, Ready: st.DB.PingContext})
	if err != nil {
		t.Fatal(err)
	}
	return h, repo, p
}
func djPhotoForm(t *testing.T, h http.Handler, id string) url.Values {
	return url.Values{"csrf": {csrf(t, h)}, "id": {id}, "name": {"Draft DJ"}, "background": {"A fictional jazz presenter"}, "vibe": {"Warm late-night delivery"}, "prompt": {"Select thoughtful jazz recordings"}, "voice": {"marin"}, "conversation_speed": {"1.25"}, "generate_photo": {"yes"}}
}
func photoDraft(t *testing.T, body string) string {
	t.Helper()
	found := regexp.MustCompile(`name="generated_photo" value="([^"]+)"`).FindStringSubmatch(body)
	if len(found) != 2 {
		t.Fatal("missing photo draft", body)
	}
	return html.UnescapeString(found[1])
}
func TestDJPhotoEditorMatchesStationActionsAndAvailability(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		h, _, p := setupDJPhotoGeneration(t, &testLogoGenerator{available: enabled})
		for _, path := range []string{"/producer/djs/" + p.ID, "/producer/djs/new"} {
			w := request(h, "GET", path, nil, nil)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			body := w.Body.String()
			start := strings.Index(body, `id="dj-photo-editor"`)
			if start < 0 {
				t.Fatal("missing shared editor", body)
			}
			editor := body[start:]
			editor = editor[:strings.Index(editor, `<label>Name`)]
			if strings.Contains(editor, `aria-label="Generate photo"`) != enabled {
				t.Fatal("incorrect generation availability", editor)
			}
			if !strings.Contains(editor, `aria-label="Upload photo"`) || strings.Contains(editor, "Optional") || strings.Contains(editor, "photo-help") || strings.Contains(editor, "dj-photo-remove") {
				t.Fatal("incorrect photo actions", editor)
			}
		}
	}
}
func TestDJPhotoGenerationIsUnsavedAndPreservesProfile(t *testing.T) {
	for _, fragment := range []bool{false, true} {
		g := &testLogoGenerator{available: true, data: photoPNG(t, color.Black)}
		h, repo, p := setupDJPhotoGeneration(t, g)
		form := djPhotoForm(t, h, p.ID)
		var headers map[string]string
		if fragment {
			headers = map[string]string{"HX-Request": "true", "HX-Target": "dj-photo-editor"}
		}
		generated := request(h, "POST", "/producer/djs", form, headers)
		body := generated.Body.String()
		if generated.Code != 200 || !strings.Contains(body, `src="data:image/jpeg;base64,`) || strings.Contains(body, "ZgotmplZ") {
			t.Fatal(generated.Code, body)
		}
		if fragment && strings.Contains(body, `name="background"`) {
			t.Fatal("generation replaced other profile fields")
		}
		if !fragment && (!strings.Contains(body, "A fictional jazz presenter") || !strings.Contains(body, `value="Draft DJ"`)) {
			t.Fatal("normal generation lost form edits", body)
		}
		if g.calls != 1 || g.request.Kind != "dj" || g.request.Name != "Draft DJ" || g.request.Background != "A fictional jazz presenter" || g.request.Vibe != "Warm late-night delivery" {
			t.Fatal(g.calls, g.request)
		}
		saved, _ := repo.Get(context.Background(), p.ID)
		if saved.Name != p.Name || saved.PortraitURL != p.PortraitURL {
			t.Fatal("generation saved the DJ prematurely", saved)
		}
		form.Del("generate_photo")
		form.Set("generated_photo", photoDraft(t, body))
		result := request(h, "POST", "/producer/djs", form, nil)
		saved, _ = repo.Get(context.Background(), p.ID)
		if result.Code != 303 || saved.Name != "Draft DJ" || saved.Background != form.Get("background") || saved.Vibe != form.Get("vibe") || saved.Prompt != form.Get("prompt") || saved.Voice != "marin" || saved.ConversationSpeed != 1.25 || !strings.HasPrefix(saved.PortraitURL, "/djs/"+p.ID+"/photo?v=") {
			t.Fatal("Save lost photo or profile edits", result.Code, saved, result.Body.String())
		}
		if g.calls != 1 {
			t.Fatal("Save generated another image")
		}
	}
}
func TestDJPhotoGenerationFailuresPreserveSavedPhoto(t *testing.T) {
	for _, test := range []struct {
		enabled bool
		data    []byte
		err     error
		calls   int
	}{
		{},
		{enabled: true, err: errors.New("Provider unavailable"), calls: 1},
		{enabled: true, data: []byte("not an image"), calls: 1},
	} {
		g := &testLogoGenerator{available: test.enabled, data: test.data, err: test.err}
		h, repo, p := setupDJPhotoGeneration(t, g)
		response := request(h, "POST", "/producer/djs", djPhotoForm(t, h, p.ID), map[string]string{"HX-Request": "true", "HX-Target": "dj-photo-editor"})
		saved, _ := repo.Get(context.Background(), p.ID)
		if response.Code != 422 || saved.PortraitURL != p.PortraitURL || saved.Name != p.Name || g.calls != test.calls {
			t.Fatal(response.Code, saved, g.calls)
		}
	}
}
func TestDJPhotoDraftValidationRemovalAndCreation(t *testing.T) {
	g := &testLogoGenerator{available: true, data: photoPNG(t, color.Black)}
	h, repo, p := setupDJPhotoGeneration(t, g)
	form := djPhotoForm(t, h, p.ID)
	generated := request(h, "POST", "/producer/djs", form, nil)
	draft := photoDraft(t, generated.Body.String())
	form.Del("generate_photo")
	form.Set("generated_photo", draft)
	form.Set("name", "")
	invalid := request(h, "POST", "/producer/djs", form, nil)
	if invalid.Code != 422 || photoDraft(t, invalid.Body.String()) == "" {
		t.Fatal("validation lost photo draft", invalid.Code)
	}
	form.Set("name", "Draft DJ")
	form.Set("photo_removed", "yes")
	cleared := request(h, "POST", "/producer/djs", form, nil)
	saved, _ := repo.Get(context.Background(), p.ID)
	if cleared.Code != 303 || saved.PortraitURL != "" {
		t.Fatal("draft removal failed", cleared.Code, saved)
	}
	form.Del("photo_removed")
	form.Set("generated_photo", "invalid")
	if w := request(h, "POST", "/producer/djs", form, nil); w.Code != 422 {
		t.Fatal("invalid draft accepted", w.Code)
	}
	form = djPhotoForm(t, h, "")
	before, _ := repo.List(context.Background())
	created := request(h, "POST", "/producer/djs", form, nil)
	if created.Code != 200 || photoDraft(t, created.Body.String()) == "" {
		t.Fatal(created.Code)
	}
	after, _ := repo.List(context.Background())
	if len(before) != len(after) {
		t.Fatal("generation created the DJ before Save")
	}
	form.Set("csrf", "wrong")
	calls := g.calls
	if w := request(h, "POST", "/producer/djs", form, nil); w.Code != 403 || g.calls != calls {
		t.Fatal("generation bypassed CSRF", w.Code)
	}
}
