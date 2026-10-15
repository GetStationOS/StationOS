package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/sources"
)

type fakeSource struct{}

func (fakeSource) Profile() sources.Profile {
	return sources.Profile{Capabilities: sources.Capabilities{Browse: true}}
}
func (fakeSource) Browse(context.Context, string, int, int) (sources.Page, error) {
	return sources.Page{Total: 1, Entries: []sources.Entry{{Item: &content.AudioItem{SourceItemID: "track", Title: "<script>alert(1)</script>", Kind: content.Track, Origin: content.Original}}}}, nil
}
func (fakeSource) Get(context.Context, string) (content.AudioItem, error) {
	return content.AudioItem{}, sources.ErrUnsupported
}
func (fakeSource) Resolve(context.Context, string) ([]content.Resource, error) {
	return nil, sources.ErrUnsupported
}
func setupWeb(t *testing.T, password string, discovery ...sources.Discoverer) (http.Handler, persistence.Stations, persistence.Sources, persistence.Catalogue) {
	t.Helper()
	db, err := persistence.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st := persistence.Stations{DB: db}
	src := persistence.Sources{DB: db}
	cat := persistence.Catalogue{DB: db}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	syncer := catalogue.Sync{Sources: src, Catalogue: cat, Logger: logger, Factory: func(context.Context, sources.Config) (sources.AudioSource, error) { return fakeSource{}, nil }}
	djsRepo, err := persistence.NewDJs(db, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	var discoverer sources.Discoverer
	if len(discovery) > 0 {
		discoverer = discovery[0]
	}
	h, err := New(Dependencies{SourceDiscovery: discoverer, Mixer: persistence.Mixer{DB: db}, Schedule: persistence.Schedule{DB: db}, DJs: djsRepo, Stations: st, Sources: src, Catalogue: cat, Playlists: persistence.Playlists{DB: db}, Sync: syncer, Ready: db.PingContext, Password: password, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	return h, st, src, cat
}
func request(h http.Handler, method, path string, values url.Values, headers map[string]string) *httptest.ResponseRecorder {
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	r := httptest.NewRequest(method, path, body)
	if values != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func csrf(t *testing.T, h http.Handler) string {
	t.Helper()
	w := request(h, "GET", "/producer/station", nil, nil)
	m := regexp.MustCompile(`name="csrf" value="([a-f0-9]+)"`).FindStringSubmatch(w.Body.String())
	if len(m) != 2 {
		t.Fatal("missing CSRF token", w.Body.String())
	}
	return m[1]
}
func TestFoundationWebJourney(t *testing.T) {
	h, st, src, cat := setupWeb(t, "")
	token := csrf(t, h)
	ctx := context.Background()
	w := request(h, "POST", "/producer/station", url.Values{"csrf": {token}, "name": {"Test station"}, "timezone": {"UTC"}, "locale": {"en"}}, nil)
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "/producer/stations/") {
		t.Fatal(w.Code, w.Body.String())
	}
	stations, err := st.List(ctx)
	if err != nil || len(stations) != 1 {
		t.Fatal(stations, err)
	}
	w = request(h, "POST", "/producer/sources", url.Values{"csrf": {token}, "station": {stations[0].ID}, "name": {"Music"}, "endpoint": {"http://server:9790/desc"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	ss, err := src.List(ctx)
	if err != nil || len(ss) != 1 {
		t.Fatal(ss, err)
	}
	w = request(h, "POST", "/producer/sources/"+ss[0].ID+"/sync", url.Values{"csrf": {token}}, map[string]string{"HX-Request": "true"})
	if w.Code != 204 || !strings.Contains(w.Header().Get("HX-Location"), `"path":"/producer/content?notice=imported"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	got, _ := cat.Search(ctx, catalogue.Query{Limit: 50})
	if got.Total != 1 {
		t.Fatal(got)
	}
	w = request(h, "GET", "/producer/content?q=alert", nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>alert") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/producer", nil, nil)
	if !strings.Contains(w.Body.String(), "Dormant") {
		t.Fatal("invented playout status")
	}
	w = request(h, "GET", "/producer/api/v1/status", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"catalogue_items":1`) || !strings.Contains(w.Body.String(), `"playout":"unavailable"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// Editing configuration keeps its identity and requires an explicit subsequent import.
	w = request(h, "POST", "/producer/station", url.Values{"csrf": {token}, "id": {stations[0].ID}, "name": {"Updated"}, "timezone": {"Europe/London"}}, nil)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	updated, _ := st.Get(ctx, stations[0].ID)
	if updated.Name != "Updated" {
		t.Fatal(updated)
	}
}
func TestPagesFragmentsAndAssets(t *testing.T) {
	h, _, _, _ := setupWeb(t, "")
	for _, path := range []string{"/producer", "/producer/station", "/producer/sources", "/producer/sources/new", "/producer/playlists/new", "/producer/content", "/healthz", "/readyz", "/producer/api/v1/status", "/static/app.css", "/static/app.js", "/static/htmx.min.js"} {
		w := request(h, "GET", path, nil, nil)
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("%s %d", path, w.Code)
		}
	}
	w := request(h, "GET", "/producer/content", nil, map[string]string{"HX-Request": "true"})
	if w.Code != 200 || strings.Contains(w.Body.String(), "<!doctype") || !strings.Contains(w.Body.String(), "Browse tracks") {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/missing", "/producer/station/extra"} {
		if w := request(h, "GET", path, nil, nil); w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	if w := request(h, "DELETE", "/producer/station", nil, nil); w.Code != 405 {
		t.Fatal(w.Code)
	}
}
func TestWriteSecurityAndValidation(t *testing.T) {
	h, _, _, _ := setupWeb(t, "")
	token := csrf(t, h)
	base := url.Values{"csrf": {token}, "name": {"Test"}, "timezone": {"UTC"}}
	if w := request(h, "POST", "/producer/station", base, map[string]string{"Origin": "https://evil.example"}); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request(h, "POST", "/producer/station", url.Values{"name": {"Test"}}, nil); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request(h, "POST", "/producer/station", url.Values{"csrf": {token}, "name": {"A"}, "timezone": {"wrong"}}, nil); w.Code != 422 {
		t.Fatal(w.Code)
	}
	base.Set("id", "missing")
	if w := request(h, "POST", "/producer/station", base, nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := request(h, "POST", "/producer/sources", url.Values{"csrf": {token}, "station": {"missing"}, "name": {"Music"}, "endpoint": {"http://server/desc"}}, nil); w.Code != 303 {
		t.Fatal(w.Code)
	}
	if w := request(h, "POST", "/producer/sources/missing/sync", url.Values{"csrf": {token}}, nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := request(h, "POST", "/producer/station", url.Values{"csrf": {token}, "name": {strings.Repeat("x", 20000)}}, nil); w.Code != 422 {
		t.Fatal(w.Code)
	}
	for _, path := range []string{"/producer/content?offset=-1", "/producer/content?offset=bad", "/producer/content?q=" + strings.Repeat("x", 201)} {
		if w := request(h, "GET", path, nil, nil); w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
}
func TestAuthenticationAndReadiness(t *testing.T) {
	h, st, _, _ := setupWeb(t, "secret")
	if w := request(h, "GET", "/producer", nil, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("GET", "/producer", nil)
	r.SetBasicAuth("admin", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(h, "GET", "/healthz", nil, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	st.DB.Close()
	if w := request(h, "GET", "/readyz", nil, nil); w.Code != 503 {
		t.Fatal(w.Code)
	}
	h2, err := New(Dependencies{Ready: func(context.Context) error { return errors.New("secret database error") }})
	if err != nil {
		t.Fatal(err)
	}
	w = request(h2, "GET", "/producer/api/v1/status", nil, nil)
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestFailedImportAndDatabaseFailure(t *testing.T) {
	h, st, src, cat := setupWeb(t, "")
	token := csrf(t, h)
	request(h, "POST", "/producer/station", url.Values{"csrf": {token}, "name": {"Test"}, "timezone": {"UTC"}}, nil)
	ss, _ := st.List(context.Background())
	request(h, "POST", "/producer/sources", url.Values{"csrf": {token}, "name": {"Music"}, "station": {ss[0].ID}, "endpoint": {"http://server/desc"}}, nil)
	configs, _ := src.List(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := New(Dependencies{Stations: st, Sources: src, Catalogue: cat, Ready: st.DB.PingContext, Logger: logger, Sync: catalogue.Sync{Sources: src, Catalogue: cat, Logger: logger, Factory: func(context.Context, sources.Config) (sources.AudioSource, error) {
		return nil, errors.New("private provider URL")
	}}})
	if err != nil {
		t.Fatal(err)
	}
	token = csrf(t, h)
	w := request(h, "POST", "/producer/sources/"+configs[0].ID+"/sync", url.Values{"csrf": {token}}, nil)
	if w.Code != 502 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body.String())
	}
	st.DB.Close()
	w = request(h, "GET", "/producer", nil, nil)
	if w.Code != 500 || strings.Contains(w.Body.String(), "sql") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestNativeFormOriginPolicy(t *testing.T) {
	h, stations, _, _ := setupWeb(t, "")
	page := request(h, "GET", "/producer/station", nil, nil)
	// Ordinary browser forms honour this policy when constructing their Origin header.
	if got := page.Header().Get("Referrer-Policy"); got != "same-origin" {
		t.Fatalf("native form submissions need their same-origin identity, got policy %q", got)
	}
	token := csrf(t, h)
	values := url.Values{"csrf": {token}, "name": {"Browser station"}, "timezone": {"UTC"}}
	response := request(h, "POST", "/producer/station", values, map[string]string{"Origin": "http://example.com"})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("same-origin form rejected: %d %s", response.Code, response.Body.String())
	}
	for _, origin := range []string{"null", "https://example.com", "http://evil.example", "http://example.com:8080", "http://example.com?bad", "http://example.com#bad"} {
		response := request(h, "POST", "/producer/station", values, map[string]string{"Origin": origin})
		if response.Code != http.StatusForbidden {
			t.Errorf("unexpected origin %q accepted: %d", origin, response.Code)
		}
	}
	saved, err := stations.List(context.Background())
	if err != nil || len(saved) != 1 {
		t.Fatalf("unexpected saved stations: %+v, %v", saved, err)
	}
}

func TestHTTPSProxyOriginPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, publicURL, origin, token string
		want                           int
	}{
		{"configured HTTPS", "https://radio.example", "https://radio.example", "valid", 204},
		{"configured trailing slash", "https://radio.example/", "https://radio.example", "valid", 204},
		{"direct HTTP remains usable", "https://radio.example", "http://example.com", "valid", 204},
		{"no configured proxy", "", "https://radio.example", "valid", 403},
		{"wrong scheme", "https://radio.example", "http://radio.example", "valid", 403},
		{"wrong port", "https://radio.example", "https://radio.example:8443", "valid", 403},
		{"configured port", "https://radio.example:8443", "https://radio.example:8443", "valid", 204},
		{"host suffix", "https://radio.example", "https://radio.example.evil.example", "valid", 403},
		{"subdomain", "https://radio.example", "https://other.radio.example", "valid", 403},
		{"null", "https://radio.example", "null", "valid", 403},
		{"userinfo", "https://radio.example", "https://user@radio.example", "valid", 403},
		{"path", "https://radio.example", "https://radio.example/", "valid", 403},
		{"query", "https://radio.example", "https://radio.example?", "valid", 403},
		{"fragment", "https://radio.example", "https://radio.example#bad", "valid", 403},
		{"missing token", "https://radio.example", "https://radio.example", "", 403},
		{"wrong token", "https://radio.example", "https://radio.example", "wrong", 403},
		{"forwarded headers cannot whitelist", "https://radio.example", "https://evil.example", "valid", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{d: Dependencies{PublicURL: tc.publicURL}, csrf: "valid"}
			h := s.protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			response := request(h, "POST", "/producer/station", url.Values{"csrf": {tc.token}}, map[string]string{
				"Origin": tc.origin, "X-Forwarded-Proto": "https", "X-Forwarded-Host": "evil.example", "Forwarded": "proto=https;host=evil.example",
			})
			if response.Code != tc.want {
				t.Fatalf("got %d, want %d: %s", response.Code, tc.want, response.Body.String())
			}
		})
	}
}

func TestCreateStationThroughHTTPSProxy(t *testing.T) {
	_, st, src, cat := setupWeb(t, "")
	h, err := New(Dependencies{Stations: st, Sources: src, Catalogue: cat, PublicURL: "https://radio.example", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRequest("GET", "http://upstream/producer/stations/new", nil)
	page.SetBasicAuth("admin", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, page)
	match := regexp.MustCompile(`name="csrf" value="([a-f0-9]+)"`).FindStringSubmatch(w.Body.String())
	if len(match) != 2 {
		t.Fatal("no form token", w.Code, w.Body.String())
	}
	values := url.Values{"csrf": {match[1]}, "name": {"Proxy station"}, "timezone": {"UTC"}}
	post := func(auth bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://upstream/producer/station", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		r.Header.Set("HX-Request", "true")
		if auth {
			r.SetBasicAuth("admin", "secret")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := post(false, "https://radio.example"); w.Code != http.StatusUnauthorized {
		t.Fatal("proxy bypassed authentication", w.Code)
	}
	if w := post(true, "https://evil.example"); w.Code != http.StatusForbidden {
		t.Fatal("foreign origin accepted", w.Code)
	}
	if w := post(true, "https://radio.example"); w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("HX-Location"), "/producer/stations/") {
		t.Fatal("proxied creation failed", w.Code, w.Body.String())
	}
	stations, err := st.List(context.Background())
	if err != nil || len(stations) != 1 || stations[0].Name != "Proxy station" {
		t.Fatal("station was not persisted", stations, err)
	}
}

func TestOptionalAuthentication(t *testing.T) {
	for _, password := range []string{"", "secret"} {
		t.Run("password="+password, func(t *testing.T) {
			h, _, _, _ := setupWeb(t, password)
			for _, credentials := range []struct{ user, password string }{{}, {"admin", "wrong"}, {"other", "secret"}, {"admin", "secret"}} {
				r := httptest.NewRequest("GET", "/producer", nil)
				if credentials.user != "" {
					r.SetBasicAuth(credentials.user, credentials.password)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				want := http.StatusOK
				if password != "" && (credentials.user != "admin" || credentials.password != password) {
					want = http.StatusUnauthorized
				}
				if w.Code != want {
					t.Fatalf("user %q: got %d, want %d", credentials.user, w.Code, want)
				}
				if want == http.StatusOK && w.Header().Get("WWW-Authenticate") != "" {
					t.Fatal("unexpected login challenge")
				}
			}
		})
	}
}
