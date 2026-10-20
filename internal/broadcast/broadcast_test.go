package broadcast

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"stationos.local/stationos/internal/content"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryRepo struct {
	mu       sync.Mutex
	playlist string
	pool     []content.AudioItem
}

func (r *memoryRepo) Assignment(context.Context, string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.playlist, nil
}
func (r *memoryRepo) Assign(_ context.Context, _, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.playlist = id
	return nil
}
func (r *memoryRepo) Pool(context.Context, string) ([]content.AudioItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]content.AudioItem(nil), r.pool...), nil
}

type opening struct {
	item  content.AudioItem
	write *io.PipeWriter
}
type fakeEncoder struct {
	calls chan opening
	count atomic.Int32
}

func (f *fakeEncoder) Open(ctx context.Context, item content.AudioItem) (io.ReadCloser, error) {
	f.count.Add(1)
	reader, writer := io.Pipe()
	context.AfterFunc(ctx, func() { reader.Close(); writer.Close() })
	select {
	case f.calls <- opening{item, writer}:
		return reader, nil
	case <-ctx.Done():
		reader.Close()
		writer.Close()
		return nil, ctx.Err()
	}
}
func fixture(t *testing.T, o Options) (*Manager, *fakeEncoder, *memoryRepo) {
	t.Helper()
	repo := &memoryRepo{playlist: "pool", pool: []content.AudioItem{{ID: "one", Title: "One"}, {ID: "two", Title: "Two"}}}
	encoder := &fakeEncoder{calls: make(chan opening, 16)}
	m := New(repo, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), o)
	t.Cleanup(m.Close)
	return m, encoder, repo
}
func opened(t *testing.T, e *fakeEncoder) opening {
	t.Helper()
	select {
	case o := <-e.calls:
		return o
	case <-time.After(3 * time.Second):
		t.Fatal("encoder not started")
		return opening{}
	}
}
func receive(t *testing.T, s *Subscription) []byte {
	t.Helper()
	select {
	case b, ok := <-s.Audio:
		if !ok {
			t.Fatal("listener closed unexpectedly")
		}
		return b
	case <-time.After(3 * time.Second):
		t.Fatal("audio not received")
		return nil
	}
}
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestSharedProgrammeAndDormancy(t *testing.T) {
	m, e, _ := fixture(t, Options{})
	if m.State("radio").Status != "Dormant" || e.count.Load() != 0 {
		t.Fatal("reading status started encoder")
	}
	a, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	o := opened(t, e)
	if e.count.Load() != 1 {
		t.Fatal("per-listener encoding")
	}
	if _, err = o.write.Write([]byte("same broadcast")); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(receive(t, a), receive(t, b)) {
		t.Fatal("listeners received different programmes")
	}
	state := m.State("radio")
	if state.Listeners != 2 || state.Status != "Broadcasting" || state.Current.ID != o.item.ID || state.Next.ID == o.item.ID {
		t.Fatal(state)
	}
	a.Close()
	if m.State("radio").Listeners != 1 {
		t.Fatal("other listener removed")
	}
	late, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	o.write.Write([]byte("live point"))
	if !bytes.Equal(receive(t, b), receive(t, late)) {
		t.Fatal("late listener did not join live")
	}
	b.Close()
	late.Close()
	waitFor(t, func() bool { return m.State("radio").Status == "Dormant" })
	if _, err = o.write.Write([]byte("after close")); err == nil {
		t.Fatal("upstream remained open")
	}
	c, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	opened(t, e)
	if e.count.Load() != 2 {
		t.Fatal("reconnect did not restart exactly once")
	}
}
func TestSlowListenerDoesNotBlockBroadcast(t *testing.T) {
	m, e, _ := fixture(t, Options{BufferChunks: 2})
	fast, _ := m.Subscribe(context.Background(), "radio")
	slow, _ := m.Subscribe(context.Background(), "radio")
	defer fast.Close()
	defer slow.Close()
	o := opened(t, e)
	for n := 0; n < 4; n++ {
		if _, err := o.write.Write([]byte{byte(n)}); err != nil {
			t.Fatal(err)
		}
		if receive(t, fast)[0] != byte(n) {
			t.Fatal("fast listener lost bytes")
		}
	}
	waitFor(t, func() bool { return m.State("radio").Listeners == 1 })
	count := 0
	for range slow.Audio {
		count++
	}
	if count != 2 {
		t.Fatal("unbounded slow queue", count)
	}
}
func TestCapacityAndConcurrentAdmission(t *testing.T) {
	m, e, _ := fixture(t, Options{MaxStations: 1, MaxListeners: 64})
	var wg sync.WaitGroup
	subs := make(chan *Subscription, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := m.Subscribe(context.Background(), "radio")
			if err != nil {
				t.Error(err)
			} else {
				subs <- s
			}
		}()
	}
	wg.Wait()
	close(subs)
	opened(t, e)
	if e.count.Load() != 1 || m.State("radio").Listeners != 64 {
		t.Fatal("concurrent admission duplicated worker")
	}
	if _, err := m.Subscribe(context.Background(), "radio"); err != ErrBusy {
		t.Fatal(err)
	}
	if _, err := m.Subscribe(context.Background(), "another"); err != ErrBusy {
		t.Fatal(err)
	}
	for s := range subs {
		s.Close()
	}
	waitFor(t, func() bool { return m.State("radio").Status == "Dormant" })
}
func TestPoolRefreshAndAssignmentStop(t *testing.T) {
	m, e, repo := fixture(t, Options{})
	s, _ := m.Subscribe(context.Background(), "radio")
	defer s.Close()
	o := opened(t, e)
	o.write.Write([]byte("track"))
	receive(t, s)
	promised := m.State("radio").Next.ID
	repo.mu.Lock()
	repo.pool = []content.AudioItem{{ID: "new", Title: "New music"}}
	repo.mu.Unlock()
	o.write.Close()
	second := opened(t, e)
	if second.item.ID != promised {
		t.Fatal("committed next changed", second.item)
	}
	second.write.Write([]byte("new"))
	receive(t, s)
	m.StopPlaylist("pool")
	waitFor(t, func() bool { return m.State("radio").Status == "Dormant" })
	repo.Assign(context.Background(), "radio", "")
	if _, err := m.Subscribe(context.Background(), "radio"); err != ErrUnavailable {
		t.Fatal(err)
	}
}

