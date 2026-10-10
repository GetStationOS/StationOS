package broadcast

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
)

func TestDeliveryClockRebuildsReserveAfterSupplyStall(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := deliveryClock{lead: 5 * time.Second}
	if clock.delay(now) != 0 {
		t.Fatal("startup burst delayed")
	}
	clock.advance(5 * audioBytesPerSecond)
	clock.advance(audioBytesPerSecond)
	if clock.delay(now) != time.Second {
		t.Fatal("burst was not limited to five seconds")
	}
	// A two-second supply interruption must allow catch-up rather than resetting
	// the clock to now, which permanently loses the player's buffered lead.
	afterStall := now.Add(3 * time.Second)
	if clock.delay(afterStall) != 0 {
		t.Fatal("catch-up delayed")
	}
	clock.advance(2 * audioBytesPerSecond)
	if clock.delay(afterStall) != 0 {
		t.Fatal("short stall discarded catch-up budget")
	}
	clock.advance(audioBytesPerSecond)
	if clock.delay(afterStall) != time.Second {
		t.Fatal("catch-up did not return to real-time pacing")
	}
	// A very long outage cannot cause unlimited output at once.
	later := now.Add(time.Hour)
	clock.delay(later)
	clock.advance(6 * audioBytesPerSecond)
	if clock.delay(later) != time.Second {
		t.Fatal("unbounded recovery burst")
	}
}

type countingAudio struct {
	reader *bytes.Reader
	read   atomic.Int64
}

func (r *countingAudio) Read(p []byte) (int, error) {
	n, e := r.reader.Read(p)
	r.read.Add(int64(n))
	return n, e
}
func (*countingAudio) Close() error { return nil }

func TestReadAheadIsBoundedAndPreservesEveryByte(t *testing.T) {
	data := bytes.Repeat([]byte("music"), 100000)
	source := &countingAudio{reader: bytes.NewReader(data)}
	audio := readAheadAudio(context.Background(), source)
	defer audio.Close()
	b := audio.(*bufferedAudio)
	waitFor(t, func() bool { return len(b.packets) == cap(b.packets) })
	// The bounded channel plus one in-flight read is the entire read-ahead.
	if got := source.read.Load(); got > 33*audioChunkBytes {
		t.Fatal("unbounded source read-ahead", got)
	}
	var got bytes.Buffer
	buf := make([]byte, 137) // partial reads must not lose packet tails or EOF
	for {
		n, err := audio.Read(buf)
		got.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatal("read-ahead changed the audio")
	}
}

func TestReadAheadContinuesThroughSourcePauseAndCancels(t *testing.T) {
	r, w := io.Pipe()
	audio := readAheadAudio(context.Background(), r)
	defer audio.Close()
	b := audio.(*bufferedAudio)
	written := make(chan struct{})
	go func() { w.Write(bytes.Repeat([]byte{7}, 32*audioChunkBytes)); close(written) }()
	<-written // upstream now stalls indefinitely, with eight seconds ready
	waitFor(t, func() bool { return len(b.packets) == 32 })
	for i := 0; i < 16; i++ {
		buf := make([]byte, audioChunkBytes)
		if n, err := audio.Read(buf); n != len(buf) || err != nil || buf[0] != 7 {
			t.Fatal("source pause interrupted ready audio", n, err)
		}
	}
	closed := make(chan struct{})
	go func() { audio.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not release blocked source")
	}
	w.Close()
}

type errorAudio struct {
	sent bool
	err  error
}

func (r *errorAudio) Read(p []byte) (int, error) {
	if r.sent {
		return 0, r.err
	}
	r.sent = true
	return copy(p, []byte("last audio")), nil
}
func (*errorAudio) Close() error { return nil }
func TestReadAheadPreservesSourceError(t *testing.T) {
	failure := errors.New("source failed")
	audio := readAheadAudio(context.Background(), &errorAudio{err: failure})
	defer audio.Close()
	got, err := io.ReadAll(audio)
	if string(got) != "last audio" || !errors.Is(err, failure) {
		t.Fatal("lost audio or source error", string(got), err)
	}
}

