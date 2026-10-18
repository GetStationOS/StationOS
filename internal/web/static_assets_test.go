package web

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestShellAssetsAreVersionedByTheirEmbeddedContent(t *testing.T) {
	h, _, _, _ := setupWeb(t, "")
	for _, path := range []string{"/", "/producer"} {
		response := request(h, "GET", path, nil, nil)
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		body := response.Body.String()
		for _, name := range []string{"app.css", "navigation.js", "app.js", "htmx.min.js"} {
			data, err := assets.ReadFile("static/" + name)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(data)
			url := "/static/" + name + "?v=" + hex.EncodeToString(hash[:8])
			if !strings.Contains(body, `"`+url+`"`) {
				t.Fatal(path, "does not load current", name)
			}
			asset := request(h, "GET", url, nil, nil)
			if asset.Code != 200 || asset.Body.String() != string(data) {
				t.Fatal("versioned asset not served", url, asset.Code)
			}
		}
	}
}
