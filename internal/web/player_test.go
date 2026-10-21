package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestPersistentPlayerLiveMetadataDoesNotStartAudio(t *testing.T) {
	_, st, src, cat := setupWeb(t, "")
	ctx := context.Background()
	st.Save(ctx, station.Station{ID: "radio", Name: "Pop", Timezone: "UTC"})
	c, err := (sources.Service{Repo: src}).Save(ctx, sources.Config{Name: "Music", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	tracks := []content.AudioItem{{ID: content.ID(c.ID, "a"), SourceID: c.ID, SourceItemID: "a", Title: "Current <song>", Artist: "Recording artist", Artwork: "http://server/current.png", Kind: content.Track, Origin: content.Original}, {ID: content.ID(c.ID, "b"), SourceID: c.ID, SourceItemID: "b", Title: "Next song", Artist: "Next artist", Artwork: "http://server/next.png", Kind: content.Track, Origin: content.Original}}
	selectTestSources(t, cat.DB)
	if err := cat.Replace(ctx, c, tracks); err != nil {
		t.Fatal(err)
	}
	pr := persistence.Playlists{DB: st.DB}
	pr.Save(ctx, playlists.Playlist{ID: "pool", StationID: "radio", Name: "Pop pool"})
	pr.SetIncluded(ctx, "pool", []string{tracks[0].ID, tracks[1].ID}, true)
	cfg := persistence.Broadcast{DB: st.DB}
	cfg.Assign(ctx, "radio", "pool")
	m := broadcast.New(cfg, tickingEncoder{}, slog.New(slog.NewTextHandler(io.Discard, nil)), broadcast.Options{})
	defer m.Close()
	deps := Dependencies{Stations: st, Sources: src, Catalogue: cat, Playlists: pr, Broadcast: m, BroadcastConfig: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	home := request(h, "GET", "/producer", nil, nil).Body.String()
	if !strings.Contains(home, `id="player-tracks"`) || !strings.Contains(home, `data-station-id="radio"`) {
		t.Fatal("missing persistent player metadata", home)
	}
	if request(h, "GET", "/producer/stations/missing/player", nil, nil).Code != 404 {
		t.Fatal("unknown station accepted")
	}
	deps.Password = "secret"
	protected, _ := New(deps)
	if request(protected, "GET", "/producer/stations/radio/player", nil, nil).Code != 401 {
		t.Fatal("metadata bypassed authentication")
	}
	server := httptest.NewServer(h)
	defer server.Close()
	eventCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, _ := http.NewRequestWithContext(eventCtx, "GET", server.URL+"/producer/stations/radio/player", nil)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	messages := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var html string
				json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &html)
				messages <- html
			}
		}
	}()
	read := func() string {
		select {
		case v := <-messages:
			return v
		case <-time.After(time.Second):
			t.Fatal("no player metadata")
			return ""
		}
	}
	if !strings.Contains(read(), "Nothing playing") || m.State("radio").Listeners != 0 {
		t.Fatal("metadata view started radio")
	}
	sub, err := m.Subscribe(ctx, "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	<-sub.Audio
	deadline := time.After(time.Second)
	for {
		select {
		case html := <-messages:
			if strings.Contains(html, "NOW PLAYING") && strings.Contains(html, "UP NEXT") && strings.Contains(html, "Recording artist") && strings.Contains(html, "Next artist") && strings.Contains(html, "Current &lt;song&gt;") {
				for _, track := range tracks {
					if !strings.Contains(html, stationTrack("radio", "", "", track).Artwork) {
						t.Fatal("player missing artwork", html)
					}
				}
				if strings.Contains(html, "http://server/") {
					t.Fatal("player exposes source artwork URL", html)
				}
				if m.State("radio").Listeners != 1 {
					t.Fatal("metadata counted as a listener")
				}
				return
			}
		case <-deadline:
			t.Fatal("player did not get both actual tracks")
		}
	}
}

