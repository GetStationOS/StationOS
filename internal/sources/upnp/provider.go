// Package upnp adapts ContentDirectory servers such as MinimServer.
package upnp

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/sources"
)

const maxXML = 4 << 20

var directoryType = regexp.MustCompile(`^urn:schemas-upnp-org:service:ContentDirectory:[1-9][0-9]*$`)

type device struct {
	FriendlyName string    `xml:"friendlyName"`
	Manufacturer string    `xml:"manufacturer"`
	Model        string    `xml:"modelName"`
	Services     []service `xml:"serviceList>service"`
	Devices      []device  `xml:"deviceList>device"`
}
type service struct {
	Type    string `xml:"serviceType"`
	Control string `xml:"controlURL"`
}

func directory(d device) (service, bool) {
	for _, s := range d.Services {
		if directoryType.MatchString(s.Type) && s.Control != "" {
			return s, true
		}
	}
	for _, child := range d.Devices {
		if s, ok := directory(child); ok {
			return s, true
		}
	}
	return service{}, false
}

type Provider struct {
	sourceID      string
	description   *url.URL
	mu            sync.Mutex
	scope         *scope
	client        *http.Client
	control       string
	service       string
	allowLoopback bool
	minimServer   bool
}

var _ sources.AudioSource = (*Provider)(nil)

func New(c sources.Config) (*Provider, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	u, err := validURL(c.Endpoint)
	if err != nil {
		return nil, err
	}
	return &Provider{sourceID: c.ID, description: u}, nil
}
func Factory(_ context.Context, c sources.Config) (sources.AudioSource, error) { return New(c) }
func (p *Provider) Profile() sources.Profile {
	return sources.Profile{Capabilities: sources.Capabilities{Browse: true, Metadata: true, Artwork: true, Streaming: true}, Restrictions: sources.Restrictions{Caching: sources.OperatorResponsible, Downloading: sources.OperatorResponsible, Crossfading: sources.OperatorResponsible, Ducking: sources.OperatorResponsible, Analysis: sources.OperatorResponsible, PublicBroadcast: sources.OperatorResponsible}}
}
func readXML(res *http.Response) ([]byte, error) {
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("UPnP HTTP status %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxXML+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxXML {
		return nil, errors.New("UPnP XML exceeds limit")
	}
	return b, nil
}
func decode(b []byte, v any) error {
	d := xml.NewDecoder(bytes.NewReader(b))
	if err := d.Decode(v); err != nil {
		return err
	}
	for {
		t, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if c, ok := t.(xml.CharData); ok && strings.TrimSpace(string(c)) == "" {
			continue
		}
		if _, ok := t.(xml.Comment); ok {
			continue
		}
		return errors.New("extra XML data")
	}
}

// Failed initialization remains retryable; offline sources cannot prevent startup.
func (p *Provider) ensure(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.control != "" {
		return nil
	}
	s, err := newScope(ctx, p.description, p.allowLoopback)
	if err != nil {
		return err
	}
	client := s.client()
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "GET", p.description.String(), nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	b, err := readXML(res)
	if err != nil {
		return err
	}
	var desc struct {
		XMLName xml.Name `xml:"root"`
		Base    string   `xml:"URLBase"`
		Device  device   `xml:"device"`
	}
	if err = decode(b, &desc); err != nil {
		return err
	}
	svc, ok := directory(desc.Device)
	if !ok {
		return errors.New("ContentDirectory service not found")
	}
	base := res.Request.URL
	if desc.Base != "" {
		base, err = url.Parse(strings.TrimSpace(desc.Base))
		if err != nil {
			return err
		}
		if err = s.check(base.String()); err != nil {
			return err
		}
	}
	rel, err := url.Parse(svc.Control)
	if err != nil {
		return err
	}
	control := base.ResolveReference(rel).String()
	if err = s.check(control); err != nil {
		return err
	}
	p.scope = s
	p.client = client
	p.control = control
	p.service = svc.Type
	p.minimServer = isMinimServer(desc.Device)
	return nil
}
func isMinimServer(d device) bool {
	if strings.EqualFold(strings.TrimSpace(d.Manufacturer), "minimserver.com") && strings.EqualFold(strings.TrimSpace(d.Model), "MinimServer") {
		return true
	}
	for _, child := range d.Devices {
		if isMinimServer(child) {
			return true
		}
	}
	return false
}

