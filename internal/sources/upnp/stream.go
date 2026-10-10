package upnp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
)

// Only demuxers for ordinary music files are admitted; manifests and playlists
// cannot give the encoder another network or filesystem input.
func demuxer(mime string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0])) {
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/flac", "audio/x-flac":
		return "flac"
	case "audio/aac", "audio/aacp", "audio/x-aac":
		return "aac"
	case "audio/wav", "audio/x-wav", "audio/vnd.wave":
		return "wav"
	case "audio/mp4", "audio/x-m4a":
		return "mov"
	case "audio/ogg", "application/ogg":
		return "ogg"
	}
	return ""
}
func (p *Provider) OpenStream(ctx context.Context, id string) (io.ReadCloser, string, error) {
	resources, err := p.Resolve(ctx, id)
	if err != nil {
		return nil, "", err
	}
	for _, resource := range resources {
		format := demuxer(resource.MIME)
		if format == "" || p.scope.check(resource.URL) != nil {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, resource.URL, nil)
		if err != nil {
			continue
		}
		res, err := p.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			continue
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			continue
		}
		return res.Body, format, nil
	}
	return nil, "", errors.New("no supported audio resource is available")
}
