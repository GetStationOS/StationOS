package chatterbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
)

// Voices returns server-owned IDs without loading voice assets or generating audio.
func (p Provider) Voices(ctx context.Context, conn ai.Connection) (djs.VoiceCatalog, error) {
	var out djs.VoiceCatalog
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(conn.Address, "/")+"/voices", nil)
	if err != nil {
		return out, errors.New("Invalid Chatterbox voice catalogue address")
	}
	req.Header.Set("Accept", "application/json")
	client := *p.HTTP
	client.Timeout = conn.RequestTimeout()
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, errors.New("Chatterbox voice catalogue is unavailable; check the service and base URL")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("Chatterbox voice catalogue returned HTTP %d", resp.StatusCode)
	}
	raw, err := production.ReadBounded(resp.Body, 256<<10)
	if err != nil {
		return out, fmt.Errorf("Chatterbox voice catalogue incomplete or exceeds limit: %w", err)
	}
	if json.Unmarshal(raw, &out) != nil || len(out.Voices) == 0 || len(out.Voices) > 200 || !djs.ValidVoiceID(out.DefaultVoice) {
		return djs.VoiceCatalog{}, errors.New("Chatterbox returned an invalid voice catalogue")
	}
	seen := map[string]bool{}
	for _, voice := range out.Voices {
		if !djs.ValidVoiceID(voice.ID) || strings.TrimSpace(voice.Name) == "" || len(voice.Name) > 200 || strings.ContainsFunc(voice.Name, func(r rune) bool { return r < 32 || r == 127 }) || seen[voice.ID] {
			return djs.VoiceCatalog{}, errors.New("Chatterbox returned invalid or duplicate voices")
		}
		seen[voice.ID] = true
	}
	if !seen[out.DefaultVoice] {
		return djs.VoiceCatalog{}, errors.New("Chatterbox default voice is missing from its catalogue")
	}
	return out, nil
}
