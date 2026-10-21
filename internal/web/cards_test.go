package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
	"time"
)

type artworkSource struct{ fakeSource }

func (artworkSource) Profile() sources.Profile {
	return sources.Profile{Capabilities: sources.Capabilities{Artwork: true}}
}
func (artworkSource) Artwork(_ context.Context, raw string) ([]byte, string, error) {
	data, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aC1sAAAAASUVORK5CYII=")
	return data, "image/png", nil
}
func TestProducerCardsDormantScheduledAndDefaultMusic(t *testing.T) {
	_, st, src, cat := setupWeb(t, "")
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pr := persistence.Playlists{DB: st.DB}
	config := persistence.Broadcast{DB: st.DB}
	shows := persistence.Schedule{DB: st.DB}
	if err := st.Save(ctx, station.Station{ID: "radio", Name: "Jazz Station", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	c, err := (sources.Service{Repo: src}).Save(ctx, sources.Config{Name: "Music", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	length := 3 * time.Minute
	items := []content.AudioItem{}
	for _, name := range []string{"First <song>", "Second song"} {
		items = append(items, content.AudioItem{ID: content.ID(c.ID, name), SourceID: c.ID, SourceItemID: name, Title: name, Artist: "The Quartet", Album: "Night music", Duration: &length, Artwork: "http://server/art.png", Kind: content.Track, Origin: content.Original})
	}
	selectTestSources(t, cat.DB)
	if err = cat.Replace(ctx, c, items); err != nil {
		t.Fatal(err)
	}
	for _, p := range []playlists.Playlist{{ID: "default", StationID: "radio", Name: "Jazz Default"}, {ID: "show", StationID: "radio", Name: "Show Pool"}} {
		if err = pr.Save(ctx, p); err != nil {
			t.Fatal(err)
		}
		if err = pr.SetIncluded(ctx, p.ID, []string{items[0].ID, items[1].ID}, true); err != nil {
			t.Fatal(err)
		}
	}
	config.Assign(ctx, "radio", "default")
	now := time.Now().UTC()
	start := (now.Hour()*60 + now.Minute()) / 15 * 15
	if err = shows.Save(ctx, schedule.Show{ID: "slot", StationID: "radio", Name: "Evening sessions", DJID: "default-jazz", PlaylistID: "show", Day: (int(now.Weekday()) + 6) % 7, Start: start, End: (start + 60) % 1440}); err != nil {
		t.Fatal(err)
	}
	m := broadcast.New(config, tickingEncoder{}, logger, broadcast.Options{Schedule: shows})
	defer m.Close()
	deps := Dependencies{Stations: st, Sources: src, Catalogue: cat, Playlists: pr, Broadcast: m, BroadcastConfig: config, ArtworkFactory: func(context.Context, sources.Config) (sources.AudioSource, error) { return artworkSource{}, nil }, Ready: st.DB.PingContext, Logger: logger}
	h, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	w := request(h, "GET", "/producer", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Jazz Default") || !strings.Contains(w.Body.String(), "Nothing playing") || strings.Contains(w.Body.String(), "Evening sessions") {
		t.Fatal("dormant card fabricates active programme", w.Body.String())
	}
	sub, err := m.Subscribe(ctx, "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	select {
	case <-sub.Audio:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not start")
	}
	for _, path := range []string{"/producer", "/producer/stations/live"} {
		w = request(h, "GET", path, nil, nil)
		html := w.Body.String()
		for _, want := range []string{"Evening sessions", "Miles Mercer", "Show Pool", "First &lt;song&gt;", "Second song", "The Quartet", "Night music", "3:00", "1 listener", "/artwork/", "hx-indicator=\"this\"", `class="matrix-table"`} {
			if !strings.Contains(html, want) {
				t.Fatal("card missing", want, html)
			}
		}
		if strings.Contains(html, "http://server/art.png") {
			t.Fatal("artwork bypasses checked source")
		}
	}
	dashboard := request(h, "GET", "/producer", nil, nil).Body.String()
	for _, want := range []string{`aria-label="Across all stations"`, "Listeners now", "Stations broadcasting", "Stations needing attention"} {
		if !strings.Contains(dashboard, want) {
			t.Fatal("missing dashboard summary", want)
		}
	}
	state := m.State("radio")
	for _, path := range []string{"/producer/stations/radio", "/producer/stations/radio/live"} {
		html := request(h, "GET", path, nil, nil).Body.String()
		if !strings.Contains(html, `id="upcoming"`) || !strings.Contains(html, `aria-label="Upcoming tracks"`) {
			t.Fatal("overview missing actual upcoming selection", path, html)
		}
		if strings.Contains(html, "Selected · checked again before playback") {
			t.Fatal("overview shows obsolete playback recheck message", path)
		}
		if strings.Contains(html, ">Upcoming</a>") {
			t.Fatal("upcoming still has a separate tab", path)
		}
		if strings.Count(html, `class="overview-track"`) != 2 {
			t.Fatal("overview missing playback panels", path, html)
		}
		for _, item := range []content.AudioItem{state.Current, state.Next} {
			if !strings.Contains(html, stationTrack("radio", "", "", item).Artwork) {
				t.Fatal("overview missing track artwork", path, html)
			}
		}
		if strings.Contains(html, "http://server/art.png") {
			t.Fatal("overview exposes source artwork URL", path)
		}
	}
	path := "/producer/stations/radio/artwork/" + state.Current.ID
	w = request(h, "GET", path, nil, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Cache-Control") != "private, max-age=300" {
		t.Fatal(w.Code, w.Header())
	}
	if request(h, "GET", "/producer/stations/radio/artwork/arbitrary?url=http://other.example", nil, nil).Code != 404 {
		t.Fatal("arbitrary artwork admitted")
	}
	deps.Password = "secret"
	protected, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	if request(protected, "GET", path, nil, nil).Code != http.StatusUnauthorized {
		t.Fatal("artwork bypassed management authentication")
	}
	sub.Close()
	m.Stop("radio")
	if err = shows.Delete(ctx, "radio", "slot"); err != nil {
		t.Fatal(err)
	}
	fallback, err := m.Subscribe(ctx, "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer fallback.Close()
	select {
	case <-fallback.Audio:
	case <-time.After(2 * time.Second):
		t.Fatal("default music did not start")
	}
	w = request(h, "GET", "/producer/stations/live", nil, nil)
	html := w.Body.String()
	if !strings.Contains(html, "Jazz Default") || !strings.Contains(html, "Second song") || strings.Contains(html, "Evening sessions") || strings.Contains(html, "ON AIR") {
		t.Fatal("default card mislabels show", html)
	}
}
func TestProgrammeDetailsUsesStationTimezoneAndFinishPolicy(t *testing.T) {
	end := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	s := broadcast.State{Programme: schedule.Occurrence{ShowID: "show", Start: end.Add(-time.Hour), End: end}, Current: content.AudioItem{ID: "track", Kind: content.Track}, PresenterStatus: "Ready"}
	times, status := programmeDetails(s, "Europe/London", end.Add(time.Second))
	if times != "10:00–11:00" || status != "Finishing current track" {
		t.Fatal(times, status)
	}
	s.Phase = "handover"
	_, status = programmeDetails(s, "UTC", end)
	if status != "Handing over" {
		t.Fatal(status)
	}
}

func TestSpeechReadinessDoesNotHideCurationFailure(t *testing.T) {
	state := broadcast.State{PresenterStatus: "Ready", CurationStatus: "Unavailable"}
	label := presenterStatus(state)
	if label != "Speech ready · Curation unavailable · music continues" || strings.Contains(label, "DJ ready") {
		t.Fatal("speech readiness hid failed curation", label)
	}
	state.CurationStatus = "Preparing"
	if label = presenterStatus(state); label != "Speech ready · Preparing curation" {
		t.Fatal(label)
	}
	state.CurationStatus = "Ready"
	if label = presenterStatus(state); label != "Speech ready · Curation ready" {
		t.Fatal(label)
	}
}

func TestDJStatusIconsKeepCapabilitiesIndependent(t *testing.T) {
	templates, err := template.New("pages").Funcs(templateFunctions()).ParseFS(assets, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	state := broadcast.State{Programme: schedule.Occurrence{ShowID: "show", DJ: djs.Profile{ID: "miles", Name: "Miles Mercer"}}, Current: content.AudioItem{ID: "track"}, PresenterStatus: "Unavailable", CurationStatus: "Ready"}
	for _, test := range []struct{ speech, selection, speechLabel, selectionLabel string }{
		{"Unavailable", "Ready", "DJ speech is unavailable; music continues", "DJ music selection is ready"},
		{"Ready", "Unavailable", "DJ speech is ready", "DJ music selection is unavailable; music continues"},
		{"Preparing", "Preparing", "DJ speech is being prepared", "DJ music selection is being prepared"},
		{"Speaking", "Ready", "DJ is speaking", "DJ music selection is ready"},
	} {
		state.PresenterStatus, state.CurationStatus = test.speech, test.selection
		state.AINotice = "Internal provider notice should not be displayed"
		data := map[string]any{
			"station-cards": stationDirectoryView{StationCards: []stationCard{{Broadcast: state, DJIndicators: djIndicators(state)}}},
			"station-live":  stationPageView{Broadcast: state},
		}
		for _, name := range []string{"station-cards", "station-live"} {
			var html bytes.Buffer
			if err := templates.ExecuteTemplate(&html, name, data[name]); err != nil {
				t.Fatal(err)
			}
			markup := html.String()
			for _, label := range []string{test.speechLabel, test.selectionLabel} {
				if !strings.Contains(markup, `aria-label="`+label+`"`) {
					t.Fatal("missing accessible capability explanation", name, label)
				}
			}
			if strings.Count(markup, `class="dj-status-icon `) != 2 || strings.Contains(markup, `class="card-dj-status"`) || strings.Contains(markup, `overview-dj-state`) || strings.Contains(markup, state.AINotice) {
				t.Fatal("playback view repeated status text or provider notices", name, markup)
			}
			if strings.Count(markup, `class="dj-status-tooltip" hidden`) != 2 {
				t.Fatal("status explanations must be hidden by default, including before styles load", name)
			}
			nameRow := `<div class="dj-name-row"><a href="/producer/djs/miles">Miles Mercer</a><div class="dj-statuses"`
			if !strings.Contains(regexp.MustCompile(`>\s+<`).ReplaceAllString(markup, "><"), nameRow) {
				t.Fatal("DJ icons must sit immediately after the DJ name", name, markup)
			}
		}
	}
	state.Current.ID = ""
	if len(djIndicators(state)) != 0 {
		t.Fatal("dormant station displayed DJ readiness")
	}
	state.Current.ID = "track"
	state.Programme.ShowID = ""
	if len(djIndicators(state)) != 0 {
		t.Fatal("continuous music displayed DJ readiness")
	}
	state.Programme.ShowID = "show"
	state.PresenterStatus, state.CurationStatus = "", ""
	if len(djIndicators(state)) != 0 {
		t.Fatal("unknown capability was presented as ready")
	}
}