func TestStartupBurstAndLateJoinShareBoundedAudio(t *testing.T) {
	m, e, _ := fixture(t, Options{StreamBuffer: 5 * time.Second})
	a, _ := m.Subscribe(context.Background(), "radio")
	defer a.Close()
	o := opened(t, e)
	data := make([]byte, 24*audioChunkBytes)
	for i := 0; i < 24; i++ {
		for j := 0; j < audioChunkBytes; j++ {
			data[i*audioChunkBytes+j] = byte(i)
		}
	}
	start := time.Now()
	go o.write.Write(data)
	for i := 0; i < 24; i++ {
		if !bytes.Equal(receive(t, a), data[i*audioChunkBytes:(i+1)*audioChunkBytes]) {
			t.Fatal("startup changed programme")
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second || elapsed < 500*time.Millisecond {
		t.Fatal("startup reserve or real-time pacing missing", elapsed)
	}
	b, _ := m.Subscribe(context.Background(), "radio")
	defer b.Close()
	snapshot := receive(t, b)
	if len(snapshot) > 5*audioBytesPerSecond || len(snapshot) < 4*audioBytesPerSecond || !bytes.HasSuffix(data, snapshot) {
		t.Fatal("late join missing bounded recent programme", len(snapshot))
	}
	follow := bytes.Repeat([]byte{24}, audioChunkBytes)
	o.write.Write(follow)
	if !bytes.Equal(receive(t, a), follow) || !bytes.Equal(receive(t, b), follow) {
		t.Fatal("replay/live boundary lost or duplicated bytes")
	}
	if e.count.Load() != 1 {
		t.Fatal("new listener started another encoder")
	}
}

func TestRealFFmpegStartupBurstRemainsDecodable(t *testing.T) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, path, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=10", "-f", "flac", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	encoder := FFmpeg{Path: path, Sources: sourceRepo{}, Factory: func(context.Context, sources.Config) (sources.AudioSource, error) { return toneProvider{raw}, nil }}
	repo := &memoryRepo{playlist: "pool", pool: []content.AudioItem{{ID: "tone", SourceID: "music", SourceItemID: "tone"}}}
	m := New(repo, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{StreamBuffer: 5 * time.Second})
	defer m.Close()
	sub, err := m.Subscribe(ctx, "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	start := time.Now()
	var captured bytes.Buffer
	for captured.Len() < 19*audioChunkBytes {
		captured.Write(receive(t, sub))
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("real encoder did not fill the client reserve promptly")
	}
	decode := exec.CommandContext(ctx, path, "-v", "error", "-f", "mp3", "-i", "pipe:0", "-f", "s16le", "pipe:1")
	decode.Stdin = bytes.NewReader(captured.Bytes())
	pcm, err := decode.Output()
	if err != nil || len(pcm) < 4*44100*2*2 {
		t.Fatal("buffered audio cannot decode", len(pcm), err)
	}
	late, err := m.Subscribe(ctx, "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer late.Close()
	joined := receive(t, late)
	decode = exec.CommandContext(ctx, path, "-v", "error", "-f", "mp3", "-i", "pipe:0", "-f", "s16le", "pipe:1")
	decode.Stdin = bytes.NewReader(joined)
	pcm, err = decode.Output()
	if err != nil || len(pcm) < 4*44100*2*2 {
		t.Fatal("late join cannot decode MP3 frames", len(pcm), err)
	}
}

func TestReadAheadParentCancellationReleasesBlockedRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r, w := io.Pipe()
	defer w.Close()
	audio := readAheadAudio(ctx, r)
	defer audio.Close()
	cancel()
	select {
	case <-audio.(*bufferedAudio).done:
	case <-time.After(time.Second):
		t.Fatal("parent cancellation leaked read-ahead worker")
	}
}

type pausedSupplyEncoder struct {
	paused  chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (e *pausedSupplyEncoder) Open(ctx context.Context, _ content.AudioItem) (io.ReadCloser, error) {
	e.calls.Add(1)
	r, w := io.Pipe()
	context.AfterFunc(ctx, func() { r.Close(); w.Close() })
	go func() {
		defer w.Close()
		for i := 0; i < 80; i++ {
			if i == 40 {
				close(e.paused)
				select {
				case <-e.release:
				case <-ctx.Done():
					return
				}
			}
			if _, err := w.Write(bytes.Repeat([]byte{byte(i)}, audioChunkBytes)); err != nil {
				return
			}
		}
	}()
	return readAheadAudio(ctx, r), nil
}
func TestMidTrackSupplyPauseDoesNotInterruptSharedDelivery(t *testing.T) {
	e := &pausedSupplyEncoder{paused: make(chan struct{}), release: make(chan struct{})}
	repo := &memoryRepo{playlist: "pool", pool: []content.AudioItem{{ID: "song"}}}
	m := New(repo, e, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{StreamBuffer: 5 * time.Second})
	defer m.Close()
	a, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	select {
	case <-e.paused:
	case <-time.After(time.Second):
		t.Fatal("source did not read ahead before stalling")
	}
	var previous time.Time
	for i := 0; i < 24; i++ {
		data := receive(t, a)
		now := time.Now()
		if !bytes.Equal(data, bytes.Repeat([]byte{byte(i)}, audioChunkBytes)) {
			t.Fatal("audio changed during source stall", i)
		}
		if i >= 20 && now.Sub(previous) > 650*time.Millisecond {
			t.Fatal("source stall interrupted delivery", now.Sub(previous))
		}
		previous = now
	}
	b, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if len(receive(t, b)) < 4*audioBytesPerSecond {
		t.Fatal("late listener could not build a reserve")
	}
	close(e.release)
	for i := 24; i < 32; i++ {
		x, y := receive(t, a), receive(t, b)
		now := time.Now()
		if !bytes.Equal(x, y) || x[0] != byte(i) {
			t.Fatal("listeners diverged after recovery", i)
		}
		if now.Sub(previous) > 650*time.Millisecond {
			t.Fatal("recovery interrupted delivery", now.Sub(previous))
		}
		previous = now
	}
	if e.calls.Load() != 1 {
		t.Fatal("listener duplicated encoder")
	}
}

func TestBufferedProgrammeHandsOverAtAudibleTrackBoundary(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 59, 58, 0, time.UTC)
	shows := timedSchedule{shows: []schedule.Show{
		{ID: "pop", StationID: "radio", Day: 0, Start: 540, End: 600, PlaylistID: "pop-pool"},
		{ID: "jazz", StationID: "radio", Day: 0, Start: 600, End: 660, PlaylistID: "jazz-pool"},
	}}
	e := &fakeEncoder{calls: make(chan opening, 8)}
	m := New(showPools{}, e, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{StreamBuffer: 5 * time.Second, Schedule: shows, Now: func() time.Time { return now }})
	defer m.Close()
	sub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first := opened(t, e)
	if first.item.ID != "pop-pool" {
		t.Fatal(first.item)
	}
	go func() { first.write.Write(make([]byte, 3*audioBytesPerSecond)); first.write.Close() }()
	for total := 0; total < 3*audioBytesPerSecond; {
		total += len(receive(t, sub))
	}
	// Three seconds of audio crossed 10:00, despite being sent in the startup
	// burst before the real wall clock moved. Do not add another Pop track.
	next := opened(t, e)
	if next.item.ID != "jazz-pool" {
		t.Fatal("buffering delayed the show handover", next.item)
	}
}
