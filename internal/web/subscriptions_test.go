package web

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/changefeed"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

func openSnapshots(t *testing.T, server *httptest.Server, path string) <-chan string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+path, nil)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		cancel()
		response.Body.Close()
		t.Fatal(response.StatusCode, response.Header)
	}
	done := make(chan struct{})
	messages := make(chan string, 16)
	go func() {
		defer close(done)
		scan := bufio.NewScanner(response.Body)
		scan.Buffer(make([]byte, 4096), 2<<20)
		for scan.Scan() {
			line := scan.Text()
			if strings.HasPrefix(line, "data: ") {
				var value string
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &value) == nil {
					select {
					case messages <- value:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		response.Body.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("observer did not release")
		}
	})
	return messages
}
func nextSnapshot(t *testing.T, messages <-chan string) string {
	t.Helper()
	select {
	case html := <-messages:
		return html
	case <-time.After(2 * time.Second):
		t.Fatal("missing committed-data snapshot")
		return ""
	}
}
func quietSnapshot(t *testing.T, messages <-chan string) {
	t.Helper()
	select {
	case value := <-messages:
		t.Fatal("unexpected snapshot", value)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOperationalSubscriptionsFollowCommitsAndReconnect(t *testing.T) {
	_, stations, src, cat := setupWeb(t, "")
	ctx := context.Background()
	if err := stations.Save(ctx, station.Station{ID: "radio", Name: "Radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	changes := &changefeed.Bus{}
	store := persistence.Operations{DB: stations.DB, Changes: changes}
	deps := Dependencies{Changes: changes, Stations: stations, Sources: src, Catalogue: cat, Operations: store}
	h, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	// Release observers before waiting for HTTP shutdown.
	t.Cleanup(server.CloseClientConnections)
	indicator := openSnapshots(t, server, "/producer/stations/radio/operations/indicator?subscribe=1")
	version := openSnapshots(t, server, "/producer/stations/radio/operations/version?kind=music&subscribe=1")
	overview := openSnapshots(t, server, "/producer/stations/radio/live?subscribe=1")
	initial := nextSnapshot(t, version)
	if strings.Contains(nextSnapshot(t, indicator), "active incident") {
		t.Fatal("invented incident")
	}
	if !strings.Contains(nextSnapshot(t, overview), "Nothing playing") {
		t.Fatal("missing overview")
	}
	event := operations.Event{ID: "failure", StationID: "radio", Kind: "failure", Outcome: "observed", Started: time.Now(), IncidentCode: "speech", Severity: "warning", Reason: "Speech failed"}
	if err := store.Save(ctx, event); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(nextSnapshot(t, indicator), "1 active incident") {
		t.Fatal("menu did not receive committed incident")
	}
	quietSnapshot(t, overview) // Background history never creates a manual review task.
	quietSnapshot(t, version)
	event.ID = "recovery"
	event.Recover = true
	if err := store.Save(ctx, event); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(nextSnapshot(t, indicator)) != "" {
		t.Fatal("automatic recovery did not clear incident indicator")
	}
	quietSnapshot(t, overview)
	event = operations.Event{ID: "music", StationID: "radio", Kind: "music", Outcome: "transmitting", Started: time.Now(), Title: "Song"}
	if err := store.Save(ctx, event); err != nil {
		t.Fatal(err)
	}
	started := nextSnapshot(t, version)
	if started == initial {
		t.Fatal("new music not announced")
	}
	event.AudioBytes = 128000
	if err := store.Save(ctx, event); err != nil {
		t.Fatal(err)
	}
	quietSnapshot(t, version) // Byte checkpoints are not producer-visible updates.
	event.Ended = time.Now()
	event.Outcome = "completed"
	if err := store.Save(ctx, event); err != nil {
		t.Fatal(err)
	}
	finished := nextSnapshot(t, version)
	if finished == started {
		t.Fatal("completion not announced")
	}
	reconnect := openSnapshots(t, server, "/producer/stations/radio/operations/version?kind=music&subscribe=1")
	if nextSnapshot(t, reconnect) != finished {
		t.Fatal("reconnect missed current snapshot")
	}
	deps.Password = "secret"
	protected, _ := New(deps)
	for _, path := range []string{"/live", "/audience", "/operations/version", "/operations/indicator"} {
		if response := request(protected, "GET", "/producer/stations/radio"+path+"?subscribe=1", nil, nil); response.Code != 401 {
			t.Fatal("subscription bypassed authentication", path, response.Code)
		}
	}
	invalid := request(h, "GET", "/producer/stations/radio/operations/version?from=invalid&subscribe=1", nil, nil)
	if invalid.Code != 400 || invalid.Header().Get("Content-Type") == "text/event-stream" {
		t.Fatal("invalid filter opened stream")
	}
}

func TestSubscriptionFiresActualExpiryDeadline(t *testing.T) {
	changes := &changefeed.Bus{}
	s := &Server{d: Dependencies{Changes: changes}, eventSlots: make(chan struct{}, 1)}
	deadline := time.Now().Add(150 * time.Millisecond)
	handler := s.subscription("test", "results", func(w http.ResponseWriter, r *http.Request) {
		if time.Now().Before(deadline) {
			w.Header().Set("X-Live-Deadline", deadline.Format(time.RFC3339Nano))
			_, _ = io.WriteString(w, "active")
		} else {
			_, _ = io.WriteString(w, "expired")
		}
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Cleanup(server.CloseClientConnections)
	snapshots := openSnapshots(t, server, "/?subscribe=1")
	if nextSnapshot(t, snapshots) != "active" || nextSnapshot(t, snapshots) != "expired" {
		t.Fatal("expiry transition missing")
	}
	quietSnapshot(t, snapshots)
}

func TestLiveUIHasNoPollingFallback(t *testing.T) {
	files, err := assets.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		raw, err := assets.ReadFile("templates/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		if regexp.MustCompile(`(?i)(?:data-)?hx-trigger\s*=\s*["'][^"']*\bevery\s+`).Match(raw) {
			t.Fatal("template polling is prohibited", file.Name())
		}
	}
	raw, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`\bsetInterval\s*\(`).Match(raw) {
		t.Fatal("periodic browser refresh is prohibited; subscribe to committed/runtime events")
	}
}

func TestAssistantSubscriptionFollowsDurableMessages(t *testing.T) {
	h, store, _ := setupAssistant(t, "", &assistantModel{})
	ctx := context.Background()
	if err := store.Create(ctx, producer.Conversation{ID: "chat", Title: "Test"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	snapshots := openSnapshots(t, server, "/producer/assistant/chat/messages?subscribe=1")
	nextSnapshot(t, snapshots)
	if err := store.Append(ctx, "chat", producer.Message{Role: "assistant", Text: "Finished the saved request"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(nextSnapshot(t, snapshots), "Finished the saved request") {
		t.Fatal("committed message missing")
	}
	quietSnapshot(t, snapshots)
}
func TestAudienceSubscriptionFollowsSubmitReviewAndUse(t *testing.T) {
	_, stations, src, cat := setupWeb(t, "")
	ctx := context.Background()
	now := time.Now().UTC()
	if err := stations.Save(ctx, station.Station{ID: "radio", Name: "Radio", Timezone: "UTC", Visibility: station.Listed}); err != nil {
		t.Fatal(err)
	}
	playlistsRepo := persistence.Playlists{DB: stations.DB}
	if err := playlistsRepo.Save(ctx, playlists.Playlist{ID: "pool", StationID: "radio", Name: "Music"}); err != nil {
		t.Fatal(err)
	}
	shows := persistence.Schedule{DB: stations.DB}
	show := schedule.Show{ID: "show", StationID: "radio", Name: "Show", DJID: "default-pop", PlaylistID: "pool", Day: (int(now.Weekday()) + 6) % 7, Start: 0, End: 0, AcceptShoutouts: true}
	if err := shows.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	show, err := shows.Get(ctx, "radio", "show")
	if err != nil {
		t.Fatal(err)
	}
	occurrence := schedule.Resolve([]schedule.Show{show}, "radio", "pool", "Music", now, time.UTC).Current
	changes := &changefeed.Bus{}
	repo := persistence.Audience{DB: stations.DB, Changes: changes}
	h, err := New(Dependencies{Changes: changes, Stations: stations, Sources: src, Catalogue: cat, Playlists: playlistsRepo, Schedule: shows, Audience: repo})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	snapshots := openSnapshots(t, server, "/producer/stations/radio/audience?subscribe=1")
	nextSnapshot(t, snapshots)
	message := audience.Message{ID: "hello", StationID: "radio", ShowID: "show", ShowName: "Show", OccurrenceKey: occurrence.Key, SenderKey: strings.Repeat("a", 64), Kind: audience.Shoutout, Status: audience.Pending, Name: "Sam", Text: "Hello from the studio", Created: now, ShowStart: occurrence.Start, Expires: occurrence.End}
	if err := repo.Submit(ctx, message); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(nextSnapshot(t, snapshots), message.Text) {
		t.Fatal("submission missing")
	}
	if err := repo.Decide(ctx, "radio", "hello", audience.Approved, "Producer approved", now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(nextSnapshot(t, snapshots), "Producer approved") {
		t.Fatal("review missing")
	}
	if err := repo.MarkUsed(ctx, "radio", []string{"hello"}, now); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(nextSnapshot(t, snapshots), message.Text) {
		t.Fatal("used contribution retained in active queue")
	}
	if err := repo.Decide(ctx, "radio", "hello", audience.Approved, "", now); err == nil {
		t.Fatal("used item reviewed")
	}
	quietSnapshot(t, snapshots)
}

func TestLatestTimelineLiveSnapshotsAndHistoricalViews(t *testing.T) {
	_, stations, src, cat := setupWeb(t, "")
	ctx := context.Background()
	if err := stations.Save(ctx, station.Station{ID: "radio", Name: "Radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	changes := &changefeed.Bus{}
	store := persistence.Operations{DB: stations.DB, Changes: changes}
	deps := Dependencies{Changes: changes, Stations: stations, Sources: src, Catalogue: cat, Operations: store}
	h, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	base := "/producer/stations/radio/timeline"
	for _, query := range []string{"", "?page=1", "?per_page=50"} {
		html := request(h, "GET", base+query, nil, nil).Body.String()
		if !strings.Contains(html, "data-history-live") || !strings.Contains(html, "data-live-url=") {
			t.Fatal("latest page is not live", query)
		}
	}
	for _, query := range []string{"?page=2", "?anchor=old", "?before=old", "?q=music", "?kind=music", "?outcome=completed", "?show=show", "?dj=dj", "?from=2026-10-01", "?until=2026-10-22"} {
		html := request(h, "GET", base+query, nil, nil).Body.String()
		if strings.Contains(html, "data-history-live") || !strings.Contains(html, "data-operations-version=") {
			t.Fatal("historical/filter view is not stable", query)
		}
	}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	t.Cleanup(server.CloseClientConnections)
	live := openSnapshots(t, server, base+"?subscribe=1")
	if !strings.Contains(nextSnapshot(t, live), "No matching broadcasts") {
		t.Fatal("missing initial empty state")
	}
	e := operations.Event{ID: "live-track", StationID: "radio", Kind: "music", Outcome: "transmitting", Started: time.Now().UTC(), Title: "New recording", Artist: "The Quartet"}
	if err := store.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	if html := nextSnapshot(t, live); !strings.Contains(html, "New recording") || !strings.Contains(html, "data-history-live") {
		t.Fatal("committed row not streamed", html)
	}
	e.AudioBytes = 500
	if err := store.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	quietSnapshot(t, live)
	e.Outcome = "completed"
	e.Ended = e.Started.Add(time.Minute)
	if err := store.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	completed := nextSnapshot(t, live)
	if !strings.Contains(completed, "Completed") {
		t.Fatal("completion missing")
	}
	reconnect := openSnapshots(t, server, base+"?subscribe=1")
	if nextSnapshot(t, reconnect) != completed {
		t.Fatal("reconnect missed latest rows")
	}
	if response := request(h, "GET", base+"?from=invalid&subscribe=1", nil, nil); response.Code != 400 {
		t.Fatal("invalid subscription accepted", response.Code)
	}
	deps.Password = "secret"
	protected, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	if response := request(protected, "GET", base+"?subscribe=1", nil, nil); response.Code != 401 {
		t.Fatal("timeline subscription bypassed authentication", response.Code)
	}
}
