package upnp

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func soap(raw string, returned, total int) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(raw))
	return fmt.Sprintf(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><Result>%s</Result><NumberReturned>%d</NumberReturned><TotalMatches>%d</TotalMatches><UpdateID>1</UpdateID></u:BrowseResponse></s:Body></s:Envelope>`, b.String(), returned, total)
}
func testProvider(t *testing.T, h http.Handler) (*Provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := New(sources.Config{ID: "source", Kind: "upnp", Name: "Music", Endpoint: srv.URL + "/desc"})
	if err != nil {
		t.Fatal(err)
	}
	p.allowLoopback = true
	return p, srv
}
func TestUPnPContractAndCatalogueImport(t *testing.T) {
	desc := fixture(t, "description.xml")
	music := strings.Replace(fixture(t, "music.xml"), "<upnp:genre>Jazz</upnp:genre>", "<upnp:genre>Jazz, Blues</upnp:genre><upnp:genre>Disco</upnp:genre>", 1)
	music = strings.Replace(music, "<dc:title>", "<dc:date>1984-06-01</dc:date><dc:title>", 1)
	var requests atomic.Int32
	p, srv := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/desc" {
			io.WriteString(w, desc)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/control" || !strings.Contains(r.Header.Get("SOAPAction"), "#Browse") {
			t.Errorf("bad SOAP request %s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "<Filter>*</Filter>") {
			t.Error("generic UPnP filter changed")
		}
		if strings.Contains(string(b), "BrowseMetadata") && !strings.Contains(string(b), "track&amp;1") {
			t.Errorf("object ID not XML escaped: %s", b)
		}
		io.WriteString(w, soap(music, 1, 1))
	}))
	var contract sources.AudioSource = p
	profile := contract.Profile()
	if !profile.Capabilities.Browse || profile.Capabilities.Search || profile.Restrictions.PublicBroadcast != sources.OperatorResponsible {
		t.Fatal(profile)
	}
	ctx := context.Background()
	page, err := contract.Browse(ctx, "0", 0, 100)
	if err != nil || len(page.Entries) != 1 {
		t.Fatal(page, err)
	}
	a := page.Entries[0].Item
	if a.Year != 1984 || a.Title != "Track & One" || a.Artist != "Artist" || a.Composer != "Composer" || a.Genre != "Jazz, Blues, Disco" || a.Duration == nil || *a.Duration != 221250*time.Millisecond || a.Artwork != srv.URL+"/art.jpg" || len(a.Resources) != 2 {
		t.Fatal(a)
	}
	got, err := contract.Get(ctx, "track&1")
	if err != nil || got.ID != a.ID {
		t.Fatal(got, err)
	}
	res, err := contract.Resolve(ctx, "track&1")
	if err != nil || len(res) != 2 || res[0].MIME != "audio/flac" {
		t.Fatal(res, err)
	}
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := persistence.Stations{DB: db}
	if err = st.Save(ctx, station.Station{ID: "station", Name: "Test", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	src := persistence.Sources{DB: db}
	c := sources.Config{ID: "source", Name: "Music", Kind: "upnp", Endpoint: srv.URL + "/desc", Revision: 1}
	if err = src.Save(ctx, c); err != nil {
		t.Fatal(err)
	}
	cat := persistence.Catalogue{DB: db}
	s := catalogue.Sync{Sources: src, Catalogue: cat, Factory: func(context.Context, sources.Config) (sources.AudioSource, error) { return p, nil }}
	n, err := s.Run(ctx, c.ID)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	result, err := cat.Search(ctx, catalogue.Query{Text: "One", Limit: 50})
	if err != nil || result.Total != 1 || result.Items[0].ID != a.ID {
		t.Fatal(result, err)
	}
	if requests.Load() != 5 {
		t.Fatal("initialization should be cached", requests.Load())
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Get(ctx, "track&1"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
func TestOfflineInitializationRetry(t *testing.T) {
	desc := fixture(t, "description.xml")
	var online atomic.Bool
	p, _ := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !online.Load() {
			http.Error(w, "offline", 503)
			return
		}
		if r.URL.Path == "/desc" {
			io.WriteString(w, desc)
		} else {
			io.WriteString(w, soap(`<DIDL-Lite/>`, 0, 0))
		}
	}))
	if _, err := p.Browse(context.Background(), "0", 0, 100); err == nil {
		t.Fatal("offline accepted")
	}
	online.Store(true)
	if _, err := p.Browse(context.Background(), "0", 0, 100); err != nil {
		t.Fatal(err)
	}
}
func TestRejectedResponses(t *testing.T) {
	desc := fixture(t, "description.xml")
	music := fixture(t, "music.xml")
	cases := map[string]string{"missing fields": `<Envelope><Body><BrowseResponse/></Body></Envelope>`, "wrong count": soap(music, 2, 2), "negative total": soap(music, 1, -1), "bad XML": `<Envelope>`, "extra SOAP": soap(music, 1, 1) + `<extra/>`, "unsafe audio": soap(strings.ReplaceAll(music, "/music.flac", "http://evil.invalid/track"), 1, 1), "bad DIDL": soap(`<NotDIDL/>`, 0, 0), "extra DIDL": soap(`<DIDL-Lite/><extra/>`, 0, 0), "bad duration": soap(strings.ReplaceAll(music, "0:03:41.250", "NaN"), 1, 1)}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			p, _ := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/desc" {
					io.WriteString(w, desc)
				} else {
					io.WriteString(w, response)
				}
			}))
			if _, err := p.Browse(context.Background(), "0", 0, 100); err == nil {
				t.Fatal("bad response accepted")
			}
		})
	}
}
func TestDescriptionSecurityAndRedirects(t *testing.T) {
	for name, desc := range map[string]string{"outside control": `<root><device><serviceList><service><serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType><controlURL>http://evil.invalid/control</controlURL></service></serviceList></device></root>`, "outside base": strings.ReplaceAll(fixture(t, "description.xml"), "<root>", "<root><URLBase>http://evil.invalid/</URLBase>"), "no directory": `<root><device/></root>`, "malformed": `<root>`} {
		t.Run(name, func(t *testing.T) {
			p, _ := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, desc) }))
			if _, err := p.Browse(context.Background(), "0", 0, 1); err == nil {
				t.Fatal("unsafe description accepted")
			}
		})
	}
	p, _ := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://evil.invalid/", 302) }))
	if _, err := p.Browse(context.Background(), "0", 0, 1); err == nil {
		t.Fatal("cross-server redirect allowed")
	}
}
func TestTransportScope(t *testing.T) {
	for _, raw := range []string{"file:///x", "http://user:pass@server/x", "http://server/x#f", "http://server:22/x", "http://server:70000/x"} {
		if _, err := validURL(raw); err == nil {
			t.Fatal(raw)
		}
	}
	u, _ := validURL("http://127.0.0.1:9790/x")
	if _, err := newScope(context.Background(), u, false); err == nil {
		t.Fatal("loopback allowed")
	}
	u, _ = validURL("http://169.254.169.254/x")
	if _, err := newScope(context.Background(), u, false); err == nil {
		t.Fatal("metadata host allowed")
	}
	u, _ = validURL("http://192.168.1.20:9790/x")
	s, err := newScope(context.Background(), u, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.check("http://192.168.1.20:9791/music"); err != nil {
		t.Fatal(err)
	}
	if s.check("http://192.168.1.21:9790/music") == nil {
		t.Fatal("outside address accepted")
	}
	if s.check("http://192.168.1.20:22/music") == nil {
		t.Fatal("restricted port accepted")
	}
}
func TestUnsupportedObjectsAndInvalidRequests(t *testing.T) {
	p, _ := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/desc" {
			io.WriteString(w, fixture(t, "description.xml"))
		} else {
			io.WriteString(w, soap(`<DIDL-Lite><container id="folder"><title>Folder</title></container><item id="video"><title>Video</title><class>object.item.videoItem</class></item></DIDL-Lite>`, 2, 2))
		}
	}))
	page, err := p.Browse(context.Background(), "0", 0, 100)
	if err != nil || len(page.Entries) != 2 || page.Entries[0].ContainerID != "folder" || page.Entries[1].Item != nil {
		t.Fatal(page, err)
	}
	for _, v := range []struct {
		o    string
		n, c int
	}{{"", 0, 1}, {"0", -1, 1}, {"0", 0, 101}} {
		if _, err = p.Browse(context.Background(), v.o, v.n, v.c); err == nil {
			t.Fatal(v)
		}
	}
	if _, err = p.Get(context.Background(), "folder"); err == nil {
		t.Fatal("folder returned as audio")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = p.Browse(ctx, "0", 0, 1); err == nil {
		t.Fatal("cancellation ignored")
	}
}
func TestReadLimitsAndDuration(t *testing.T) {
	res := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxXML+1)))}
	if _, err := readXML(res); err == nil {
		t.Fatal("unbounded XML")
	}
	for _, raw := range []string{"-1:00:00", "0:60:00", "0:00:NaN", "0:00:60", "bad"} {
		if _, err := duration(raw); err == nil {
			t.Fatal(raw)
		}
	}
	if d, err := duration(""); err != nil || d != nil {
		t.Fatal(d, err)
	}
	if _, err := Factory(context.Background(), sources.Config{}); err == nil {
		t.Fatal("bad factory config accepted")
	}
	if _, err := decodeTest(); err == nil {
		t.Fatal("truncated XML accepted")
	}
}
func decodeTest() (any, error) { var v struct{}; err := decode([]byte(`<x>`), &v); return v, err }

