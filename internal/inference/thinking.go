package inference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"stationos.local/stationos/internal/ai"
)

type thinkingEntry struct {
	allowed bool
	expires time.Time
}

// Read-only metadata determines whether false is supported. Never guess a
// model-specific thinking level or expose its private reasoning in DJ output.
func (c *Client) canDisableThinking(parent context.Context, conn ai.Connection, key, model string) bool {
	digest := sha256.Sum256([]byte(conn.ID + "\n" + conn.Address + "\n" + model + "\n" + key))
	id := hex.EncodeToString(digest[:])
	c.capsMu.Lock()
	cached, ok := c.caps[id]
	c.capsMu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.allowed
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	raw, err := c.request(ctx, conn, key, "POST", "/api/show", map[string]string{"model": model})
	if err != nil {
		return false
	}
	var v struct {
		Thinking struct{ Values []json.RawMessage }
	}
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	allowed := false
	for _, value := range v.Thinking.Values {
		if string(value) == "false" {
			allowed = true
		}
	}
	c.capsMu.Lock()
	defer c.capsMu.Unlock()
	if c.caps == nil || len(c.caps) >= 64 {
		c.caps = map[string]thinkingEntry{}
	}
	c.caps[id] = thinkingEntry{allowed, time.Now().Add(5 * time.Minute)}
	return allowed
}
