package upnp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"stationos.local/stationos/internal/sources"
)

const ssdpTarget = "239.255.255.250:1900"
const maxDiscoveredServers = 24

type advertisement struct {
	location string
	peer     netip.Addr
}

// Discovery uses bounded IPv4 SSDP searches on active multicast interfaces.
// Concurrent page loads share a scan; successful results are cached briefly.
type Discovery struct {
	mu            sync.Mutex
	running       chan struct{}
	cached        []sources.DiscoveredSource
	expires       time.Time
	probe         func(context.Context) ([]advertisement, error)
	allowLoopback bool // Tests only, matching the existing provider test policy.
}

func (d *Discovery) Discover(ctx context.Context) ([]sources.DiscoveredSource, error) {
	for {
		d.mu.Lock()
		if time.Now().Before(d.expires) {
			out := append([]sources.DiscoveredSource{}, d.cached...)
			d.mu.Unlock()
			return out, nil
		}
		if done := d.running; done != nil {
			d.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		d.running = make(chan struct{})
		d.mu.Unlock()
		found, err := d.scan(ctx)
		d.mu.Lock()
		if err == nil {
			d.cached = append([]sources.DiscoveredSource{}, found...)
			d.expires = time.Now().Add(15 * time.Second)
		}
		close(d.running)
		d.running = nil
		d.mu.Unlock()
		return found, err
	}
}

func (d *Discovery) scan(parent context.Context) ([]sources.DiscoveredSource, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	probe := d.probe
	if probe == nil {
		probe = probeNetwork
	}
	ads, err := probe(ctx)
	if err != nil {
		return nil, err
	}
	found := make([]sources.DiscoveredSource, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	seen := map[string]bool{}
	for _, ad := range ads {
		if seen[ad.location] {
			continue
		}
		seen[ad.location] = true
		if len(seen) > maxDiscoveredServers {
			break
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return nil, ctx.Err()
		}
		wg.Add(1)
		go func(ad advertisement) {
			defer wg.Done()
			defer func() { <-slots }()
			server, err := d.describe(ctx, ad)
			if err == nil {
				mu.Lock()
				found = append(found, server)
				mu.Unlock()
			}
		}(ad)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Name == found[j].Name {
			return found[i].Endpoint < found[j].Endpoint
		}
		return found[i].Name < found[j].Name
	})
	return found, nil
}

func probeNetwork(ctx context.Context) ([]advertisement, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	target, _ := net.ResolveUDPAddr("udp4", ssdpTarget)
	var wg sync.WaitGroup
	var mu sync.Mutex
	out := []advertisement{}
	attempts, successes := 0, 0
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsGlobalUnicast() || prefix.Addr().IsLinkLocalUnicast() {
				continue
			}
			if attempts >= 16 {
				break
			}
			attempts++
			ip := net.IP(prefix.Addr().AsSlice())
			wg.Add(1)
			go func() {
				defer wg.Done()
				ads, err := probeInterface(ctx, ip, target, 3*time.Second)
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					successes++
					out = append(out, ads...)
				}
			}()
		}
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if attempts == 0 || successes == 0 {
		return nil, errors.New("local network discovery is unavailable")
	}
	return out, nil
}

func probeInterface(ctx context.Context, ip net.IP, target *net.UDPAddr, window time.Duration) ([]advertisement, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := setDiscoveryInterface(conn, ip); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline := time.Now().Add(window)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	for _, kind := range []string{"urn:schemas-upnp-org:device:MediaServer:1", "urn:schemas-upnp-org:service:ContentDirectory:1"} {
		request := fmt.Sprintf("M-SEARCH * HTTP/1.1\r\nHOST: %s\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: %s\r\n\r\n", ssdpTarget, kind)
		if _, err := conn.WriteToUDP([]byte(request), target); err != nil {
			return nil, err
		}
	}
	out := []advertisement{}
	seen := map[string]bool{}
	buf := make([]byte, 8193)
	for packets := 0; packets < 256; packets++ {
		n, peer, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				return out, nil
			}
			return nil, err
		}
		if n > 8192 {
			continue
		}
		ad, ok := parseAdvertisement(buf[:n], peer.Addr())
		if ok && !seen[ad.location] {
			seen[ad.location] = true
			out = append(out, ad)
		}
		if len(out) >= maxDiscoveredServers {
			break
		}
	}
	return out, nil
}

