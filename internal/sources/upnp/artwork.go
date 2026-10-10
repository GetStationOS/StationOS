package upnp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

func (p *Provider) Artwork(parent context.Context, raw string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	if err := p.ensure(ctx); err != nil {
		return nil, "", err
	}
	if err := p.scope.check(raw); err != nil {
		return nil, "", err
	}
	defer p.client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	response, err := p.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", errors.New("artwork unavailable")
	}
	const limit = 4 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 || len(data) > limit {
		return nil, "", errors.New("artwork exceeds limit")
	}
	mime := http.DetectContentType(data)
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return data, mime, nil
	}
	return nil, "", errors.New("unsupported artwork format")
}
