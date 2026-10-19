package broadcast

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os/exec"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/sources"
	"testing"
	"time"
)

type sourceRepo struct{}

func (sourceRepo) Delete(context.Context, string) error { return nil }

func (sourceRepo) Get(context.Context, string) (sources.Config, error) {
	return sources.Config{ID: "music"}, nil
}
func (sourceRepo) Save(context.Context, sources.Config) error     { return nil }
func (sourceRepo) List(context.Context) ([]sources.Config, error) { return nil, nil }

type toneProvider struct{ audio []byte }

func (t toneProvider) Profile() sources.Profile {
	return sources.Profile{Capabilities: sources.Capabilities{Streaming: true}, Restrictions: sources.Restrictions{PublicBroadcast: sources.OperatorResponsible}}
}
func (t toneProvider) Browse(context.Context, string, int, int) (sources.Page, error) {
	return sources.Page{}, sources.ErrUnsupported
}
func (t toneProvider) Get(context.Context, string) (content.AudioItem, error) {
	return content.AudioItem{}, sources.ErrUnsupported
}
func (t toneProvider) Resolve(context.Context, string) ([]content.Resource, error) {
	return nil, sources.ErrUnsupported
}
func (t toneProvider) OpenStream(context.Context, string) (io.ReadCloser, string, error) {
	return io.NopCloser(bytes.NewReader(t.audio)), "flac", nil
}
func TestRealFLACPreloadToDecodableMP3(t *testing.T) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, path, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-f", "flac", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	encoder := FFmpeg{Path: path, Sources: sourceRepo{}, Factory: func(context.Context, sources.Config) (sources.AudioSource, error) { return toneProvider{raw}, nil }}
	audio, err := encoder.Preload(ctx, content.AudioItem{SourceID: "music", SourceItemID: "tone"})
	if err != nil {
		t.Fatal(err)
	}
	mp3, err := io.ReadAll(audio)
	audio.Close()
	if err != nil || len(mp3) < 10000 {
		t.Fatal(len(mp3), err)
	}
	if bytes.HasPrefix(mp3, []byte("ID3")) || bytes.Contains(mp3, []byte("Xing")) {
		t.Fatal("per-track tags in continuous output")
	}
	decode := exec.CommandContext(ctx, path, "-v", "error", "-f", "mp3", "-i", "pipe:0", "-f", "s16le", "pipe:1")
	decode.Stdin = bytes.NewReader(mp3)
	pcm, err := decode.Output()
	if err != nil || len(pcm) < 44100*2*2 {
		t.Fatal("encoded broadcast cannot decode", len(pcm), err)
	}
}

func TestRealMusicTailToSpeechHasNoMultiSecondGap(t *testing.T) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Two music phrases, a deliberate internal pause, then three seconds of
	// trailing silence of the sort found on music files.
	raw, err := exec.CommandContext(ctx, path, "-v", "error", "-f", "lavfi", "-i", "aevalsrc=if(lt(t\\,0.4)+between(t\\,1\\,1.4)\\,0.2*sin(2*PI*440*t)\\,0):d=4.4:s=44100", "-f", "flac", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	e := FFmpeg{Path: path, Sources: sourceRepo{}, Factory: func(context.Context, sources.Config) (sources.AudioSource, error) { return toneProvider{raw}, nil }}
	reader, err := e.Preload(ctx, content.AudioItem{SourceID: "music", SourceItemID: "tail"})
	if err != nil {
		t.Fatal(err)
	}
	music, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	speechRaw, err := exec.CommandContext(ctx, path, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=700:duration=1", "-f", "mp3", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	speech, err := (production.FFmpeg{Path: path}).MP3(ctx, speechRaw)
	if err != nil {
		t.Fatal(err)
	}
	decode := exec.CommandContext(ctx, path, "-v", "error", "-f", "mp3", "-i", "pipe:0", "-ar", "44100", "-ac", "1", "-f", "s16le", "pipe:1")
	decode.Stdin = bytes.NewReader(append(music, speech...))
	pcm, err := decode.Output()
	if err != nil {
		t.Fatal(err)
	}
	duration := float64(len(pcm)) / (44100 * 2)
	if duration < 2.4 || duration > 2.6 {
		t.Fatal("trailing music silence retained or music cut", duration)
	}
	sample := func(i int) int16 { return int16(binary.LittleEndian.Uint16(pcm[i*2:])) }
	// The internal pause remains quiet, while the handover's longest quiet run
	// stays below 150ms (including MP3 encoder padding), rather than three seconds.
	for i := int(0.6 * 44100); i < int(0.9*44100); i++ {
		if v := sample(i); v > 100 || v < -100 {
			t.Fatal("internal music pause lost")
		}
	}
	longest, run := 0, 0
	for i := int(1.3 * 44100); i < int(1.8*44100); i++ {
		if v := sample(i); v <= 100 && v >= -100 {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	if float64(longest)/44100 > 0.15 {
		t.Fatal("audible gap at handover", float64(longest)/44100)
	}
}

func TestTrimmedMusicPipelineCancelsBeforeSongEnd(t *testing.T) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, path, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=30", "-f", "flac", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	e := FFmpeg{Path: path, Sources: sourceRepo{}, Factory: func(context.Context, sources.Config) (sources.AudioSource, error) { return toneProvider{raw}, nil }}
	audio, err := e.Preload(ctx, content.AudioItem{SourceID: "music", SourceItemID: "long"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- audio.Close() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("decoder/encoder cancellation stalled")
	}
}