func parseAdvertisement(raw []byte, peer netip.Addr) (advertisement, bool) {
	if len(raw) > 8192 || !peer.IsGlobalUnicast() || peer.IsLoopback() || peer.IsLinkLocalUnicast() {
		return advertisement{}, false
	}
	res, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), nil)
	if err != nil {
		return advertisement{}, false
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || len(res.Header.Values("Location")) != 1 {
		return advertisement{}, false
	}
	st := res.Header.Get("ST")
	if st != "urn:schemas-upnp-org:device:MediaServer:1" && st != "urn:schemas-upnp-org:service:ContentDirectory:1" {
		return advertisement{}, false
	}
	location := strings.TrimSpace(res.Header.Get("Location"))
	u, err := validURL(location)
	if err != nil {
		return advertisement{}, false
	}
	if host, err := netip.ParseAddr(u.Hostname()); err == nil && host.Unmap() != peer.Unmap() {
		return advertisement{}, false
	}
	return advertisement{location: location, peer: peer.Unmap()}, true
}

func (d *Discovery) describe(parent context.Context, ad advertisement) (sources.DiscoveredSource, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	u, err := validURL(ad.location)
	if err != nil {
		return sources.DiscoveredSource{}, err
	}
	sc, err := newScope(ctx, u, d.allowLoopback)
	if err != nil {
		return sources.DiscoveredSource{}, err
	}
	// An advertisement cannot make us fetch from a different machine. DNS is
	// resolved once, checked against the UDP sender and pinned for HTTP/redirects.
	for _, ip := range sc.ips {
		if ip.Unmap() != ad.peer.Unmap() {
			return sources.DiscoveredSource{}, errors.New("description is outside advertising server")
		}
	}
	client := sc.client()
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "GET", ad.location, nil)
	if err != nil {
		return sources.DiscoveredSource{}, err
	}
	res, err := client.Do(req)
	if err != nil {
		return sources.DiscoveredSource{}, err
	}
	raw, err := readXML(res)
	if err != nil {
		return sources.DiscoveredSource{}, err
	}
	var desc struct {
		XMLName xml.Name `xml:"root"`
		Base    string   `xml:"URLBase"`
		Device  device   `xml:"device"`
	}
	if err := decode(raw, &desc); err != nil {
		return sources.DiscoveredSource{}, err
	}
	owner, ok := musicDevice(desc.Device)
	if !ok {
		return sources.DiscoveredSource{}, errors.New("not a music server")
	}
	base := res.Request.URL
	if desc.Base != "" {
		base, err = url.Parse(strings.TrimSpace(desc.Base))
		if err != nil {
			return sources.DiscoveredSource{}, err
		}
		if err = sc.check(base.String()); err != nil {
			return sources.DiscoveredSource{}, err
		}
	}
	service, _ := directory(owner)
	control, err := url.Parse(service.Control)
	if err != nil {
		return sources.DiscoveredSource{}, err
	}
	if err = sc.check(base.ResolveReference(control).String()); err != nil {
		return sources.DiscoveredSource{}, err
	}
	name := strings.TrimSpace(owner.FriendlyName)
	if name == "" {
		name = strings.TrimSpace(owner.Model)
	}
	if name == "" {
		name = u.Hostname()
	}
	if len(name) > 200 {
		name = strings.ToValidUTF8(name[:200], "")
	}
	model := strings.TrimSpace(owner.Model)
	if len(model) > 200 {
		model = strings.ToValidUTF8(model[:200], "")
	}
	return sources.DiscoveredSource{Name: name, Endpoint: ad.location, Model: model}, nil
}

func musicDevice(d device) (device, bool) {
	for _, service := range d.Services {
		if directoryType.MatchString(service.Type) && service.Control != "" {
			return d, true
		}
	}
	for _, child := range d.Devices {
		if found, ok := musicDevice(child); ok {
			return found, true
		}
	}
	return device{}, false
}
