package production

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestPresenterSegmentsConcatenateAsDecodableContinuousMP3(t *testing.T) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, path, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=500:duration=1", "-f", "mp3", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	audio, err := (FFmpeg{Path: path}).MP3(ctx, raw)
	if err != nil || len(audio) < 10000 || bytes.HasPrefix(audio, []byte("ID3")) {
		t.Fatal(len(audio), err)
	}
	combined := append(append(append([]byte{}, audio...), audio...), audio...)
	decode := exec.CommandContext(ctx, path, "-v", "error", "-f", "mp3", "-i", "pipe:0", "-ar", "44100", "-ac", "2", "-f", "s16le", "pipe:1")
	decode.Stdin = bytes.NewReader(combined)
	pcm, err := decode.Output()
	if err != nil || len(pcm) < 44100*2*2*3 {
		t.Fatal("joined segments cannot decode", len(pcm), err)
	}
	if _, err = (FFmpeg{Path: path}).MP3(ctx, []byte("not MP3")); err == nil {
		t.Fatal("invalid speech accepted")
	}
}

func TestSpeechEdgeSilenceRemovedButInternalPausePreserved(t *testing.T) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Two short speech stand-ins separated by an editorial pause, with a second
	// of unwanted silence at either end.
	raw, err := exec.CommandContext(ctx, path, "-v", "error", "-f", "lavfi", "-i", "aevalsrc=if(between(t\\,1\\,1.5)+between(t\\,2\\,2.5)\\,0.2*sin(2*PI*500*t)\\,0):d=3.5:s=44100", "-f", "mp3", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	audio, err := (FFmpeg{Path: path}).MP3(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	decode := exec.CommandContext(ctx, path, "-v", "error", "-f", "mp3", "-i", "pipe:0", "-ar", "44100", "-ac", "1", "-f", "s16le", "pipe:1")
	decode.Stdin = bytes.NewReader(audio)
	pcm, err := decode.Output()
	if err != nil {
		t.Fatal(err)
	}
	duration := float64(len(pcm)) / (44100 * 2)
	if duration < 1.45 || duration > 1.7 {
		t.Fatal("speech edge trim lost pause or retained silence", duration)
	}
	// Encoding adds a small frame delay, but speech must start well within 100ms.
	first := len(pcm) / 2
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		if sample > 100 || sample < -100 {
			first = i / 2
			break
		}
	}
	if float64(first)/44100 > 0.1 {
		t.Fatal("speech still begins with silence")
	}
}