func TestPlayerArtworkFollowsSpeakingPresenter(t *testing.T) {
	templates, err := template.New("pages").Funcs(templateFunctions()).ParseFS(assets, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	music := content.AudioItem{ID: "song", Title: "Song", Artist: "Artist", Artwork: "http://source/album.jpg"}
	next := content.AudioItem{ID: "next", Title: "Next song", Artwork: "http://source/next.jpg"}
	photo := "/producer/djs/outgoing/photo?v=123"
	state := broadcast.State{Programme: schedule.Occurrence{DJ: djs.Profile{ID: "outgoing", Name: "Speaking DJ", PortraitURL: photo}}, Following: schedule.Occurrence{DJ: djs.Profile{ID: "incoming", PortraitURL: "/static/djs/clara-vale.jpg"}}, Next: next}
	for _, phase := range []string{"chatter", "handover", "music", "missing-photo", "empty"} {
		t.Run(phase, func(t *testing.T) {
			current := state
			current.Phase = phase
			current.Current = content.AudioItem{ID: "speech", Title: "DJ link", Artist: "Speaking DJ", Kind: content.PresenterLink}
			if phase == "music" {
				current.Current = music
			}
			if phase == "missing-photo" {
				current.Phase = "chatter"
				current.Programme.DJ.PortraitURL = ""
			}
			if phase == "empty" {
				current.Phase = "chatter"
				current.Current = content.AudioItem{}
			}
			var html bytes.Buffer
			if err := templates.ExecuteTemplate(&html, "player-tracks", playerView{Station: station.Station{ID: "radio"}, Broadcast: current}); err != nil {
				t.Fatal(err)
			}
			// The homepage must use the same artwork selection as the player.
			var cards bytes.Buffer
			if err := templates.ExecuteTemplate(&cards, "station-cards", stationDirectoryView{StationCards: []stationCard{{Station: station.Station{ID: "radio", Name: "Radio"}, Broadcast: current, NextTrack: stationTrack("radio", "Up next", "Not queued", next)}}}); err != nil {
				t.Fatal(err)
			}
			cardHTML := strings.SplitN(cards.String(), `<td class="matrix-current" data-label="Now playing">`, 2)[1]
			var directory bytes.Buffer
			publicStation := station.Station{ID: "radio", StreamSlug: "radio", Name: "Radio"}
			directoryState := current
			directoryState.Programme.DJ.PortraitURL = publicPresenterPortrait("/radio/radio", current.Programme.DJ.ID, strings.TrimPrefix(current.Programme.DJ.PortraitURL, "/producer"))
			if err := templates.ExecuteTemplate(&directory, "public-cards", publicPageView{StationCards: []stationCard{{Station: publicStation, Broadcast: directoryState, CurrentTrack: publicTrack(publicStation, currentBroadcastTrack("radio", directoryState)), NextTrack: publicTrack(publicStation, stationTrack("radio", "UP NEXT", "Not queued", next))}}}); err != nil {
				t.Fatal(err)
			}
			directoryHTML := directory.String()
			if strings.Count(directoryHTML, `class="card-track"`) != 2 || !strings.Contains(directoryHTML, "UP NEXT") || !strings.Contains(directoryHTML, "Next song") || !strings.Contains(directoryHTML, "/radio/radio/artwork/next") {
				t.Fatal("listener directory missing current/upcoming track details", directoryHTML)
			}
			if strings.Contains(directoryHTML, "/producer/") || strings.Contains(directoryHTML, "http://source/") {
				t.Fatal("listener directory exposed producer or source artwork URLs", directoryHTML)
			}
			if (phase == "chatter" || phase == "handover") && (!strings.Contains(directoryHTML, "/radio/radio/presenters/outgoing/photo") || !strings.Contains(directoryHTML, "DJ ON AIR")) {
				t.Fatal("listener directory missing speaking DJ portrait", directoryHTML)
			}
			var overview bytes.Buffer
			if err := templates.ExecuteTemplate(&overview, "station-live", stationPageView{pageView: pageView{Station: station.Station{ID: "radio"}}, Broadcast: current}); err != nil {
				t.Fatal(err)
			}
			overviewHTML := strings.SplitN(overview.String(), `<div class="overview-playout">`, 2)[1]
			if strings.Contains(overviewHTML, `src="`+photo+`"`) != (phase == "chatter" || phase == "handover") {
				t.Fatal("overview did not follow the speaking presenter's artwork", overviewHTML)
			}
			if phase == "music" && (!strings.Contains(overviewHTML, stationTrack("radio", "", "", music).Artwork) || !strings.Contains(overviewHTML, "NOW PLAYING")) {
				t.Fatal("overview did not restore music artwork", overviewHTML)
			}
			if (phase == "chatter" || phase == "handover") && !strings.Contains(overviewHTML, "DJ ON AIR") {
				t.Fatal("overview did not label the spoken link", overviewHTML)
			}
			if !strings.Contains(overviewHTML, stationTrack("radio", "", "", next).Artwork) || strings.Contains(overviewHTML, "/static/djs/clara-vale.jpg") {
				t.Fatal("overview changed the upcoming artwork or showed the incoming DJ", overviewHTML)
			}
			if phase == "chatter" || phase == "handover" {
				if !strings.Contains(cardHTML, `src="`+photo+`"`) || !strings.Contains(cardHTML, "DJ ON AIR") {
					t.Fatal("station card missing speaking DJ artwork", cardHTML)
				}
			} else if strings.Contains(cardHTML, `src="`+photo+`"`) {
				t.Fatal("station card retained DJ artwork", cardHTML)
			}
			if phase == "music" && !strings.Contains(cardHTML, stationTrack("radio", "", "", music).Artwork) {
				t.Fatal("station card lost music artwork", cardHTML)
			}
			result := html.String()
			if strings.Contains(result, "/static/djs/clara-vale.jpg") {
				t.Fatal("used incoming DJ before they spoke", result)
			}
			if !strings.Contains(result, stationTrack("radio", "", "", next).Artwork) {
				t.Fatal("lost next album cover", result)
			}
			switch phase {
			case "music":
				if strings.Contains(result, photo) || !strings.Contains(result, stationTrack("radio", "", "", music).Artwork) || !strings.Contains(result, "NOW PLAYING") {
					t.Fatal("did not restore music artwork", result)
				}
			case "missing-photo", "empty":
				if strings.Contains(result, photo) || !strings.Contains(result, "art-placeholder") {
					t.Fatal("missing portrait fallback", result)
				}
			default:
				if !strings.Contains(result, `src="`+photo+`"`) || !strings.Contains(result, "DJ ON AIR") || !strings.Contains(result, "Speaking DJ") {
					t.Fatal("missing speaking DJ portrait", result)
				}
			}
		})
	}
}
