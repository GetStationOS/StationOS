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

	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/station"
)

type testLogoGenerator struct {
	available bool
	data      []byte
	err       error
	calls     int
	request   producer.ImageRequest
}

func (g *testLogoGenerator) Available(context.Context) (bool, error) { return g.available, nil }
func (g *testLogoGenerator) Generate(_ context.Context, r producer.ImageRequest) (producer.Image, error) {
	g.calls++
	g.request = r
	return producer.Image{Data: g.data}, g.err
}

func setupLogoGeneration(t *testing.T, g *testLogoGenerator) (http.Handler, persistence.Stations) {
	t.Helper()
	_, st, src, cat := setupWeb(t, "")
	h, err := New(Dependencies{Stations: st, Sources: src, Catalogue: cat, Playlists: persistence.Playlists{DB: st.DB}, Logos: g, Ready: st.DB.PingContext})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(context.Background(), station.Station{ID: "logo-test", Name: "Saved name", Description: "Saved description", Timezone: "UTC", StreamSlug: "logo-test"}); err != nil {
		t.Fatal(err)
	}
	return h, st
}
func logoForm(t *testing.T, h http.Handler) url.Values {
	return url.Values{"csrf": {csrf(t, h)}, "id": {"logo-test"}, "name": {"Draft name"}, "description": {"Draft description"}, "timezone": {"Europe/London"}, "locale": {"en-GB"}, "stream_slug": {"logo-test"}, "repeat_minutes": {"180"}, "visibility": {"unlisted"}, "generate_logo": {"yes"}}
}
func TestStationLogoGenerationIsConditionalAndUncluttered(t *testing.T) {
	for _, available := range []bool{false, true} {
		g := &testLogoGenerator{available: available}
		h, _ := setupLogoGeneration(t, g)
		for _, path := range []string{"/producer/stations/logo-test/settings", "/producer/stations/new"} {
			w := request(h, "GET", path, nil, nil)
			body := w.Body.String()
			editor := body[strings.Index(body, `id="station-logo-editor"`):]
			editor = editor[:strings.Index(editor, `<label>Station name`)]
			if strings.Contains(editor, "Generate logo") != available {
				t.Fatal("incorrect generation availability", editor)
			}
			for _, noise := range []string{"Optional", "Saved logo", "Default logo", "Choose image", "JPEG or PNG"} {
				if strings.Contains(editor, noise) {
					t.Fatal("logo control is cluttered", noise)
				}
			}
			if !strings.Contains(editor, "Upload logo") {
				t.Fatal("missing upload action")
			}
		}
	}
}
func TestStationLogoGenerationPreviewsDraftBeforeSave(t *testing.T) {
	for _, fragment := range []bool{false, true} {
		g := &testLogoGenerator{available: true, data: photoPNG(t, color.Black)}
		h, st := setupLogoGeneration(t, g)
		form := logoForm(t, h)
		var headers map[string]string
		if fragment {
			headers = map[string]string{"HX-Request": "true", "HX-Target": "station-logo-editor"}
		}
		generated := request(h, "POST", "/producer/station", form, headers)
		body := generated.Body.String()
		if generated.Code != 200 || !strings.Contains(body, `src="data:image/jpeg;base64,`) || strings.Contains(body, "ZgotmplZ") {
			t.Fatal(generated.Code, body)
		}
		if fragment && strings.Contains(body, `name="name"`) {
			t.Fatal("generation replaced other settings", body)
		}
		if !fragment && !strings.Contains(body, `value="Draft name"`) {
			t.Fatal("normal form lost draft fields", body)
		}
		if g.calls != 1 || g.request.Kind != "station" || g.request.Name != "Draft name" || g.request.Description != "Draft description" {
			t.Fatal("wrong generation request", g.request, g.calls)
		}
		saved, _ := st.Get(context.Background(), "logo-test")
		if saved.Name != "Saved name" || saved.LogoVersion != "" {
			t.Fatal("generation persisted unsaved settings", saved)
		}
		match := regexp.MustCompile(`name="generated_logo" value="([^"]+)"`).FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatal("missing generated draft", body)
		}
		form.Del("generate_logo")
		form.Set("generated_logo", html.UnescapeString(match[1]))
		result := request(h, "POST", "/producer/station", form, nil)
		saved, _ = st.Get(context.Background(), "logo-test")
		if result.Code != 303 || saved.Name != "Draft name" || saved.Description != "Draft description" || saved.LogoVersion == "" {
			t.Fatal("Save did not persist generated draft", result.Code, saved, result.Body.String())
		}
		if g.calls != 1 {
			t.Fatal("saving regenerated the logo")
		}
	}
}
func TestStationLogoGenerationFailuresPreserveStation(t *testing.T) {
	for _, test := range []struct {
		name      string
		available bool
		data      []byte
		err       error
		calls     int
	}{
		{name: "disabled"},
		{name: "provider failure", available: true, err: errors.New("Provider unavailable"), calls: 1},
		{name: "invalid image", available: true, data: []byte("invalid"), calls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := &testLogoGenerator{available: test.available, data: test.data, err: test.err}
			h, st := setupLogoGeneration(t, g)
			current, _ := st.Get(context.Background(), "logo-test")
			current.Logo = &station.LogoChange{Data: photoPNG(t, color.White)}
			original, err := (station.Service{Repo: st}).Save(context.Background(), current)
			if err != nil {
				t.Fatal(err)
			}
			result := request(h, "POST", "/producer/station", logoForm(t, h), map[string]string{"HX-Request": "true", "HX-Target": "station-logo-editor"})
			saved, _ := st.Get(context.Background(), "logo-test")
			if result.Code != 422 || saved.Name != "Saved name" || saved.LogoVersion != original.LogoVersion || g.calls != test.calls {
				t.Fatal(result.Code, saved, g.calls)
			}
		})
	}
	g := &testLogoGenerator{available: true}
	h, _ := setupLogoGeneration(t, g)
	form := logoForm(t, h)
	form.Set("csrf", "wrong")
	if w := request(h, "POST", "/producer/station", form, nil); w.Code != 403 || g.calls != 0 {
		t.Fatal("generation bypassed CSRF", w.Code)
	}
}
func TestStationLogoDraftCanBeClearedAndInvalidDraftIsRejected(t *testing.T) {
	g := &testLogoGenerator{available: true, data: photoPNG(t, color.Black)}
	h, st := setupLogoGeneration(t, g)
	form := logoForm(t, h)
	generated := request(h, "POST", "/producer/station", form, nil)
	match := regexp.MustCompile(`name="generated_logo" value="([^"]+)"`).FindStringSubmatch(generated.Body.String())
	if len(match) != 2 {
		t.Fatal(generated.Body.String())
	}
	form.Del("generate_logo")
	form.Set("generated_logo", html.UnescapeString(match[1]))
	form.Set("name", "")
	invalid := request(h, "POST", "/producer/station", form, nil)
	if invalid.Code != 422 || !strings.Contains(invalid.Body.String(), `value="`+match[1]+`"`) {
		t.Fatal("validation lost generated draft", invalid.Code)
	}
	form.Set("name", "Draft name")
	form.Set("photo_removed", "yes")
	if w := request(h, "POST", "/producer/station", form, nil); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ := st.Get(context.Background(), "logo-test")
	if saved.LogoVersion != "" {
		t.Fatal("removed draft was saved")
	}
	form.Del("photo_removed")
	form.Set("generated_logo", "invalid")
	if w := request(h, "POST", "/producer/station", form, nil); w.Code != 422 {
		t.Fatal("invalid draft accepted", w.Code)
	}
}

func TestStationLogoGenerationForNewStationRemainsUnsaved(t *testing.T) {
	g := &testLogoGenerator{available: true, data: photoPNG(t, color.Black)}
	h, st := setupLogoGeneration(t, g)
	form := logoForm(t, h)
	form.Del("id")
	form.Del("stream_slug")
	response := request(h, "POST", "/producer/station", form, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `name="generated_logo" value="/9j/`) {
		t.Fatal(response.Code, response.Body.String())
	}
	stations, err := st.List(context.Background())
	if err != nil || len(stations) != 1 {
		t.Fatal("generation created the station before Save", stations, err)
	}
}