func TestMinimServerImportUsesFlatItemsView(t *testing.T) {
	desc := strings.Replace(fixture(t, "description.xml"), "<device>", "<device><manufacturer>minimserver.com</manufacturer><modelName>MinimServer</modelName>", 1)
	var itemPages atomic.Int32
	p, srv := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/desc" {
			io.WriteString(w, desc)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var request struct {
			Body struct {
				Browse struct {
					Object string `xml:"ObjectID"`
					Filter string `xml:"Filter"`
					Offset int    `xml:"StartingIndex"`
				} `xml:"Browse"`
			} `xml:"Body"`
		}
		if err := xml.Unmarshal(raw, &request); err != nil {
			t.Error(err)
			http.Error(w, "bad XML", 400)
			return
		}
		if request.Body.Browse.Filter != "*,upnp:userAnnotation[tag.OriginalDate]" {
			t.Errorf("missing extended metadata filter: %q", request.Body.Browse.Filter)
		}
		switch request.Body.Browse.Object {
		case "0":
			io.WriteString(w, soap(`<DIDL-Lite><container id="0$=Artist"><title>Artist</title></container><container id="0$items"><title>101 items</title></container><container id="0$albums"><title>Albums</title></container></DIDL-Lite>`, 3, 3))
		case "0$items":
			itemPages.Add(1)
			var didl strings.Builder
			didl.WriteString(`<DIDL-Lite>`)
			offset := request.Body.Browse.Offset
			count := min(100, 101-offset)
			for n := offset; n < offset+count; n++ {
				fmt.Fprintf(&didl, `<item id="track%d"><title>Track %d</title><class>object.item.audioItem.musicTrack</class><date>2008-10-06</date><userAnnotation>tag.OriginalDate=1987</userAnnotation><genre>Pop</genre><res protocolInfo="http-get:*:audio/mpeg:*">/track%d.mp3</res></item>`, n, n, n)
			}
			didl.WriteString(`</DIDL-Lite>`)
			io.WriteString(w, soap(didl.String(), count, 101))
		default:
			t.Errorf("import expanded virtual navigation folder: %q", request.Body.Browse.Object)
			http.Error(w, "virtual view must not be indexed", 500)
		}
	}))
	ctx := context.Background()
	db, err := persistence.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := (persistence.Stations{DB: db}).Save(ctx, station.Station{ID: "station", Name: "Test", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	src := persistence.Sources{DB: db}
	config := sources.Config{ID: "source", Name: "Music", Kind: "upnp", Endpoint: srv.URL + "/desc", Revision: 1}
	if err := src.Save(ctx, config); err != nil {
		t.Fatal(err)
	}
	cat := persistence.Catalogue{DB: db}
	if _, err := db.Exec(`INSERT INTO station_sources(station_id,source_id) SELECT stations.id,sources.id FROM stations CROSS JOIN sources`); err != nil {
		t.Fatal(err)
	}
	syncer := catalogue.Sync{Sources: src, Catalogue: cat, Factory: func(context.Context, sources.Config) (sources.AudioSource, error) { return p, nil }}
	for n := 0; n < 2; n++ {
		count, err := syncer.Run(ctx, config.ID)
		if err != nil || count != 101 {
			t.Fatal(count, err)
		}
	}
	result, err := cat.Search(ctx, catalogue.Query{Limit: 100, YearFrom: 1980, YearTo: 1989, Genre: "Pop"})
	if err != nil || result.Total != 101 || itemPages.Load() != 4 {
		t.Fatal(result.Total, itemPages.Load(), err)
	}
	repo := persistence.Playlists{DB: db}
	if err := repo.Save(ctx, playlists.Playlist{ID: "eighties", StationID: "station", Name: "1980s Pop"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceRules(ctx, "eighties", []playlists.Rule{{ID: "pop", Kind: "genre", Value: "Pop"}, {ID: "decade", Kind: "decade", Value: "1980"}}); err != nil {
		t.Fatal(err)
	}
	eligible, err := repo.Resolve(ctx, "eighties", 0, 100)
	if err != nil || eligible.Total != 101 {
		t.Fatalf("original-date playlist: total=%d err=%v", eligible.Total, err)
	}

	for _, item := range result.Items {
		if item.Year != 1987 || item.OriginalDate != "1987" || item.ReleaseDate != "2008-10-06" {
			t.Fatalf("lost date provenance: %+v", item)
		}
	}
}

func TestMinimServerMissingItemsViewFails(t *testing.T) {
	desc := strings.Replace(fixture(t, "description.xml"), "<device>", "<device><manufacturer>minimserver.com</manufacturer><modelName>MinimServer</modelName>", 1)
	p, _ := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/desc" {
			io.WriteString(w, desc)
		} else {
			io.WriteString(w, soap(`<DIDL-Lite><container id="0$=Artist"><title>Artist</title></container></DIDL-Lite>`, 1, 1))
		}
	}))
	if _, err := p.CatalogueRoot(context.Background()); err == nil {
		t.Fatal("missing complete view accepted")
	}
}