func TestBroadcastPacesFiniteInput(t *testing.T) {
	m, e, _ := fixture(t, Options{})
	sub, err := m.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	o := opened(t, e)
	start := time.Now()
	go func() { o.write.Write(make([]byte, 8*4096)) }()
	for i := 0; i < 8; i++ {
		if len(receive(t, sub)) != 4096 {
			t.Fatal("unexpected chunk")
		}
	}
	if elapsed := time.Since(start); elapsed < 1600*time.Millisecond || elapsed > 3*time.Second {
		t.Fatal("128kbps shared pacing", elapsed)
	}
}

func TestPlayerSessionReplacesProbeWithoutRestartingBroadcast(t *testing.T) {
	m, e, _ := fixture(t, Options{MaxListeners: 2})
	probe, err := m.SubscribeClient(context.Background(), "radio", "player-a")
	if err != nil {
		t.Fatal(err)
	}
	o := opened(t, e)
	other, err := m.SubscribeClient(context.Background(), "radio", "player-b")
	if err != nil {
		t.Fatal(err)
	}
	playback, err := m.SubscribeClient(context.Background(), "radio", "player-a")
	if err != nil {
		t.Fatal("replacement failed at capacity", err)
	}
	select {
	case <-probe.Done:
	default:
		t.Fatal("probe still consumes broadcast data")
	}
	probe.Close()
	if m.State("radio").Listeners != 2 || e.count.Load() != 1 {
		t.Fatal("replacement changed programme or removed another listener", m.State("radio"))
	}
	if _, err := m.Subscribe(context.Background(), "radio"); err != ErrBusy {
		t.Fatal("independent external listener bypassed capacity", err)
	}
	if _, err := o.write.Write([]byte("same live audio")); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(receive(t, playback), receive(t, other)) {
		t.Fatal("players received different audio")
	}
	other.Close()
	if m.State("radio").Listeners != 1 {
		t.Fatal("remaining app player counted incorrectly")
	}
	playback.Close()
	waitFor(t, func() bool { return m.State("radio").Status == "Dormant" })
}