// MinimServer's facet menus are a virtual graph. Its unfiltered all-items view
// enumerates the library without expanding every artist/genre/date combination.
func (p *Provider) CatalogueRoot(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := p.ensure(ctx); err != nil {
		return "", err
	}
	if !p.minimServer {
		return "0", nil
	}
	page, err := p.Browse(ctx, "0", 0, 100)
	if err != nil {
		return "", err
	}
	for _, entry := range page.Entries {
		if entry.ContainerID == "0$items" {
			return entry.ContainerID, nil
		}
	}
	return "", errors.New("MinimServer does not expose its unfiltered all-items view")
}

func (p *Provider) Browse(ctx context.Context, object string, offset, count int) (sources.Page, error) {
	return p.browse(ctx, object, "BrowseDirectChildren", offset, count)
}
func (p *Provider) browse(ctx context.Context, object, flag string, offset, count int) (sources.Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if object == "" || len(object) > 2048 || strings.ContainsRune(object, 0) || offset < 0 || offset > 1000000 || count < 1 || count > 100 {
		return sources.Page{}, errors.New("invalid source page")
	}
	if err := p.ensure(ctx); err != nil {
		return sources.Page{}, err
	}
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(object)); err != nil {
		return sources.Page{}, err
	}
	body := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:Browse xmlns:u="` + p.service + `"><ObjectID>` + escaped.String() + `</ObjectID><BrowseFlag>` + flag + `</BrowseFlag><Filter>*</Filter><StartingIndex>` + strconv.Itoa(offset) + `</StartingIndex><RequestedCount>` + strconv.Itoa(count) + `</RequestedCount><SortCriteria></SortCriteria></u:Browse></s:Body></s:Envelope>`
	req, err := http.NewRequestWithContext(ctx, "POST", p.control, strings.NewReader(body))
	if err != nil {
		return sources.Page{}, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `"`+p.service+`#Browse"`)
	res, err := p.client.Do(req)
	if err != nil {
		return sources.Page{}, err
	}
	b, err := readXML(res)
	if err != nil {
		return sources.Page{}, err
	}
	var envelope struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Response *struct {
				Result   *string `xml:"Result"`
				Returned *int    `xml:"NumberReturned"`
				Total    *int    `xml:"TotalMatches"`
				Update   string  `xml:"UpdateID"`
			} `xml:"BrowseResponse"`
		} `xml:"Body"`
	}
	if err = decode(b, &envelope); err != nil {
		return sources.Page{}, err
	}
	v := envelope.Body.Response
	if v == nil || v.Result == nil || v.Returned == nil || v.Total == nil {
		return sources.Page{}, errors.New("invalid UPnP Browse response")
	}
	entries, err := p.parseDIDL(*v.Result)
	if err != nil {
		return sources.Page{}, err
	}
	if *v.Returned != len(entries) || *v.Returned > count || *v.Total < 0 || *v.Total > 100000 || *v.Returned > *v.Total {
		return sources.Page{}, errors.New("inconsistent UPnP page counts")
	}
	return sources.Page{Entries: entries, Total: *v.Total, UpdateID: v.Update}, nil
}
func (p *Provider) Get(ctx context.Context, id string) (content.AudioItem, error) {
	page, err := p.browse(ctx, id, "BrowseMetadata", 0, 1)
	if err != nil {
		return content.AudioItem{}, err
	}
	if len(page.Entries) != 1 || page.Entries[0].Item == nil || page.Entries[0].Item.SourceItemID != id {
		return content.AudioItem{}, sources.ErrNotFound
	}
	return *page.Entries[0].Item, nil
}
func (p *Provider) Resolve(ctx context.Context, id string) ([]content.Resource, error) {
	a, err := p.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(a.Resources) == 0 {
		return nil, errors.New("no playable resource")
	}
	return a.Resources, nil
}

type didlEntry struct {
	ID        string       `xml:"id,attr"`
	Title     string       `xml:"title"`
	Class     string       `xml:"class"`
	Artists   []didlCredit `xml:"artist"`
	Authors   []didlCredit `xml:"author"`
	Creators  []string     `xml:"creator"`
	Composers []string     `xml:"composer"`
	Album     string       `xml:"album"`
	Date      string       `xml:"date"`
	Genres    []string     `xml:"genre"`
	Art       string       `xml:"albumArtURI"`
	Resources []struct {
		URL      string `xml:",chardata"`
		Protocol string `xml:"protocolInfo,attr"`
		Duration string `xml:"duration,attr"`
	} `xml:"res"`
}

func (p *Provider) resourceURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	u = p.description.ResolveReference(u)
	if p.scope == nil {
		return "", errors.New("source is not initialized")
	}
	if err = p.scope.check(u.String()); err != nil {
		return "", err
	}
	return u.String(), nil
}
func duration(raw string) (*time.Duration, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return nil, errors.New("invalid source duration")
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 168 {
		return nil, errors.New("invalid source duration")
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return nil, errors.New("invalid source duration")
	}
	secs, err := strconv.ParseFloat(parts[2], 64)
	if err != nil || !(secs >= 0 && secs < 60) {
		return nil, errors.New("invalid source duration")
	}
	d := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(secs*float64(time.Second))
	return &d, nil
}
func (p *Provider) parseDIDL(raw string) ([]sources.Entry, error) {
	d := xml.NewDecoder(strings.NewReader(raw))
	var root xml.StartElement
	for {
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		if start, ok := t.(xml.StartElement); ok {
			root = start
			break
		}
	}
	if root.Name.Local != "DIDL-Lite" {
		return nil, errors.New("invalid DIDL root")
	}
	out := []sources.Entry{}
	for {
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		if end, ok := t.(xml.EndElement); ok && end.Name == root.Name {
			break
		}
		start, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "container" && start.Name.Local != "item" {
			if err = d.Skip(); err != nil {
				return nil, err
			}
			continue
		}
		var v didlEntry
		if err = d.DecodeElement(&v, &start); err != nil {
			return nil, err
		}
		if v.ID == "" || len(v.ID) > 2048 || strings.ContainsRune(v.ID, 0) || v.Title == "" {
			return nil, errors.New("invalid DIDL entry")
		}
		entry := sources.Entry{}
		if start.Name.Local == "container" {
			entry.ContainerID = v.ID
		} else if strings.HasPrefix(v.Class, "object.item.audioItem") {
			kind := content.RecordedProgramme
			if strings.HasPrefix(v.Class, "object.item.audioItem.musicTrack") {
				kind = content.Track
			}
			a := content.AudioItem{ID: content.ID(p.sourceID, v.ID), SourceID: p.sourceID, SourceItemID: v.ID, Kind: kind, Title: v.Title, Album: v.Album, Year: sourceYear(v.Date), Genre: strings.Join(v.Genres, ", "), Origin: content.Original, Resources: []content.Resource{}}
			v.applyCredits(&a)
			if v.Art != "" {
				a.Artwork, err = p.resourceURL(v.Art)
				if err != nil {
					return nil, err
				}
			}
			for _, res := range v.Resources {
				parts := strings.SplitN(res.Protocol, ":", 4)
				if len(parts) != 4 || parts[0] != "http-get" {
					continue
				}
				mime := strings.ToLower(strings.TrimSpace(strings.Split(parts[2], ";")[0]))
				if !strings.HasPrefix(mime, "audio/") {
					continue
				}
				u, err := p.resourceURL(res.URL)
				if err != nil {
					return nil, err
				}
				a.Resources = append(a.Resources, content.Resource{URL: u, MIME: mime})
				if a.Duration == nil {
					a.Duration, err = duration(res.Duration)
					if err != nil {
						return nil, err
					}
				}
			}
			if err = a.Validate(); err != nil {
				return nil, err
			}
			if len(a.Resources) > 0 {
				entry.Item = &a
			}
		}
		// Preserve unsupported objects as empty entries so pagination counts remain exact.
		out = append(out, entry)
	}
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if c, ok := t.(xml.CharData); ok && strings.TrimSpace(string(c)) == "" {
			continue
		}
		if _, ok := t.(xml.Comment); ok {
			continue
		}
		return nil, errors.New("extra DIDL data")
	}
	return out, nil
}
