package web

import (
	"context"
	"net/url"
	"stationos.local/stationos/internal/mixer"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/station"
	"strconv"
	"strings"
	"testing"
)

func TestMixerPageSaveValidationAndFallback(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	ctx := context.Background()
	if err := st.Save(ctx, station.Station{ID: "one", Name: "One", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	path := "/stations/one/mixer"
	token := csrf(t, h)
	for _, headers := range []map[string]string{nil, {"HX-Request": "true"}} {
		w := request(h, "GET", path, nil, headers)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `aria-current="page">Mixer`) || !strings.Contains(w.Body.String(), `name="music_volume"`) || !strings.Contains(w.Body.String(), `name="dj_volume"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := request(h, "POST", path, url.Values{"csrf": {token}, "music_volume": {"65"}, "dj_volume": {"200"}}, map[string]string{"HX-Request": "true"})
	if w.Code != 204 || !strings.Contains(w.Header().Get("HX-Location"), path+"?notice=mixer-saved") {
		t.Fatal(w.Code, w.Header())
	}
	repo := persistence.Mixer{DB: st.DB}
	got, err := repo.Get(ctx, "one")
	if err != nil || got != (mixer.Levels{Music: 65, DJ: 200}) {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"-1", "401", "NaN", ""} {
		w = request(h, "POST", path, url.Values{"csrf": {token}, "music_volume": {"75"}, "dj_volume": {bad}}, map[string]string{"HX-Request": "true"})
		if w.Code != 200 || !strings.Contains(w.Body.String(), `role="alert"`) || !strings.Contains(w.Body.String(), `value="75"`) {
			t.Fatal(w.Code, w.Body.String())
		}
		after, _ := repo.Get(ctx, "one")
		if after != got {
			t.Fatal("partial write", after)
		}
	}
	w = request(h, "POST", path, url.Values{"csrf": {token}, "music_volume": {"0"}, "dj_volume": {"100"}}, nil)
	if w.Code != 303 || w.Header().Get("Location") != path+"?notice=mixer-saved" {
		t.Fatal(w.Code, w.Header())
	}
	w = request(h, "POST", path, url.Values{"music_volume": {"50"}, "dj_volume": {"50"}}, nil)
	if w.Code != 403 {
		t.Fatal("missing token accepted", w.Code)
	}
	w = request(h, "GET", "/stations/missing/mixer", nil, nil)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestMixerAutosaveCommitsWithoutRedirectOrFormSwap(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	ctx := context.Background()
	if err := st.Save(ctx, station.Station{ID: "auto", Name: "Automatic", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	path := "/stations/auto/mixer"
	token := csrf(t, h)
	page := request(h, "GET", path, nil, nil)
	if strings.Contains(page.Body.String(), "Save mix") || strings.Contains(page.Body.String(), ">Cancel</a>") || strings.Contains(page.Body.String(), "data-mixer-unsaved") {
		t.Fatal("manual save controls remain", page.Body.String())
	}
	headers := map[string]string{"X-Mixer-Autosave": "true", "Accept": "application/json"}
	repo := persistence.Mixer{DB: st.DB}
	for _, v := range []mixer.Levels{{Music: 30, DJ: 160}, {Music: 0, DJ: 160}, {Music: 100, DJ: 0}} {
		w := request(h, "POST", path, url.Values{"csrf": {token}, "music_volume": {strconv.Itoa(v.Music)}, "dj_volume": {strconv.Itoa(v.DJ)}}, headers)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"saved"`) || w.Header().Get("HX-Location") != "" || w.Header().Get("Location") != "" {
			t.Fatal(w.Code, w.Header(), w.Body.String())
		}
		got, err := repo.Get(ctx, "auto")
		if err != nil || got != v {
			t.Fatal("autosave did not commit", got, err)
		}
	}
	before, _ := repo.Get(ctx, "auto")
	w := request(h, "POST", path, url.Values{"csrf": {token}, "music_volume": {"401"}, "dj_volume": {"10"}}, headers)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"error":`) {
		t.Fatal(w.Code, w.Body.String())
	}
	after, _ := repo.Get(ctx, "auto")
	if after != before {
		t.Fatal("invalid autosave partially committed", after)
	}
	w = request(h, "POST", path, url.Values{"music_volume": {"70"}, "dj_volume": {"70"}}, headers)
	if w.Code != 403 {
		t.Fatal("autosave bypassed CSRF", w.Code)
	}
	after, _ = repo.Get(ctx, "auto")
	if after != before {
		t.Fatal("unprotected autosave committed", after)
	}
}
