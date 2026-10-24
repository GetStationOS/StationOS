package web

import (
	"context"
	"net/http"
	"net/url"
	"stationos.local/stationos/internal/announcements"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
	"time"
)

func announcementWeb(t *testing.T) (http.Handler, persistence.Announcements) {
	t.Helper()
	_, st, _, _ := setupWeb(t, "")
	ctx := context.Background()
	if err := st.Save(ctx, station.Station{ID: "station", Name: "Test radio", Timezone: "Europe/London", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	repo := persistence.Announcements{DB: st.DB}
	dj, err := persistence.NewDJs(st.DB, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Dependencies{Announcements: repo, Stations: st, DJs: dj, Schedule: persistence.Schedule{DB: st.DB}})
	if err != nil {
		t.Fatal(err)
	}
	return h, repo
}
func TestAnnouncementWebJourneyAndValidation(t *testing.T) {
	h, repo := announcementWeb(t)
	base := "/producer/stations/station/announcements"
	token := csrf(t, h)
	for _, path := range []string{base, base + "/new", base + "/settings"} {
		w := request(h, "GET", path, nil, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Announcements") {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	values := url.Values{"csrf": {token}, "version": {"0"}, "title": {"Sunday special"}, "category": {"promotion"}, "text": {"Jazz this Sunday at eight."}, "mode": {"exact"}, "status": {"draft"}, "repeat": {"once"}, "starts": {time.Now().Add(-time.Hour).Format("2006-01-02T15:04")}, "expires": {time.Now().AddDate(0, 0, 7).Format("2006-01-02T15:04")}}
	bad := url.Values{}
	for k, v := range values {
		bad[k] = append([]string(nil), v...)
	}
	bad.Set("expires", "bad")
	w := request(h, "POST", base+"/new", bad, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), values.Get("text")) {
		t.Fatal("input lost", w.Code, w.Body.String())
	}
	w = request(h, "POST", base+"/new", values, map[string]string{"HX-Request": "true"})
	if w.Code != 204 || !strings.Contains(w.Header().Get("HX-Location"), "notice=saved") {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	items, err := repo.List(context.Background(), "station")
	if err != nil || len(items) != 1 || items[0].Status != "draft" || items[0].Plays != 0 {
		t.Fatal(items, err)
	}
	m := items[0]
	edit := base + "/" + m.ID
	w = request(h, "GET", edit, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Preview saved message") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", edit+"/action", url.Values{"csrf": {token}, "version": {"1"}, "action": {"next"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	m, _ = repo.Get(context.Background(), "station", m.ID)
	if !m.Next || m.Status != "active" {
		t.Fatal(m)
	}
	values.Set("version", "1")
	w = request(h, "POST", edit, values, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), announcements.ErrConflict.Error()) {
		t.Fatal("stale save accepted", w.Code, w.Body.String())
	}
	w = request(h, "GET", edit+"/delete", nil, map[string]string{"HX-Request": "true", "HX-Target": "dialog-body"})
	if w.Code != 200 || strings.Contains(w.Body.String(), "<!doctype") || !strings.Contains(w.Body.String(), "dialog-heading") {
		t.Fatal("delete fragment", w.Code, w.Body.String())
	}
	w = request(h, "POST", edit+"/delete", url.Values{"csrf": {token}, "version": {"2"}, "confirm": {"yes"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", base+"/new", values, nil)
	if w.Code != 422 {
		t.Fatal("invalid create version accepted", w.Code)
	}
}
func TestAnnouncementFragmentsAndSecurity(t *testing.T) {
	h, _ := announcementWeb(t)
	base := "/producer/stations/station/announcements"
	w := request(h, "GET", base, nil, map[string]string{"HX-Request": "true", "HX-Target": "announcements-results"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `id="announcements-results"`) || strings.Contains(w.Body.String(), "<h1") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", base+"/settings", url.Values{"gap": {"5"}, "version": {"0"}}, nil)
	if w.Code != 403 {
		t.Fatal("missing csrf accepted", w.Code)
	}
	w = request(h, "GET", "/producer/stations/other/announcements", nil, nil)
	if w.Code != 404 {
		t.Fatal("unknown station", w.Code)
	}
}

type announcementPreviewProducer struct{ calls int }

func (p *announcementPreviewProducer) Announcement(ctx context.Context, m announcements.Message, dj djs.Profile) ([]byte, error) {
	p.calls++
	production.ReportSpeechTranscript(ctx, m.Text)
	return []byte("preview mp3"), nil
}
func TestAnnouncementPreviewUsesRedirectAndStationScopedPrivateAudio(t *testing.T) {
	_, repo := announcementWeb(t)
	ctx := context.Background()
	djRepo, err := persistence.NewDJs(repo.DB, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := djRepo.List(ctx)
	if err != nil || len(profiles) == 0 {
		t.Fatal(err)
	}
	spy := &announcementPreviewProducer{}
	h, err := New(Dependencies{Announcements: repo, AnnouncementProducer: spy, Stations: persistence.Stations{DB: repo.DB}, DJs: djRepo, Schedule: persistence.Schedule{DB: repo.DB}})
	if err != nil {
		t.Fatal(err)
	}
	token := csrf(t, h)
	now := time.Now()
	m := announcements.Message{ID: "preview", StationID: "station", Title: "Preview", Text: "Only the producer hears this sample.", Category: "message", Mode: "exact", Status: "draft", Repeat: "once", DJID: profiles[0].ID, Starts: now, Expires: now.Add(time.Hour)}
	if err = repo.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	base := "/producer/stations/station/announcements/preview"
	w := request(h, "POST", base+"/action", url.Values{"csrf": {token}, "action": {"preview"}, "version": {"1"}}, nil)
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "?preview=") {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	location := w.Header().Get("Location")
	w = request(h, "GET", location, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<audio") || !strings.Contains(w.Body.String(), m.Text) {
		t.Fatal(w.Code, w.Body.String())
	}
	previewToken := strings.Split(location, "?preview=")[1]
	audio := "/producer/stations/station/announcement-previews/" + previewToken
	w = request(h, "GET", audio, nil, nil)
	if w.Code != 200 || w.Body.String() != "preview mp3" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	w = request(h, "GET", strings.Replace(audio, "/station/", "/other/", 1), nil, nil)
	if w.Code != 404 {
		t.Fatal("preview leaked to another station", w.Code)
	}
	m, err = repo.Get(ctx, "station", m.ID)
	if err != nil || m.Plays != 0 || m.Status != "draft" || spy.calls != 1 {
		t.Fatal("preview changed airtime or activation", m, err, spy.calls)
	}
	m.Text = "Changed"
	if err = repo.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	w = request(h, "GET", location, nil, nil)
	if strings.Contains(w.Body.String(), "<audio") {
		t.Fatal("stale version preview displayed")
	}
}
