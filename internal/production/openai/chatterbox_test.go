package openai

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/inference"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

type speechStore struct {
	ai.Store
	conn ai.Connection
}

func (s speechStore) Routes(context.Context) (ai.Routes, error) {
	return ai.Routes{Speech: ai.Selection{Connection: s.conn.ID}}, nil
}
func (s speechStore) Connection(context.Context, string) (ai.Connection, error) { return s.conn, nil }
func (s speechStore) Credential(context.Context, string) (string, error) {
	if s.conn.Type == "openai" {
		return "test-secret", nil
	}
	return "", nil
}

type waveNormalizer struct {
	t     *testing.T
	calls int
}

func (n *waveNormalizer) MP3(_ context.Context, b []byte) ([]byte, error) {
	n.calls++
	if string(b[:4]) != "RIFF" {
		n.t.Fatal("WAV lost before normalization")
	}
	return []byte("normalized mp3"), nil
}
func testWave() []byte {
	b := make([]byte, 46)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 38)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 24000)
	binary.LittleEndian.PutUint32(b[28:], 48000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 2)
	return b
}
func TestChatterboxDJLinkWithoutOpenAIKey(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Write([]byte(`{"default_model":"nano","models":[{"id":"nano","name":"Chatterbox Nano"}]}`))
			return
		}

		calls++
		var body map[string]string
		if r.URL.Path != "/tts" || json.NewDecoder(r.Body).Decode(&body) != nil || body["text"] != "A new StationOS link." || len(body) != 2 || body["model"] != "nano" {
			t.Error(r.URL.Path, body)
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(testWave())
	}))
	defer server.Close()
	audio := &waveNormalizer{t: t}
	p := New(noKey{}, nil, audio)
	p.Text = &localText{reply: "A new StationOS link."}
	p.Speech = inference.New(speechStore{conn: ai.Connection{ID: "nano", Type: "chatterbox", Address: server.URL}}, nil)
	link := production.LinkRequest{Show: profile()}
	link.Show.DJ.HasKey = false
	out, err := p.Link(context.Background(), link)
	if err != nil || !bytes.Equal(out, []byte("normalized mp3")) || calls != 1 || audio.calls != 1 {
		t.Fatal(out, err, calls, audio.calls)
	}
}

func TestRoutedOpenAISpeechRetainsVoiceOptions(t *testing.T) {
	p := New(testKeys{}, nil, testAudio{})
	p.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/audio/speech" || r.Header.Get("Authorization") != "Bearer test-secret" || body["voice"] != "cedar" || body["instructions"] != "Warm" || body["speed"] != 1.25 || body["model"] != "gpt-4o-mini-tts" || body["response_format"] != "mp3" {
			t.Error(r.URL, body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader([]byte("mp3")))}, nil
	})}
	router := inference.New(speechStore{conn: ai.Connection{ID: "openai", Type: "openai"}}, nil)
	router.OpenAISpeech = p
	if identity, err := router.Identity(context.Background(), "speech"); err != nil || identity != "" {
		t.Fatal("existing OpenAI cache identity changed", identity, err)
	}
	p.Speech = router
	if p.speechBudget(context.Background()) != production.Timeout {
		t.Fatal("existing OpenAI production timeout changed")
	}
	out, err := router.Synthesize(context.Background(), production.SpeechRequest{Text: "Hello", Model: "gpt-4o-mini-tts", Voice: "cedar", Direction: "Warm", Speed: 1.25})
	if err != nil || string(out) != "mp3" {
		t.Fatal(out, err)
	}
}

// Opt-in: uses the real provider through the same Link -> speech router ->
// FFmpeg path as broadcasting. Ordinary automated tests never contact Nano.
func TestChatterboxLiveDJLink(t *testing.T) {
	address := os.Getenv("STATIONOS_CHATTERBOX_TEST_URL")
	if address == "" {
		t.Skip("set STATIONOS_CHATTERBOX_TEST_URL for the real-service test")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	p := New(noKey{}, nil, production.FFmpeg{Path: ffmpeg})
	p.Text = &localText{reply: "You're listening to StationOS. Here's another great recording, brought to you by Chatterbox Nano."}
	speechRouter := inference.New(speechStore{conn: ai.Connection{ID: "nano", Type: "chatterbox", Address: address}}, nil)
	p.Speech = speechRouter
	catalog, err := speechRouter.Voices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	selectedVoice := os.Getenv("STATIONOS_CHATTERBOX_TEST_VOICE")
	if selectedVoice == "" {
		selectedVoice = catalog.DefaultVoice
	}
	t.Log("Selected Chatterbox voice:", selectedVoice)
	link := production.LinkRequest{Show: profile()}
	link.Show.DJ.HasKey = false
	link.Show.DJ.SpeechVoices = map[string]string{"nano": selectedVoice}
	link.Show.DJ.SpeechConnection = "nano"
	link.Show.DJ.SpeechModel = "chatterbox-nano"
	link.Show.DJ.SpeechSettings = map[string]djs.SpeechPreference{"nano": {Model: "chatterbox-nano", Voice: selectedVoice, Speed: 1}}
	out, err := p.Link(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	decode := exec.Command(ffmpeg, "-v", "error", "-f", "mp3", "-i", "pipe:0", "-f", "s16le", "pipe:1")
	decode.Stdin = bytes.NewReader(out)
	if pcm, err := decode.Output(); err != nil || len(pcm) < 44100*2*2 {
		t.Fatal("generated link cannot play", len(pcm), err)
	}
	path := os.Getenv("STATIONOS_CHATTERBOX_TEST_OUTPUT")
	if path == "" {
		path = filepath.Join(t.TempDir(), "stationos-dj-link.mp3")
	}
	if err = os.WriteFile(path, out, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("Playable StationOS DJ link:", path)
}

func TestSelectedDJVoiceReachesLinksAndShowPresentation(t *testing.T) {
	for _, kind := range []string{"link", "presentation"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					w.Write([]byte(`{"default_model":"nano","models":[{"id":"nano","name":"Chatterbox Nano"}]}`))
					return
				}

				calls++
				var body map[string]string
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["voice"] != "warm" {
					t.Error("DJ voice not forwarded", body)
				}
				w.Header().Set("Content-Type", "audio/wav")
				w.Write(testWave())
			}))
			defer server.Close()
			p := New(noKey{}, nil, &waveNormalizer{t: t})
			p.Text = &localText{reply: "A short link."}
			p.Speech = inference.New(speechStore{conn: ai.Connection{ID: "nano", Type: "chatterbox", Address: server.URL}}, nil)
			show := profile()
			show.DJ.HasKey = false
			show.DJ.Voice = "onyx"
			show.DJ.SpeechVoices = map[string]string{"nano": "warm", "other-server": "wrong"}
			if kind == "link" {
				if _, err := p.Link(context.Background(), production.LinkRequest{Show: show}); err != nil {
					t.Fatal(err)
				}
			} else {
				p.Text = &localText{reply: `{"intro":"Welcome","outro":"Goodbye"}`}
				if _, err := p.Prepare(context.Background(), show, schedule.Occurrence{}); err != nil {
					t.Fatal(err)
				}
			}
			want := 1
			if kind == "presentation" {
				want = 2
			}
			if calls != want {
				t.Fatal(calls, want)
			}
		})
	}
}
