// Package chatterbox connects to an external, resident Nano TTS service.
package chatterbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/production"
)

const Model = "chatterbox-nano"
const MaxAudioBytes = 16 << 20

func NewClient() *http.Client {
	return &http.Client{Timeout: 180 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Chatterbox redirects are not allowed") }}
}

type Provider struct {
	HTTP   *http.Client
	Logger *slog.Logger
}

func (p Provider) Synthesize(ctx context.Context, conn ai.Connection, text, voice string) ([]byte, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("Chatterbox speech text is empty")
	}
	body := map[string]string{"text": text}
	if voice != "" {
		body["voice"] = voice
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(conn.Address, "/")+"/tts", bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("Invalid Chatterbox address")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/wav")
	client := *p.HTTP
	client.Timeout = conn.RequestTimeout()
	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("Chatterbox TTS cancelled: %w", ctx.Err())
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return nil, fmt.Errorf("Chatterbox TTS timed out: %w", context.DeadlineExceeded)
		}
		return nil, errors.New("Chatterbox TTS connection failed; check the service and base URL")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Chatterbox TTS returned HTTP %d", resp.StatusCode)
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "audio/wav" {
		return nil, errors.New("Chatterbox TTS did not return audio/wav")
	}
	audio, err := production.ReadBounded(resp.Body, MaxAudioBytes)
	if err != nil {
		return nil, fmt.Errorf("Chatterbox TTS audio incomplete or exceeds limit: %w", err)
	}
	if !ValidWAV(audio) {
		return nil, errors.New("Chatterbox TTS returned empty or malformed WAV audio")
	}
	logger := p.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.InfoContext(ctx, "Chatterbox speech generated", "elapsed", time.Since(started), "audio_duration", resp.Header.Get("X-Audio-Duration"), "generation_time", resp.Header.Get("X-Generation-Time"), "rtf", resp.Header.Get("X-RTF"))
	return audio, nil
}

// ValidWAV checks bounded RIFF chunks before audio reaches FFmpeg. FFmpeg remains
// responsible for decoding the waveform; a signature alone is not valid audio.
func ValidWAV(b []byte) bool {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return false
	}
	size := uint64(binary.LittleEndian.Uint32(b[4:8])) + 8
	if size != uint64(len(b)) {
		return false
	}
	format, data := false, false
	for pos := uint64(12); pos < size; {
		if pos+8 > size {
			return false
		}
		n := uint64(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		start := pos + 8
		if start+n > size {
			return false
		}
		switch string(b[pos : pos+4]) {
		case "fmt ":
			if n < 16 {
				return false
			}
			f := b[start : start+n]
			if binary.LittleEndian.Uint16(f[:2]) == 0 || binary.LittleEndian.Uint16(f[2:4]) == 0 || binary.LittleEndian.Uint32(f[4:8]) == 0 || binary.LittleEndian.Uint16(f[12:14]) == 0 {
				return false
			}
			format = true
		case "data":
			data = n > 0
		}
		pos = start + n + (n % 2)
		if pos > size {
			return false
		}
	}
	return format && data
}
