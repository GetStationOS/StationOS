package upnp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscoveryRejectsUntrustedAdvertisements(t *testing.T) {
	peer := netip.MustParseAddr("192.168.1.10")
	good := "HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:MediaServer:1\r\nLOCATION: http://192.168.1.10:9791/device.xml\r\n\r\n"
	if ad, ok := parseAdvertisement([]byte(good), peer); !ok || ad.location != "http://192.168.1.10:9791/device.xml" {
		t.Fatal(ad, ok)
	}
	for _, raw := range []string{
		strings.Replace(good, "200 OK", "404 Not Found", 1),
		strings.Replace(good, "MediaServer:1", "MediaRenderer:1", 1),
		strings.Replace(good, "http://192.168.1.10:9791", "http://127.0.0.1:9791", 1),
		strings.Replace(good, "http://192.168.1.10:9791", "http://192.168.1.20:9791", 1),
		strings.Replace(good, "http://", "file://", 1),
		strings.Replace(good, "http://", "http://user:password@", 1),
		strings.Replace(good, "\r\n\r\n", "\r\nLOCATION: http://192.168.1.10/other\r\n\r\n", 1),
		strings.Repeat("x", 8193),
	} {
		if _, ok := parseAdvertisement([]byte(raw), peer); ok {
			t.Fatalf("accepted %q", raw)
		}
	}
	for _, ip := range []string{"127.0.0.1", "169.254.1.2", "239.255.255.250", "0.0.0.0"} {
		if _, ok := parseAdvertisement([]byte(good), netip.MustParseAddr(ip)); ok {
			t.Fatal("accepted peer", ip)
		}
	}
}

func TestDiscoveryVerifiesDescriptionsDeduplicatesAndSharesResults(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/device.xml":
			fmt.Fprint(w, `<root><device><friendlyName>Music server</friendlyName><deviceList><device><friendlyName>Library &amp; jazz</friendlyName><modelName>MinimServer</modelName><serviceList><service><serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType><controlURL>/control</controlURL></service></serviceList></device></deviceList></device></root>`)
		case "/renderer.xml":
			fmt.Fprint(w, `<root><device><friendlyName>TV</friendlyName></device></root>`)
		default:
			http.Error(w, "bad description", 500)
		}
	}))
	defer provider.Close()
	var probes atomic.Int32
	discoverer := &Discovery{allowLoopback: true, probe: func(ctx context.Context) ([]advertisement, error) {
		probes.Add(1)
		return []advertisement{{provider.URL + "/device.xml", netip.MustParseAddr("127.0.0.1")}, {provider.URL + "/device.xml", netip.MustParseAddr("127.0.0.1")}, {provider.URL + "/renderer.xml", netip.MustParseAddr("127.0.0.1")}}, nil
	}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			servers, err := discoverer.Discover(context.Background())
			if err != nil || len(servers) != 1 || servers[0].Name != "Library & jazz" || servers[0].Model != "MinimServer" {
				t.Errorf("bad discovery: %+v %v", servers, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 2 || probes.Load() != 1 {
		t.Fatal("duplicate scan or fetch", calls.Load(), probes.Load())
	}
	found, _ := discoverer.Discover(context.Background())
	found[0].Name = "mutated"
	again, _ := discoverer.Discover(context.Background())
	if again[0].Name != "Library & jazz" {
		t.Fatal("cache exposed mutable data")
	}
}

func TestDiscoveryCannotFetchOtherHostsOrFollowTheirRedirects(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer other.Close()
	d := &Discovery{allowLoopback: true}
	_, err := d.describe(context.Background(), advertisement{other.URL, netip.MustParseAddr("192.168.1.10")})
	if err == nil || hits.Load() != 0 {
		t.Fatal("fetched non-advertising host", err, hits.Load())
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(other.URL, "127.0.0.1", "localhost", 1), 302)
	}))
	defer redirect.Close()
	_, err = d.describe(context.Background(), advertisement{redirect.URL, netip.MustParseAddr("127.0.0.1")})
	if err == nil || hits.Load() != 0 {
		t.Fatal("followed different host", err, hits.Load())
	}
}

func TestDiscoverySendsSSDPAndCancelsPromptly(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := probeInterface(ctx, net.IPv4(127, 0, 0, 1), server.LocalAddr().(*net.UDPAddr), time.Minute)
		done <- err
	}()
	server.SetReadDeadline(time.Now().Add(time.Second))
	for i := 0; i < 2; i++ {
		buf := make([]byte, 2048)
		n, _, err := server.ReadFromUDP(buf)
		if err != nil {
			t.Fatal(err)
		}
		raw := string(buf[:n])
		if !strings.Contains(raw, "M-SEARCH * HTTP/1.1\r\n") || !strings.Contains(raw, `MAN: "ssdp:discover"`) || !strings.Contains(raw, "MX: 2") {
			t.Fatal("invalid SSDP request", raw)
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery ignored cancellation")
	}
}
