package broadcast

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"

	"stationos.local/stationos/internal/content"
)

// PreloadingEncoder primes a bounded amount of the queued track before airtime.
// Implementations must not buffer a whole song and must honour cancellation.
type PreloadingEncoder interface {
	Preload(context.Context, content.AudioItem) (io.ReadCloser, error)
}

type primedAudio struct {
	io.Reader
	audio  io.ReadCloser
	cancel context.CancelFunc
}

func (p *primedAudio) Close() error {
	if p.cancel != nil {
		defer p.cancel()
	}
	return p.audio.Close()
}

func (f FFmpeg) Preload(ctx context.Context, item content.AudioItem) (io.ReadCloser, error) {
	audio, err := f.Open(ctx, item)
	if err != nil {
		return nil, err
	}
	// A single chunk is enough to pay source/decoder startup ahead of time.
	// Backpressure on FFmpeg's pipe bounds any further read-ahead.
	buf := make([]byte, 4096)
	n, err := audio.Read(buf)
	if n == 0 || (err != nil && !errors.Is(err, io.EOF)) {
		audio.Close()
		if err == nil {
			err = io.ErrNoProgress
		}
		return nil, err
	}
	return &primedAudio{Reader: io.MultiReader(bytes.NewReader(buf[:n]), audio), audio: audio}, nil
}

type audioPreparation struct {
	item      content.AudioItem
	show      string
	music     int
	normalise bool
	cancel    context.CancelFunc
	result    chan io.ReadCloser
}

func startAudioPreparation(ctx context.Context, e PreloadingEncoder, item content.AudioItem, show string, workers *sync.WaitGroup) *audioPreparation {
	ctx, cancel := context.WithCancel(ctx)
	p := &audioPreparation{item: item, show: show, music: musicLevel(ctx), normalise: musicNormalisation(ctx), cancel: cancel, result: make(chan io.ReadCloser)}
	workers.Add(1)
	go func() {
		defer workers.Done()
		audio, err := e.Preload(ctx, item)
		if err != nil {
			cancel()
			close(p.result)
			return
		}
		select {
		case p.result <- &primedAudio{Reader: audio, audio: audio, cancel: cancel}:
			// Ownership, including cancellation, moves to the playout reader.
		case <-ctx.Done():
			audio.Close()
		}
	}()
	return p
}
