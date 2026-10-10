package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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

type tickingEncoder struct{}

func (tickingEncoder) Open(ctx context.Context, _ content.AudioItem) (io.ReadCloser, error) {
	reader, writer := io.Pipe()
	context.AfterFunc(ctx, func() { reader.Close(); writer.Close() })
	go func() {
		defer writer.Close()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := writer.Write([]byte("shared audio")); err != nil {
					return
				}
			}
		}
	}()
	return reader, nil
}
func TestExternalStreamsAndPlaylistSetup(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := persistence.Stations{DB: db}
	src := persistence.Sources{DB: db}
	cat := persistence.Catalogue{DB: db}
	pr := persistence.Playlists{DB: db}
	config := persistence.Broadcast{DB: db}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := broadcast.New(config, tickingEncoder{}, logger, broadcast.Options{})
	defer m.Close()
	a, err := (station.Service{Repo: st}).Save(ctx, station.Station{Name: "Jazz 24", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := (sources.Service{Repo: src}).Save(ctx, sources.Config{Name: "Music", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	item := content.AudioItem{ID: content.ID(c.ID, "one"), SourceID: c.ID, SourceItemID: "one", Title: "One", Kind: content.Track, Origin: content.Original}
	cat.Replace(ctx, c, []content.AudioItem{item})
	pr.Save(ctx, playlists.Playlist{ID: "pool", StationID: a.ID, Name: "Pool"})
	pr.SetIncluded(ctx, "pool", []string{item.ID}, true)
	h, err := New(Dependencies{Stations: st, Sources: src, Catalogue: cat, Playlists: pr, Broadcast: m, BroadcastConfig: config, PublicURL: "https://radio.example", Ready: db.PingContext, Password: "secret", Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Basic YWRtaW46c2VjcmV0"}
	w := request(h, "GET", "/stations/"+a.ID, nil, headers)
	if !strings.Contains(w.Body.String(), "https://radio.example/streams/jazz-24") || !strings.Contains(w.Body.String(), `id="radio-player"`) || !strings.Contains(w.Body.String(), `hx-history-elt`) {
		t.Fatal(w.Body.String())
	}
	if request(h, "HEAD", "/streams/jazz-24", nil, nil).Code != 200 || m.State(a.ID).Listeners != 0 {
		t.Fatal("HEAD starts listening")
	}
	if request(h, "GET", "/stations/"+a.ID+"/live", nil, nil).Code != 401 {
		t.Fatal("management status exposed")
	}
	if request(h, "GET", "/streams/jazz-24", nil, nil).Code != 503 {
		t.Fatal("unassigned station starts")
	}
	token := csrfWithAuth(t, h, headers)
	w = request(h, "POST", "/stations/"+a.ID+"/broadcast", url.Values{"csrf": {token}, "playlist": {"pool"}}, headers)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Config.WriteTimeout = 50 * time.Millisecond
	srv.Start()
	defer srv.Close()
	probe, _ := http.NewRequest("GET", srv.URL+"/streams/jazz-24", nil)
	probe.Header.Set("Range", "bytes=0-1")
	first, err := http.DefaultClient.Do(probe)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	join, _ := http.NewRequest("GET", srv.URL+"/streams/jazz-24", nil)
	join.Header.Set("Range", "bytes=100-")
	second, err := http.DefaultClient.Do(join)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	if first.StatusCode != 200 || second.StatusCode != 200 || first.Header.Get("Content-Range") != "" || second.Header.Get("Content-Range") != "" || first.Header.Get("Content-Type") != "audio/mpeg" || first.Header.Get("Accept-Ranges") != "none" || m.State(a.ID).Listeners != 2 {
		t.Fatal(first.StatusCode, first.Header, m.State(a.ID))
	}
	buf := make([]byte, len("shared audio"))
	if _, err := io.ReadFull(second.Body, buf); err != nil || string(buf) != "shared audio" {
		t.Fatal(string(buf), err)
	}
	longer := make([]byte, 240)
	if _, err := io.ReadFull(first.Body, longer); err != nil {
		t.Fatal("ordinary response timeout cut off broadcast", err)
	}
	sessionURL := srv.URL + "/streams/jazz-24?listener=0123456789abcdef0123456789abcdef"
	appProbe, _ := http.NewRequest("GET", sessionURL, nil)
	appProbe.Header.Set("Range", "bytes=0-1")
	appFirst, err := http.DefaultClient.Do(appProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer appFirst.Body.Close()
	appSecond, err := http.Get(sessionURL)
	if err != nil {
		t.Fatal(err)
	}
	defer appSecond.Body.Close()
	if appFirst.StatusCode != 200 || appSecond.StatusCode != 200 || m.State(a.ID).Listeners != 3 {
		t.Fatal("one app plus two independent devices must count as three", m.State(a.ID))
	}
	if _, err := io.ReadFull(appSecond.Body, buf); err != nil {
		t.Fatal("replacement cannot play", err)
	}
	_, _ = io.Copy(io.Discard, appFirst.Body)
	appFirst.Body.Close()
	if m.State(a.ID).Listeners != 3 {
		t.Fatal("closing old probe removed playback")
	}
	appSecond.Body.Close()
	invalid, err := http.Get(srv.URL + "/streams/jazz-24?listener=invalid")
	if err != nil {
		t.Fatal(err)
	}
	invalid.Body.Close()
	if invalid.StatusCode != 400 {
		t.Fatal("invalid client identity admitted")
	}
	first.Body.Close()
	second.Body.Close()
	deadline := time.Now().Add(time.Second)
	for m.State(a.ID).Status != "Dormant" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m.State(a.ID).Status != "Dormant" {
		t.Fatal("disconnect leaves broadcast running")
	}
	a.StreamSlug = "jazz-live"
	if err := st.Save(ctx, a); err != nil {
		t.Fatal(err)
	}
	if request(h, "HEAD", "/streams/jazz-24", nil, nil).Code != 404 || request(h, "HEAD", "/streams/jazz-live", nil, nil).Code != 200 {
		t.Fatal("edited address not applied")
	}
	if request(h, "HEAD", "/streams/jazz-live", nil, map[string]string{"Range": "bytes=100-"}).Code != 200 {
		t.Fatal("HEAD range probe must advertise the live stream without starting playback")
	}
}
func csrfWithAuth(t *testing.T, h http.Handler, headers map[string]string) string {
	t.Helper()
	w := request(h, "GET", "/station", nil, headers)
	start := strings.Index(w.Body.String(), `name="csrf" value="`)
	if start < 0 {
		t.Fatal(w.Body.String())
	}
	value := w.Body.String()[start+len(`name="csrf" value="`):]
	return strings.SplitN(value, `"`, 2)[0]
}
