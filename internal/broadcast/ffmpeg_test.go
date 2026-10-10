package broadcast

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/sources"
	"testing"
	"time"
)

type sourceRepo struct{}

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
