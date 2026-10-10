package web

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
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
	tracks := []content.AudioItem{{ID: content.ID(c.ID, "a"), SourceID: c.ID, SourceItemID: "a", Title: "Current <song>", Artist: "Recording artist", Kind: content.Track, Origin: content.Original}, {ID: content.ID(c.ID, "b"), SourceID: c.ID, SourceItemID: "b", Title: "Next song", Artist: "Next artist", Kind: content.Track, Origin: content.Original}}
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
	home := request(h, "GET", "/", nil, nil).Body.String()
	if !strings.Contains(home, `id="player-tracks"`) || !strings.Contains(home, `data-station-id="radio"`) {
		t.Fatal("missing persistent player metadata", home)
	}
	if request(h, "GET", "/stations/missing/player", nil, nil).Code != 404 {
		t.Fatal("unknown station accepted")
	}
	deps.Password = "secret"
	protected, _ := New(deps)
	if request(protected, "GET", "/stations/radio/player", nil, nil).Code != 401 {
		t.Fatal("metadata bypassed authentication")
	}
	server := httptest.NewServer(h)
	defer server.Close()
	eventCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, _ := http.NewRequestWithContext(eventCtx, "GET", server.URL+"/stations/radio/player", nil)
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
