package web

import (
	"context"
	"net/http"
	"net/url"
	"stationos.local/stationos/internal/announcements"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
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
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Private preview") {
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
	location := strings.Split(w.Header().Get("Location"), "#")[0]
	w = request(h, "GET", location, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<audio") || !strings.Contains(w.Body.String(), m.Text) {
		t.Fatal(w.Code, w.Body.String())
	}
	parsed, _ := url.Parse(location)
	previewToken := parsed.Query().Get("preview")
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

func TestAnnouncementInlinePreviewKeepsEditorAndReturnsLocalErrors(t *testing.T) {
	_, repo := announcementWeb(t)
	ctx := context.Background()
	djRepo, _ := persistence.NewDJs(repo.DB, make([]byte, 32))
	spy := &announcementPreviewProducer{}
	h, err := New(Dependencies{Announcements: repo, AnnouncementProducer: spy, Stations: persistence.Stations{DB: repo.DB}, DJs: djRepo, Schedule: persistence.Schedule{DB: repo.DB}})
	if err != nil {
		t.Fatal(err)
	}
	m := announcements.Message{ID: "inline", StationID: "station", Title: "Inline", Text: "Saved script", Mode: "exact", Category: "message", Status: "draft", Repeat: "once", DJID: "default-pop", Starts: time.Now(), Expires: time.Now().Add(time.Hour)}
	if err = repo.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	token := csrf(t, h)
	path := "/producer/stations/station/announcements/inline"
	values := url.Values{"csrf": {token}, "version": {"1"}, "action": {"preview"}, "text": {"Unsaved edit"}}
	headers := map[string]string{"HX-Request": "true", "HX-Target": "announcement-preview"}
	w := request(h, "POST", path+"/action", values, headers)
	if w.Code != 200 || w.Header().Get("HX-Location") != "" || !strings.Contains(w.Body.String(), "Saved script") || !strings.Contains(w.Body.String(), "<audio") || strings.Contains(w.Body.String(), "data-announcement-form") || strings.Contains(w.Body.String(), "Unsaved edit") {
		t.Fatal("preview did not stay local", w.Code, w.Body.String())
	}
	w = request(h, "GET", path, nil, nil)
	if !strings.Contains(w.Body.String(), "<audio") || spy.calls != 1 {
		t.Fatal("cached preview lost or regenerated")
	}
	m.Version = 1
	m.Text = "New saved script"
	if err = repo.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	w = request(h, "POST", path+"/action", values, headers)
	if !strings.Contains(w.Body.String(), announcements.ErrConflict.Error()) || strings.Contains(w.Body.String(), "<!doctype") {
		t.Fatal("conflict escaped preview region", w.Code, w.Body.String())
	}
}

func TestAnnouncementShowAndDateTargeting(t *testing.T) {
	h, repo := announcementWeb(t)
	ctx := context.Background()
	if err := (persistence.Playlists{DB: repo.DB}).Save(ctx, playlists.Playlist{ID: "pool", StationID: "station", Name: "Pool"}); err != nil {
		t.Fatal(err)
	}
	shows := persistence.Schedule{DB: repo.DB}
	show := schedule.Show{ID: "monday", StationID: "station", Name: "Monday show", Day: 0, Start: 600, End: 660, DJID: "default-pop", PlaylistID: "pool"}
	if err := shows.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	token := csrf(t, h)
	base := "/producer/stations/station/announcements/new"
	values := url.Values{"csrf": {token}, "version": {"0"}, "title": {"Episode notice"}, "text": {"Facts"}, "category": {"message"}, "mode": {"exact"}, "status": {"draft"}, "repeat": {"once"}, "starts": {"2026-10-25T00:00"}, "expires": {"2026-10-28T00:00"}, "scope": {"episode"}, "episode_show": {"monday"}, "episode_date": {"2026-10-26"}}
	w := request(h, "POST", base, values, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	items, _ := repo.List(ctx, "station")
	if len(items) != 1 || items[0].EpisodeKey == "" || len(items[0].ShowIDs) != 1 {
		t.Fatal(items)
	}
	w = request(h, "GET", base+"/episode?episode_show=monday&episode_date=2026-10-26", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Monday show") || strings.Contains(w.Body.String(), "<!doctype") {
		t.Fatal(w.Code, w.Body.String())
	}
	values.Set("episode_date", "2026-10-27")
	w = request(h, "POST", base, values, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "no episode on that date") || !strings.Contains(w.Body.String(), `value="2026-10-27"`) {
		t.Fatal("invalid date not retained", w.Code, w.Body.String())
	}
	w = request(h, "GET", "/producer/stations/station/announcements/"+items[0].ID, nil, nil)
	if !strings.Contains(w.Body.String(), `name="episode_date" value="2026-10-26"`) || strings.Contains(w.Body.String(), `name="episode"`) {
		t.Fatal("episode target not restored or old menu retained")
	}
}

func TestAnnouncementPreviewAudioSupportsBrowserRangeRequests(t *testing.T) {
	_, repo := announcementWeb(t)
	ctx := context.Background()
	djRepo, _ := persistence.NewDJs(repo.DB, make([]byte, 32))
	h, err := New(Dependencies{Announcements: repo, AnnouncementProducer: &announcementPreviewProducer{}, Stations: persistence.Stations{DB: repo.DB}, DJs: djRepo, Schedule: persistence.Schedule{DB: repo.DB}})
	if err != nil {
		t.Fatal(err)
	}
	m := announcements.Message{ID: "range", StationID: "station", Title: "Range", Category: "message", Text: "Sample", Mode: "exact", Status: "draft", Repeat: "once", DJID: "default-pop", Starts: time.Now(), Expires: time.Now().Add(time.Hour)}
	if err = repo.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	w := request(h, "POST", "/producer/stations/station/announcements/range/action", url.Values{"csrf": {csrf(t, h)}, "action": {"preview"}, "version": {"1"}}, nil)
	u, _ := url.Parse(w.Header().Get("Location"))
	token := u.Query().Get("preview")
	w = request(h, "GET", "/producer/stations/station/announcement-previews/"+token, nil, map[string]string{"Range": "bytes=0-6"})
	if w.Code != 206 || w.Body.String() != "preview" || w.Header().Get("Content-Range") == "" {
		t.Fatal("browser byte range not served", w.Code, w.Body.String(), w.Header())
	}
}
