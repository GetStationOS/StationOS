package web

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"stationos.local/stationos/internal/sources"
)

type sourceDiscoveryStub struct {
	found []sources.DiscoveredSource
	err   error
	calls int
}

func (d *sourceDiscoveryStub) Discover(context.Context) ([]sources.DiscoveredSource, error) {
	d.calls++
	return d.found, d.err
}

func TestSourceDiscoveryIsReadOnlyAndSupportsOrdinaryForms(t *testing.T) {
	discovery := &sourceDiscoveryStub{found: []sources.DiscoveredSource{{Name: "Library & jazz", Endpoint: "http://192.168.1.10:9791/device.xml", Model: "MinimServer"}}}
	h, _, repo, _ := setupWeb(t, "", discovery)
	page := request(h, "GET", "/producer/sources/new", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `hx-get="/producer/sources/discover"`) || discovery.calls != 0 {
		t.Fatal("creation page blocked on discovery")
	}
	result := request(h, "GET", "/producer/sources/discover", nil, map[string]string{"HX-Request": "true"})
	if result.Code != 200 || !strings.Contains(result.Body.String(), "Library &amp; jazz") || !strings.Contains(result.Body.String(), "Use server") || strings.Contains(result.Body.String(), "<!doctype") {
		t.Fatal(result.Code, result.Body.String())
	}
	all, _ := repo.List(context.Background())
	if len(all) != 0 {
		t.Fatal("discovery saved configuration")
	}
	fields := url.Values{"name": {"Library & jazz"}, "endpoint": {"http://192.168.1.10:9791/device.xml"}}
	prefilled := request(h, "GET", "/producer/sources/new?"+fields.Encode(), nil, nil)
	if prefilled.Code != 200 || !strings.Contains(prefilled.Body.String(), `value="Library &amp; jazz"`) || !strings.Contains(prefilled.Body.String(), `value="http://192.168.1.10:9791/device.xml"`) {
		t.Fatal("no-JS selection lost values")
	}
	fields.Set("csrf", csrf(t, h))
	saved := request(h, "POST", "/producer/sources", fields, nil)
	if saved.Code != 303 {
		t.Fatal(saved.Code, saved.Body.String())
	}
	result = request(h, "GET", "/producer/sources/discover", nil, map[string]string{"HX-Request": "true"})
	if !strings.Contains(result.Body.String(), "Already added") || strings.Contains(result.Body.String(), "Use server") {
		t.Fatal("saved server offered as new", result.Body.String())
	}
	fallback := request(h, "GET", "/producer/sources/new?discover=1", nil, nil)
	if fallback.Code != 200 || !strings.Contains(fallback.Body.String(), "Already added") {
		t.Fatal("ordinary search failed")
	}
}

func TestSourceDiscoveryEmptyFailureAuthenticationAndInputRetention(t *testing.T) {
	for _, failure := range []error{nil, errors.New("private network diagnostic")} {
		d := &sourceDiscoveryStub{err: failure}
		h, _, _, _ := setupWeb(t, "", d)
		result := request(h, "GET", "/producer/sources/discover", nil, map[string]string{"HX-Request": "true"})
		expected := "No music servers found"
		if failure != nil {
			expected = "Could not search the local network"
		}
		if result.Code != 200 || !strings.Contains(result.Body.String(), expected) || strings.Contains(result.Body.String(), "private network diagnostic") {
			t.Fatal(result.Code, result.Body.String())
		}
	}
	d := &sourceDiscoveryStub{}
	h, _, _, _ := setupWeb(t, "password", d)
	if w := request(h, "GET", "/producer/sources/discover", nil, nil); w.Code != 401 || d.calls != 0 {
		t.Fatal("discovery bypassed authentication")
	}
	h, _, _, _ = setupWeb(t, "", d)
	w := request(h, "POST", "/producer/sources", url.Values{"csrf": {csrf(t, h)}, "name": {"My music"}, "endpoint": {"invalid-address"}}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `value="My music"`) || !strings.Contains(w.Body.String(), `value="invalid-address"`) || !strings.Contains(w.Body.String(), `role="alert"`) {
		t.Fatal("invalid form lost input", w.Code, w.Body.String())
	}
}
