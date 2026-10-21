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
	"stationos.local/stationos/internal/changefeed"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
	"time"
)

func TestStationEventsOnlySendChangedViewsAndKeepAuthentication(t *testing.T) {
	_, st, src, cat := setupWeb(t, "")
	ctx := context.Background()
	if err := st.Save(ctx, station.Station{ID: "radio", Name: "Before", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	m := broadcast.New(persistence.Broadcast{DB: st.DB}, tickingEncoder{}, slog.New(slog.NewTextHandler(io.Discard, nil)), broadcast.Options{})
	defer m.Close()
	changes := &changefeed.Bus{}
	store := persistence.Operations{DB: st.DB, Changes: changes}
	deps := Dependencies{Changes: changes, Operations: store, Stations: st, Sources: src, Catalogue: cat, Playlists: persistence.Playlists{DB: st.DB}, Broadcast: m, BroadcastConfig: persistence.Broadcast{DB: st.DB}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	home := request(h, "GET", "/producer", nil, nil).Body.String()
	if strings.Contains(home, `hx-trigger="every 3s"`) || !strings.Contains(home, `data-station-events="/producer/stations/events"`) {
		t.Fatal("cards still poll", home)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	eventCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, _ := http.NewRequestWithContext(eventCtx, "GET", server.URL+"/producer/stations/events", nil)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal(response.Header)
	}
	messages := make(chan string, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		scan := bufio.NewScanner(response.Body)
		scan.Buffer(make([]byte, 4096), 1<<20)
		for scan.Scan() {
			line := scan.Text()
			if strings.HasPrefix(line, "data: ") {
				var html string
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &html) == nil {
					messages <- html
				}
			}
		}
	}()
	receive := func() string {
		select {
		case v := <-messages:
			return v
		case <-time.After(time.Second):
			t.Fatal("missing event")
			return ""
		}
	}
	if !strings.Contains(receive(), "Before") || m.State("radio").Listeners != 0 {
		t.Fatal("initial view missing or counted as listener")
	}
	m.Changed()
	select {
	case <-messages:
		t.Fatal("unchanged view sent another update")
	case <-time.After(50 * time.Millisecond):
	}
	if err := st.Save(ctx, station.Station{ID: "radio", Name: "After", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	m.Changed()
	if !strings.Contains(receive(), "After") {
		t.Fatal("changed station not sent")
	}
	if err := st.Save(ctx, station.Station{ID: "radio", Name: "Episode update", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	changes.Publish("episodes")
	if !strings.Contains(receive(), "Episode update") {
		t.Fatal("episode commit did not refresh dashboard")
	}
	event := operations.Event{ID: "dashboard-failure", StationID: "radio", Kind: "failure", Outcome: "observed", Started: time.Now(), IncidentCode: "speech", Severity: "warning", Reason: "Speech failed"}
	if err := store.Save(ctx, event); err != nil {
		t.Fatal(err)
	}
	if html := receive(); !strings.Contains(html, "1 active incident") || !strings.Contains(html, `class="dashboard-attention"`) {
		t.Fatal("dashboard missed committed incident", html)
	}
	event.ID = "dashboard-recovery"
	event.Recover = true
	if err := store.Save(ctx, event); err != nil {
		t.Fatal(err)
	}
	if html := receive(); strings.Contains(html, `class="dashboard-attention"`) {
		t.Fatal("dashboard missed recovery", html)
	}
	cancel()
	response.Body.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event stream did not release on cancellation")
	}
	deps.Password = "secret"
	protected, _ := New(deps)
	if request(protected, "GET", "/producer/stations/events", nil, nil).Code != http.StatusUnauthorized {
		t.Fatal("events bypass authentication")
	}
}