// Gerbera's SDK closes SOAP connections without a Connection: close response
// header. A subsequent POST must never race that close on a pooled connection.
func TestBrowseUsesFreshConnectionsForClosingUPnPServers(t *testing.T) {
	desc := fixture(t, "description.xml")
	music := fixture(t, "music.xml")
	connections := map[string]bool{}
	var mu sync.Mutex
	p, _ := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/desc" {
			io.WriteString(w, desc)
			return
		}
		if !r.Close {
			t.Error("SOAP request permits reuse of a closing connection")
		}
		mu.Lock()
		if connections[r.RemoteAddr] {
			t.Error("SOAP requests reused a connection")
		}
		connections[r.RemoteAddr] = true
		mu.Unlock()
		io.Copy(io.Discard, r.Body)
		response := soap(music, 1, 1)
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nContent-Type: text/xml\r\nContent-Length: %d\r\n\r\n%s", len(response), response)
		if err := buf.Flush(); err != nil {
			t.Error(err)
		}
	}))
	for i := 0; i < 5; i++ {
		page, err := p.Browse(context.Background(), "0", 0, 100)
		if err != nil || len(page.Entries) != 1 {
			t.Fatalf("browse %d: page=%+v err=%v", i, page, err)
		}
	}
}

func TestGerberaCatalogueUsesDiscoveredAllAudioView(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			p, _ := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/desc" {
					io.WriteString(w, `<root><device><manufacturer>Gerbera Contributors</manufacturer><modelName>Gerbera</modelName><serviceList><service><serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType><controlURL>/control</controlURL></service></serviceList></device></root>`)
					return
				}
				body, _ := io.ReadAll(r.Body)
				didl := `<DIDL-Lite xmlns:dc="http://purl.org/dc/elements/1.1/">`
				if strings.Contains(string(body), "<ObjectID>0</ObjectID>") {
					didl += `<container id="custom-audio"><dc:title>Audio</dc:title></container>`
				} else if strings.Contains(string(body), "<ObjectID>custom-audio</ObjectID>") {
					title := "All Audio"
					if missing {
						title = "Albums"
					}
					didl += `<container id="custom-all"><dc:title>` + title + `</dc:title></container>`
				} else {
					t.Errorf("unexpected browse: %s", body)
				}
				io.WriteString(w, soap(didl+`</DIDL-Lite>`, 1, 1))
			}))
			root, err := p.CatalogueRoot(context.Background())
			if missing {
				if err == nil {
					t.Fatal("missing complete view silently fell back to virtual graph", root)
				}
				return
			}
			if err != nil || root != "custom-all" {
				t.Fatal(root, err)
			}
		})
	}
}
