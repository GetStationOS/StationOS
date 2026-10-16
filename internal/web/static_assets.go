package web

import (
	"crypto/sha256"
	"encoding/hex"
)

// Version embedded assets by content, so a deployment cannot pair new markup
// with cached styles or scripts from an older build.
func assetURL(name string) (string, error) {
	data, err := assets.ReadFile("static/" + name)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "/static/" + name + "?v=" + hex.EncodeToString(sum[:8]), nil
}
