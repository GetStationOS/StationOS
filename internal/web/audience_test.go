package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

func TestPublicAudienceConsentCSRFPolicyAndQueue(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := persistence.Stations{DB: db}
	shows := persistence.Schedule{DB: db}
	repo := persistence.Audience{DB: db}
	pl := persistence.Playlists{DB: db}
	if err = st.Save(ctx, station.Station{ID: "s", StreamSlug: "radio", Name: "Radio", Timezone: "UTC", Visibility: station.Listed}); err != nil {
		t.Fatal(err)
	}
	if err = pl.Save(ctx, playlists.Playlist{ID: "p", StationID: "s", Name: "Pool"}); err != nil {
		t.Fatal(err)
	}
	day := (int(time.Now().UTC().Weekday()) + 6) % 7
	show := schedule.Show{ID: "show", StationID: "s", Name: "All day", Description: "Listener favourites <every day>", DJID: "default-pop", PlaylistID: "p", Day: day, Start: 0, End: 1425, AcceptShoutouts: true, AcceptRequests: true}
	if err = shows.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	h, err := New(Dependencies{Audience: repo, Sources: persistence.Sources{DB: db}, Catalogue: persistence.Catalogue{DB: db}, Stations: st, Schedule: shows, Playlists: pl, BroadcastConfig: persistence.Broadcast{DB: db}, Password: "secret", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	occurrences, err := (audience.Service{Stations: st, Shows: shows, Repo: repo}).Opportunities(ctx, "s", audience.Shoutout)
	if err != nil || len(occurrences) == 0 {
		t.Fatal(occurrences, err)
	}
	key := occurrences[0].Key
	path := "/radio/radio/shoutouts?show=" + url.QueryEscape(key)
	w := request(h, "GET", path, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Your shout-out") || strings.Contains(w.Body.String(), "station-tabs") {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal(cookies)
	}
	marker := `name="csrf" value="`
	text := strings.SplitN(w.Body.String(), marker, 2)
	if len(text) != 2 {
		t.Fatal("no token")
	}
	token := strings.SplitN(text[1], `"`, 2)[0]
	form := url.Values{"csrf": {token}, "show": {occurrences[0].Key}, "name": {"<script>Sam</script>"}, "message": {"Hi family!"}, "consent": {"yes"}}
	headers := map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value}

	// The on-page workflow renders only the selected step, while direct URLs and
	// history restoration retain the complete listening page.
	hx := map[string]string{"HX-Request": "true", "HX-Target": "contribution-panel", "Cookie": headers["Cookie"]}
	choices := request(h, "GET", "/radio/radio/contribute?show="+url.QueryEscape(key), nil, hx)
	if choices.Code != 200 || !strings.Contains(choices.Body.String(), "Request a track") || !strings.Contains(choices.Body.String(), "Send a shout-out") || strings.Contains(choices.Body.String(), `id="main"`) {
		t.Fatal(choices.Code, choices.Body.String())
	}
	restored := request(h, "GET", path, nil, map[string]string{"HX-Request": "true", "HX-Target": "contribution-panel", "HX-History-Restore-Request": "true"})
	if !strings.Contains(restored.Body.String(), "<!doctype html>") || !strings.Contains(restored.Body.String(), "Programme") {
		t.Fatal(restored.Body.String())
	}
	empty := request(h, "GET", "/radio/radio/shoutouts", nil, hx)
	if !strings.Contains(empty.Body.String(), "Choose a show") || strings.Contains(empty.Body.String(), `name="message"`) {
		t.Fatal(empty.Body.String())
	}
	stale := request(h, "GET", "/radio/radio/contribute?show=old-occurrence", nil, hx)
	if !strings.Contains(stale.Body.String(), "Contributions are closed") || strings.Contains(stale.Body.String(), "Request a track") {
		t.Fatal(stale.Body.String())
	}
	page := request(h, "GET", "/radio/radio", nil, nil)
	if !strings.Contains(page.Body.String(), `data-open-show`) || strings.Contains(page.Body.String(), `href="/radio/radio/requests"`) {
		t.Fatal(page.Body.String())
	}
	invalid := url.Values{"csrf": {token}, "show": {key}, "name": {"Pat"}, "message": {"Hello everyone"}}
	failed := request(h, "POST", path, invalid, hx)
	if failed.Code != 200 || !strings.Contains(failed.Body.String(), `value="Pat"`) || !strings.Contains(failed.Body.String(), "Hello everyone") || !strings.Contains(failed.Body.String(), `role="alert"`) {
		t.Fatal(failed.Code, failed.Body.String())
	}

	if got := request(h, "POST", path, form, nil); got.Code != 403 {
		t.Fatal("no cookie", got.Code)
	}
	badHeaders := map[string]string{"Cookie": headers["Cookie"], "Origin": "https://evil.example"}
	if got := request(h, "POST", path, form, badHeaders); got.Code != 403 {
		t.Fatal("cross origin", got.Code)
	}
	form.Del("consent")
	if got := request(h, "POST", path, form, headers); got.Code != 422 {
		t.Fatal("consent", got.Code)
	}
	form.Set("consent", "yes")
	w = request(h, "POST", path, form, headers)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	messages, total, err := repo.List(ctx, "s", "pending", 0, 50, time.Now())
	if err != nil || total != 1 {
		t.Fatal(messages, total, err)
	}
	if got := request(h, "POST", path, form, headers); got.Code != 422 {
		t.Fatal("duplicate", got.Code)
	}
	if got := request(h, "GET", "/producer/stations/s/audience", nil, nil); got.Code != 401 {
		t.Fatal("queue public", got.Code)
	}
	reqHeaders := map[string]string{"Authorization": "Basic YWRtaW46c2VjcmV0"}
	w = request(h, "GET", "/producer/stations/s/audience", nil, reqHeaders)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "&lt;script&gt;Sam&lt;/script&gt;") {
		t.Fatal(w.Code, w.Body.String())
	}
	// Audience form tokens must never authorize producer writes.
	if got := request(h, "POST", "/producer/stations/s/audience/"+messages[0].ID, url.Values{"csrf": {token}, "action": {"approve"}}, reqHeaders); got.Code != 403 {
		t.Fatal("public token accepted by management", got.Code)
	}

	// Successful enhanced forms navigate the contribution panel to a separate
	// thank-you step; refresh of that GET cannot submit again.
	another := url.Values{"csrf": {token}, "show": {key}, "name": {"Jo"}, "message": {"Happy birthday"}, "consent": {"yes"}}
	sent := request(h, "POST", path, another, hx)
	if sent.Code != 200 || !strings.Contains(sent.Header().Get("HX-Location"), `"target":"#contribution-panel"`) || !strings.Contains(sent.Header().Get("HX-Location"), `"push":false`) {
		t.Fatal(sent.Code, sent.Header())
	}
	thanks := request(h, "GET", path+"&sent=yes", nil, hx)
	if !strings.Contains(thanks.Body.String(), "Thanks for contributing!") || strings.Contains(thanks.Body.String(), `name="message"`) {
		t.Fatal(thanks.Body.String())
	}

	// Show pages expose public description and presenter information only.
	detail := request(h, "GET", "/radio/radio/shows/show", nil, nil)
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), "Listener favourites &lt;every day&gt;") || !strings.Contains(detail.Body.String(), "airing:") || !strings.Contains(detail.Body.String(), "/shows/show/contribute?show=") || strings.Contains(detail.Body.String(), "api_key") {
		t.Fatal(detail.Code, detail.Body.String())
	}
	expanded := request(h, "GET", "/radio/radio/shows/show?show="+url.QueryEscape(key), nil, map[string]string{"HX-Request": "true", "HX-Target": "show-details-show"})
	if expanded.Code != 200 || !strings.Contains(expanded.Body.String(), "Listener favourites") || !strings.Contains(expanded.Body.String(), `id="contribution-show"`) || strings.Contains(expanded.Body.String(), `id="public-live"`) || strings.Contains(expanded.Body.String(), "<title>") || !strings.Contains(expanded.Body.String(), `hx-push-url="false"`) {
		t.Fatal("show expansion replaced the station", expanded.Code, expanded.Body.String())
	}
	if strings.Contains(expanded.Body.String(), "Close show details") || strings.Contains(expanded.Body.String(), "data-close-show") || strings.Contains(page.Body.String(), "<details") || strings.Contains(page.Body.String(), "show-chevron") {
		t.Fatal("show rows should toggle without an extra close control", expanded.Body.String())
	}
	detailFragment := request(h, "GET", "/radio/radio/shows/show", nil, map[string]string{"HX-Request": "true", "HX-Target": "main"})
	if strings.Contains(detailFragment.Body.String(), "<!doctype html>") || !strings.Contains(detailFragment.Body.String(), "Listener favourites") {
		t.Fatal("show details did not use HTMX shell", detailFragment.Body.String())
	}
	detailHX := map[string]string{"HX-Request": "true", "HX-Target": "contribution-show", "Cookie": headers["Cookie"]}
	detailTypes := request(h, "GET", "/radio/radio/shows/show/contribute?show="+url.QueryEscape(key), nil, detailHX)
	if !strings.Contains(detailTypes.Body.String(), "/shows/show/requests?show=") {
		t.Fatal(detailTypes.Body.String())
	}
	if !strings.Contains(detailTypes.Body.String(), `aria-label="Cancel contribution"`) || !strings.Contains(detailTypes.Body.String(), `hx-target="#contribution-show"`) {
		t.Fatal("missing scoped cancel control", detailTypes.Body.String())
	}
	cancelled := request(h, "GET", "/radio/radio/shows/show?show="+url.QueryEscape(key), nil, detailHX)
	if cancelled.Code != 200 || !strings.Contains(cancelled.Body.String(), "Be part of the show") || !strings.Contains(cancelled.Body.String(), ">Be part of the show</a>") || strings.Contains(cancelled.Body.String(), "public-show-detail") || strings.Contains(cancelled.Body.String(), "<title>") || strings.Contains(cancelled.Body.String(), "contribution-cancel") {
		t.Fatal("cancel did not reset only the contribution area", cancelled.Code, cancelled.Body.String())
	}

	if got := request(h, "GET", "/radio/radio/shows/missing", nil, nil); got.Code != 404 {
		t.Fatal("missing show", got.Code)
	}
	if got := request(h, "POST", "/radio/radio/shows/missing/shoutouts", another, headers); got.Code != 404 {
		t.Fatal("cross-show submission", got.Code)
	}
	detailForm := request(h, "GET", "/radio/radio/shows/show/shoutouts?show="+url.QueryEscape(key), nil, detailHX)
	if !strings.Contains(detailForm.Body.String(), `action="/radio/radio/shows/show/shoutouts"`) {
		t.Fatal(detailForm.Body.String())
	}

	// Search projects only this show's eligible recordings and stays a small list.
	source := sources.Config{ID: "music", Name: "Music", Kind: "upnp", Endpoint: "http://music.test/description.xml", Revision: 1}
	if err = (persistence.Sources{DB: db}).Save(ctx, source); err != nil {
		t.Fatal(err)
	}
	var music []content.AudioItem
	var ids []string
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("dream-%02d", i)
		track := content.AudioItem{ID: content.ID(source.ID, id), SourceID: source.ID, SourceItemID: id, Kind: content.Track, Origin: content.Original, Title: fmt.Sprintf("Dreams %02d", i), Artist: "Fleetwood Mac", Album: "Rumours", Resources: []content.Resource{{URL: "http://music.test/audio.mp3", MIME: "audio/mpeg"}}}
		music = append(music, track)
		ids = append(ids, track.ID)
	}
	foreign := music[0]
	foreign.SourceItemID = "foreign"
	foreign.ID = content.ID(source.ID, "foreign")
	foreign.Title = "Foreign recording"
	foreign.Artist = "Foreign band"
	music = append(music, foreign)
	if err = (persistence.Catalogue{DB: db}).Replace(ctx, source, music); err != nil {
		t.Fatal(err)
	}
	if err = pl.SetIncluded(ctx, "p", ids, true); err != nil {
		t.Fatal(err)
	}
	searchBase := "/radio/radio/shows/show/requests?show=" + url.QueryEscape(key)
	searchHX := map[string]string{"HX-Request": "true", "HX-Target": "audience-track-options-contribution-show"}
	for _, tc := range []struct {
		q       string
		count   int
		message string
	}{
		{"", 0, "Start typing"}, {"Mac Dreams", 8, "Showing the first 8 matches"}, {"00 fleetwood", 1, "1 recording found"}, {"unknown", 0, "No tracks match"}, {"foreign", 0, "No tracks match"},
	} {
		got := request(h, "GET", searchBase+"&q="+url.QueryEscape(tc.q), nil, searchHX)
		if got.Code != 200 || strings.Count(got.Body.String(), "data-track-result ") != tc.count || !strings.Contains(got.Body.String(), tc.message) || strings.Contains(got.Body.String(), `name="message"`) || strings.Contains(got.Body.String(), "<title>") {
			t.Fatal(tc.q, got.Code, got.Body.String())
		}
		if tc.count > 0 && (!strings.Contains(got.Body.String(), "Rumours") || !strings.Contains(got.Body.String(), "Fleetwood Mac")) {
			t.Fatal("recordings lack identifying metadata", got.Body.String())
		}
	}
	requestUI := request(h, "GET", searchBase, nil, detailHX)
	if !strings.Contains(requestUI.Body.String(), "input changed delay:300ms") || !strings.Contains(requestUI.Body.String(), "data-selected-recording") || strings.Contains(requestUI.Body.String(), `<select name="track"`) {
		t.Fatal("search UI missing", requestUI.Body.String())
	}
	// Validation keeps the selected recording, requester and dedication together.
	invalidRequest := url.Values{"csrf": {token}, "show": {key}, "track": {ids[9]}, "name": {"Sam"}, "message": {"For my family"}}
	retained := request(h, "POST", "/radio/radio/shows/show/requests", invalidRequest, detailHX)
	for _, want := range []string{`value="Sam"`, "For my family", `value="` + ids[9] + `"`, "Dreams 09", "checked"} {
		if !strings.Contains(retained.Body.String(), want) {
			t.Fatal("validation lost selection or input", want, retained.Code, retained.Body.String())
		}
	}
	tracksOnly := request(h, "GET", "/radio/radio/shows/show/requests?show="+url.QueryEscape(key)+"&q=test", nil, map[string]string{"HX-Request": "true", "HX-Target": "audience-track-options-contribution-show"})
	if tracksOnly.Code != 200 || !strings.Contains(tracksOnly.Body.String(), `id="audience-track-options-contribution-show"`) || strings.Contains(tracksOnly.Body.String(), "<title>") || strings.Contains(tracksOnly.Body.String(), `name="message"`) {
		t.Fatal("track search replaced more than its selector", tracksOnly.Code, tracksOnly.Body.String())
	}
	detailHeaders := map[string]string{"HX-Request": "true", "HX-Target": "contribution-show", "Cookie": headers["Cookie"]}
	third := url.Values{"csrf": {token}, "show": {key}, "name": {"Ash"}, "message": {"Good afternoon!"}, "consent": {"yes"}}
	if got := request(h, "POST", "/radio/radio/shows/show/shoutouts", third, detailHeaders); got.Code != 200 || !strings.Contains(got.Header().Get("HX-Location"), "/shows/show/shoutouts") {
		t.Fatal(got.Code, got.Body.String(), got.Header())
	}

	queueAuth := map[string]string{"Authorization": "Basic YWRtaW46c2VjcmV0"}
	queue := request(h, "GET", "/producer/stations/s/audience", nil, queueAuth)
	if queue.Code != 200 || !strings.Contains(queue.Body.String(), `id="audience-queue-results"`) || !strings.Contains(queue.Body.String(), `hx-trigger="every 5s"`) || !strings.Contains(queue.Body.String(), `title="Approve"`) || !strings.Contains(queue.Body.String(), `title="Dismiss"`) {
		t.Fatal("queue feedback or icon actions missing", queue.Code, queue.Body.String())
	}
	queueAuth["HX-Request"] = "true"
	queueAuth["HX-Target"] = "audience-queue-results"
	polled := request(h, "GET", "/producer/stations/s/audience?status=pending&offset=0", nil, queueAuth)
	if polled.Code != 200 || strings.Contains(polled.Body.String(), "<title>") || strings.Contains(polled.Body.String(), `<select name="status"`) || !strings.Contains(polled.Body.String(), "audience-queue-table") {
		t.Fatal("queue polling replaced the filter or page", polled.Code, polled.Body.String())
	}
	for _, fragment := range []string{`type="checkbox" name="status"`, `hx-trigger="change, submit"`, `hx-swap="outerHTML show:none"`, `audience-status-badge`} {
		if !strings.Contains(queue.Body.String(), fragment) {
			t.Fatal("missing automatic table filter or styling", fragment)
		}
	}
	multiple := request(h, "GET", "/producer/stations/s/audience?filtered=1&status=approved&status=pending", nil, queueAuth)
	if multiple.Code != 200 || !strings.Contains(multiple.Body.String(), "status=pending&amp;status=approved") || strings.Contains(multiple.Body.String(), "audience-status-options") {
		t.Fatal("filter refresh lost its selections or replaced the controls", multiple.Body.String())
	}
	emptyQueue := request(h, "GET", "/producer/stations/s/audience?filtered=1", nil, queueAuth)
	if emptyQueue.Code != 200 || !strings.Contains(emptyQueue.Body.String(), "Select a status above") || !strings.Contains(emptyQueue.Body.String(), "0 submissions") {
		t.Fatal("unchecking every status did not empty the table", emptyQueue.Body.String())
	}
	css := request(h, "GET", "/static/app.css", nil, nil)
	if css.Code != 200 || !strings.HasPrefix(css.Body.String(), ":root") || strings.Contains(css.Body.String(), "package broadcast") {
		t.Fatal("application stylesheet was overwritten")
	}
	// Requests cannot nominate tracks outside the show's current eligibility pool.
	requestForm := url.Values{"csrf": {token}, "show": {occurrences[0].Key}, "track": {"foreign-track"}, "consent": {"yes"}}
	if got := request(h, "POST", "/radio/radio/requests", requestForm, headers); got.Code != 422 {
		t.Fatal("foreign requested track", got.Code, got.Body.String())
	}
	if got := request(h, "POST", path, url.Values{"csrf": {token}, "show": {occurrences[0].Key}, "message": {strings.Repeat("a", 401)}, "consent": {"yes"}}, headers); got.Code != 422 {
		t.Fatal("oversized message", got.Code)
	}
	show.AcceptShoutouts = false
	if err = shows.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	if got := request(h, "POST", path, form, headers); got.Code != 422 {
		t.Fatal("disabled show", got.Code)
	}
	updated, _ := (audience.Service{Stations: st, Shows: shows, Repo: repo}).Opportunities(ctx, "s", audience.Request)
	onlyRequest := request(h, "GET", "/radio/radio/contribute?show="+url.QueryEscape(updated[0].Key), nil, hx)
	if !strings.Contains(onlyRequest.Body.String(), "Request a track") || strings.Contains(onlyRequest.Body.String(), "Send a shout-out") {
		t.Fatal(onlyRequest.Body.String())
	}
	show.AcceptRequests = false
	if err = shows.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	noInteraction := request(h, "GET", "/radio/radio/shows/show", nil, map[string]string{"HX-Request": "true", "HX-Target": "show-details-show"})
	if noInteraction.Code != 200 || strings.Contains(noInteraction.Body.String(), "show-contribution") || strings.Contains(noInteraction.Body.String(), "Be part of the show") || !strings.Contains(noInteraction.Body.String(), "Listener favourites") {
		t.Fatal("disabled interaction panel was shown", noInteraction.Code, noInteraction.Body.String())
	}
	saved, _ := st.Get(ctx, "s")
	saved.Visibility = station.Private
	if err = st.Save(ctx, saved); err != nil {
		t.Fatal(err)
	}
	if got := request(h, "GET", "/radio/radio/shows/show", nil, nil); got.Code != 404 {
		t.Fatal("private show", got.Code)
	}
	if got := request(h, "GET", path, nil, nil); got.Code != 404 {
		t.Fatal("private form", got.Code)
	}
	if got := request(h, "POST", path, form, headers); got.Code != 404 {
		t.Fatal("private submission", got.Code)
	}
}
