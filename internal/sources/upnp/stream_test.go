package upnp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCheckedAudioOpeningAndFallback(t *testing.T) {
	var flac, mp3 atomic.Int32
	p, _ := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/desc":
			io.WriteString(w, fixture(t, "description.xml"))
		case "/control":
			io.WriteString(w, soap(fixture(t, "music.xml"), 1, 1))
		case "/music.flac":
			flac.Add(1)
			http.Error(w, "unavailable", 404)
		case "/music.mp3":
			mp3.Add(1)
			io.WriteString(w, "checked bytes")
		default:
			t.Errorf("unexpected URL %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	audio, format, err := p.OpenStream(context.Background(), "track&1")
	if err != nil {
		t.Fatal(err)
	}
	defer audio.Close()
	body, err := io.ReadAll(audio)
	if err != nil || string(body) != "checked bytes" || format != "mp3" || flac.Load() != 1 || mp3.Load() != 1 {
		t.Fatal(string(body), format, err)
	}
}
func TestAudioRejectsOtherServerAndManifest(t *testing.T) {
	for _, resource := range []string{`<res protocolInfo="http-get:*:audio/flac:*">http://other.example/music.flac</res>`, `<res protocolInfo="http-get:*:application/vnd.apple.mpegurl:*">/playlist.m3u8</res>`} {
		music := fixture(t, "music.xml")
		start := strings.Index(music, "<res ")
		end := strings.Index(music, "</item>")
		music = music[:start] + resource + music[end:]
		p, _ := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/desc" {
				io.WriteString(w, fixture(t, "description.xml"))
				return
			}
			if r.URL.Path != "/control" {
				t.Errorf("unsafe fetch %s", r.URL.Path)
			}
			io.WriteString(w, soap(music, 1, 1))
		}))
		audio, _, err := p.OpenStream(context.Background(), "track&1")
		if err == nil {
			audio.Close()
			t.Fatal("unsafe audio accepted")
		}
	}
}
