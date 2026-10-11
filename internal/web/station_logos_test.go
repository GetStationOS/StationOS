package web

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"net/url"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
)

func TestStationLogoLifecycle(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	fields := url.Values{"csrf": {csrf(t, h)}, "name": {"Logo Radio"}, "timezone": {"Europe/London"}, "locale": {"en-GB"}}
	if w := imageForm(t, h, "/station", fields, photoPNG(t, color.White), true); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	stations, err := st.List(context.Background())
	if err != nil || len(stations) != 1 {
		t.Fatal(stations, err)
	}
	a := stations[0]
	fields.Set("id", a.ID)
	logo := request(h, "GET", a.LogoURL(), nil, nil)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(logo.Body.Bytes()))
	if logo.Code != 200 || format != "jpeg" || cfg.Width != 768 || err != nil {
		t.Fatal(logo.Code, cfg, format, err)
	}
	if w := imageForm(t, h, "/station", fields, nil, false); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	retained, _ := st.Get(context.Background(), a.ID)
	if retained.LogoVersion != a.LogoVersion {
		t.Fatal("ordinary save erased logo")
	}
	fields.Set("timezone", "invalid")
	if w := imageForm(t, h, "/station", fields, photoPNG(t, color.Black), false); w.Code != 422 || !strings.Contains(w.Body.String(), "Logo Radio") {
		t.Fatal(w.Code, w.Body.String())
	}
	if got := request(h, "GET", a.LogoURL(), nil, nil); !bytes.Equal(got.Body.Bytes(), logo.Body.Bytes()) {
		t.Fatal("invalid save changed logo")
	}
	fields.Set("timezone", "UTC")
	if w := imageForm(t, h, "/station", fields, []byte("bad image"), true); w.Code != 422 {
		t.Fatal(w.Code)
	}
	if w := imageForm(t, h, "/station", fields, photoPNG(t, color.Black), true); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	replaced, _ := st.Get(context.Background(), a.ID)
	if replaced.LogoVersion == a.LogoVersion {
		t.Fatal("replacement not versioned")
	}
	fields.Set("photo_removed", "yes")
	if w := imageForm(t, h, "/station", fields, photoPNG(t, color.White), true); w.Code != 422 {
		t.Fatal("conflicting changes accepted", w.Code)
	}
	if w := imageForm(t, h, "/station", fields, nil, true); w.Code != 204 {
		t.Fatal(w.Code)
	}
	removed, _ := st.Get(context.Background(), a.ID)
	if removed.LogoVersion != "" {
		t.Fatal("logo not removed")
	}
	def := request(h, "GET", removed.LogoURL(), nil, nil)
	bundled := request(h, "GET", "/static/station-logo.jpg", nil, nil)
	if def.Code != 200 || !bytes.Equal(def.Body.Bytes(), bundled.Body.Bytes()) {
		t.Fatal("default logo missing")
	}
	fields.Del("photo_removed")
	if w := imageForm(t, h, "/station", fields, photoPNG(t, color.White), true); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if err := st.Delete(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	st.DB.QueryRow(`SELECT count(*) FROM station_logos`).Scan(&count)
	if count != 0 {
		t.Fatal("orphan logo")
	}
	if w := request(h, "GET", a.LogoURL(), nil, nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
func TestStationLogoPublicReadProtectedWrite(t *testing.T) {
	h, st, _, _ := setupWeb(t, "secret")
	a, err := (station.Service{Repo: st}).Save(context.Background(), station.Station{Name: "Public radio", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "HEAD"} {
		w := request(h, method, a.LogoURL(), nil, nil)
		if w.Code != 200 {
			t.Fatal(method, w.Code)
		}
		if method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD body")
		}
	}
	if w := request(h, "GET", "/stations/"+a.ID+"/settings", nil, nil); w.Code != 401 {
		t.Fatal("settings exposed")
	}
	if w := imageForm(t, h, "/station", nil, photoPNG(t, color.White), true); w.Code != 401 {
		t.Fatal("unprotected write")
	}
	if w := request(h, "GET", "/station-logos/missing.jpg", nil, nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
func TestICYStationLogo(t *testing.T) {
	var out bytes.Buffer
	url := "https://radio.example/station-logos/radio.jpg?v=123"
	w := icyWriter{output: &out, remaining: icyInterval, title: "Song", logo: url}
	audio := bytes.Repeat([]byte{42}, icyInterval*2)
	if _, err := w.Write(audio); err != nil {
		t.Fatal(err)
	}
	data := out.Bytes()
	n := int(data[icyInterval]) * 16
	if !strings.Contains(string(data[icyInterval+1:icyInterval+1+n]), "StreamUrl='"+url+"';") {
		t.Fatal("logo not advertised")
	}
	if !bytes.Equal(data[:icyInterval], audio[:icyInterval]) || data[len(data)-1] != 0 {
		t.Fatal("audio or unchanged metadata corrupted")
	}
	block := icyArtworkBlock(strings.Repeat("é", 5000), url)
	if len(block) > 4081 || len(block) != 1+int(block[0])*16 {
		t.Fatal("metadata overflow")
	}
}
