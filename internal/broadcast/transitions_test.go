package broadcast

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

type delayedCleanup struct {
	io.ReadCloser
	release <-chan struct{}
	started chan struct{}
	once    sync.Once
}

func (r *delayedCleanup) Close() error {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return r.ReadCloser.Close()
}

type aheadTestEncoder struct {
	fakeEncoder
	cleanupRelease chan struct{}
	cleanupStarted chan struct{}
	primed         chan struct{}
}

func (e *aheadTestEncoder) Open(ctx context.Context, item content.AudioItem) (io.ReadCloser, error) {
	audio, err := e.fakeEncoder.Open(ctx, item)
	if err == nil && e.count.Load() == 1 {
		audio = &delayedCleanup{ReadCloser: audio, release: e.cleanupRelease, started: e.cleanupStarted}
	}
	return audio, err
}
func (e *aheadTestEncoder) Preload(ctx context.Context, item content.AudioItem) (io.ReadCloser, error) {
	audio, err := e.fakeEncoder.Open(ctx, item)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := audio.Read(buf)
	if n == 0 {
		audio.Close()
		return nil, err
	}
	select {
	case e.primed <- struct{}{}:
	default:
	}
	return &primedAudio{Reader: io.MultiReader(bytes.NewReader(buf[:n]), audio), audio: audio}, nil
}
func TestReadyChatterAndPrimedMusicDoNotWaitForTrackCleanup(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	shows := timedSchedule{shows: []schedule.Show{{ID: "show", StationID: "radio", Day: 0, Start: 540, End: 600, PlaylistID: "pool", DJ: djs.Profile{ID: "dj", HasKey: true}}}}
	repo := &memoryRepo{playlist: "pool", pool: []content.AudioItem{length("one", 180), length("two", 180)}}
	encoder := &aheadTestEncoder{fakeEncoder: fakeEncoder{calls: make(chan opening, 8)}, cleanupRelease: make(chan struct{}), cleanupStarted: make(chan struct{}), primed: make(chan struct{}, 8)}
	links := readyLinks{called: make(chan production.LinkRequest, 8)}
	m := New(repo, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Schedule: shows, Links: links, Now: func() time.Time { return now }})
	defer m.Close()
	defer close(encoder.cleanupRelease)
	a, _ := m.Subscribe(context.Background(), "radio")
	defer a.Close()
	b, _ := m.Subscribe(context.Background(), "radio")
	defer b.Close()
	current := opened(t, &encoder.fakeEncoder)
	next := opened(t, &encoder.fakeEncoder)
	req := <-links.called
	if req.Current.ID != current.item.ID || req.Next.ID != next.item.ID {
		t.Fatal("chatter was not prepared for the queued song", req)
	}
	next.write.Write([]byte("NEXT MUSIC"))
	<-encoder.primed
	current.write.Write([]byte("CURRENT MUSIC"))
	receive(t, a)
	receive(t, b)
	boundary := time.Now()
	current.write.Close()
	select {
	case data := <-a.Audio:
		if string(data) != "HANDOVER" {
			t.Fatal("missing ready chatter", string(data))
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("ready chatter waited for source cleanup")
	}
	if string(receive(t, b)) != "HANDOVER" {
		t.Fatal("listeners heard different chatter")
	}
	if string(receive(t, a)) != "NEXT MUSIC" || string(receive(t, b)) != "NEXT MUSIC" {
		t.Fatal("primed music not used")
	}
	if time.Since(boundary) > 300*time.Millisecond {
		t.Fatal("transition introduced a gap")
	}
	// Cleanup runs asynchronously: verify it starts without requiring the
	// scheduler to run its goroutine before the ready audio is delivered.
	select {
	case <-encoder.cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("old audio not cleaned up")
	}
	if m.State("radio").Current.ID != next.item.ID {
		t.Fatal("wrong queued track")
	}
}

type cancellationLink struct {
	started   chan struct{}
	cancelled chan struct{}
	release   <-chan struct{}
}

func (p cancellationLink) Link(ctx context.Context, _ production.LinkRequest) ([]byte, error) {
	select {
	case p.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	select {
	case p.cancelled <- struct{}{}:
	default:
	}
	<-p.release
	return nil, ctx.Err()
}
func TestLateChatterCancellationDoesNotBlockMusic(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	shows := timedSchedule{shows: []schedule.Show{{ID: "show", StationID: "radio", Day: 0, Start: 540, End: 600, PlaylistID: "pool", DJ: djs.Profile{ID: "dj", HasKey: true}}}}
	release := make(chan struct{})
	links := cancellationLink{started: make(chan struct{}, 8), cancelled: make(chan struct{}, 8), release: release}
	e := &fakeEncoder{calls: make(chan opening, 8)}
	m := New(showPools{}, e, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Schedule: shows, Links: links, Now: func() time.Time { return now }})
	defer m.Close()
	defer close(release)
	sub, _ := m.Subscribe(context.Background(), "radio")
	defer sub.Close()
	first := opened(t, e)
	<-links.started
	first.write.Write([]byte("MUSIC"))
	receive(t, sub)
	first.write.Close()
	select {
	case <-links.cancelled:
	case <-time.After(time.Second):
		t.Fatal("late link not cancelled")
	}
	select {
	case next := <-e.calls:
		next.write.Write([]byte("MORE MUSIC"))
	case <-time.After(300 * time.Millisecond):
		t.Fatal("music waited for cancelled AI")
	}
	if string(receive(t, sub)) != "MORE MUSIC" {
		t.Fatal("music interrupted")
	}
}
