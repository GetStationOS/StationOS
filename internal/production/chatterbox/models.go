package chatterbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"strings"
)

// Models reads metadata only. IDs are server-owned, so future Turbo models need
// no StationOS release. Legacy servers without /models retain Nano support.
func (p Provider) Models(ctx context.Context, conn ai.Connection) ([]djs.SpeechModel, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(conn.Address, "/")+"/models", nil)
	if err != nil {
		return nil, errors.New("Invalid Chatterbox model catalogue address")
	}
	client := *p.HTTP
	client.Timeout = conn.RequestTimeout()
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Chatterbox model catalogue unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		resp.Body.Close()
		legacy, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(conn.Address, "/")+"/health", nil)
		if err != nil {
			return nil, err
		}
		health, err := client.Do(legacy)
		if err != nil {
			return nil, errors.New("Chatterbox health unavailable")
		}
		defer health.Body.Close()
		raw, err := production.ReadBounded(health.Body, 256<<10)
		if err != nil {
			return nil, err
		}
		var v struct{ Status, Model string }
		if health.StatusCode != 200 || json.Unmarshal(raw, &v) != nil || v.Status != "ok" || v.Model != Model {
			return nil, errors.New("Chatterbox health did not report a ready Nano model")
		}
		return []djs.SpeechModel{{ID: "nano", Name: "Chatterbox Nano"}}, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Chatterbox model catalogue returned HTTP %d", resp.StatusCode)
	}
	raw, err := production.ReadBounded(resp.Body, 256<<10)
	if err != nil {
		return nil, errors.New("Chatterbox model catalogue incomplete or exceeds limit")
	}
	var out struct {
		Default string `json:"default_model"`
		Models  []djs.SpeechModel
	}
	if json.Unmarshal(raw, &out) != nil || len(out.Models) == 0 || len(out.Models) > 200 {
		return nil, errors.New("Invalid Chatterbox model catalogue")
	}
	seen := map[string]bool{}
	for _, m := range out.Models {
		if !ai.ValidModel(m.ID) || strings.TrimSpace(m.Name) == "" || len(m.Name) > 200 || seen[m.ID] {
			return nil, errors.New("Invalid Chatterbox model catalogue")
		}
		seen[m.ID] = true
	}
	if !seen[out.Default] {
		return nil, errors.New("Chatterbox default model is missing from its catalogue")
	}
	return out.Models, nil
}
