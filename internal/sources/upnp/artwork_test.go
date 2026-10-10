package upnp

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestArtworkUsesCheckedSourceAndBoundedImageResponse(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aC1sAAAAASUVORK5CYII=")
	p, server := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/desc":
			io.WriteString(w, fixture(t, "description.xml"))
		case "/art":
			w.Write(png)
		case "/html":
			io.WriteString(w, "<html>not artwork</html>")
		case "/large":
			io.WriteString(w, strings.Repeat("a", (4<<20)+1))
		case "/redirect":
			http.Redirect(w, r, "http://other.example/art", 302)
		default:
			http.NotFound(w, r)
		}
	}))
	data, mime, err := p.Artwork(context.Background(), server.URL+"/art")
	if err != nil || mime != "image/png" || len(data) != len(png) {
		t.Fatal(mime, len(data), err)
	}
	for _, raw := range []string{"http://other.example/art", server.URL + "/html", server.URL + "/large", server.URL + "/redirect", "file:///etc/passwd"} {
		if _, _, err = p.Artwork(context.Background(), raw); err == nil {
			t.Fatal("invalid artwork accepted", raw)
		}
	}
}
