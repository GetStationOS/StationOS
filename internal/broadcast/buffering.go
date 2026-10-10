package broadcast

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

const audioChunkBytes = 4096
const audioBytesPerSecond = 16000 // 128 kbps MP3

// deliveryClock provides a bounded startup/catch-up burst, then real-time
// delivery. A short supply stall consumes the lead but does not discard it.
type deliveryClock struct {
	lead time.Duration
	next time.Time
}

func (c *deliveryClock) delay(now time.Time) time.Duration {
	floor := now.Add(-c.lead)
	if c.next.IsZero() || c.next.Before(floor) {
		c.next = floor
	}
	return max(time.Duration(0), c.next.Sub(now))
}
func (c *deliveryClock) advance(n int) {
	c.next = c.next.Add(time.Duration(n) * time.Second / audioBytesPerSecond)
}

// Selection uses the audible programme position, not an early delivery boundary.
// Otherwise the startup burst could choose another outgoing track before the
// wall clock reaches a show ending which this track has already crossed.
func (c *deliveryClock) programmeTime(now, wall time.Time) time.Time {
	if c.lead <= 0 || c.next.IsZero() {
		return now
	}
	return now.Add(max(time.Duration(0), c.next.Add(c.lead).Sub(wall)))
}

// recentAudio is a station-owned byte-bounded window. Chunks are immutable and
// shared by all listeners. A join snapshot is queued under the fan-out lock.
type recentAudio struct {
	limit  int
	bytes  int
	chunks [][]byte
}

func (b *recentAudio) append(data []byte) {
	if b.limit <= 0 {
		return
	}
	b.chunks = append(b.chunks, data)
	b.bytes += len(data)
	for b.bytes > b.limit || len(b.chunks) > 64 {
		b.bytes -= len(b.chunks[0])
		b.chunks[0] = nil
		b.chunks = b.chunks[1:]
	}
}
func (b *recentAudio) snapshot() []byte {
	data := make([]byte, 0, b.bytes)
	for _, part := range b.chunks {
		data = append(data, part...)
	}
	return data
}

type audioPacket struct {
	data []byte
	err  error
}

type bufferedAudio struct {
	ctx        context.Context
	cancel     context.CancelFunc
	upstream   io.ReadCloser
	packets    chan audioPacket
	pending    audioPacket
	done       chan struct{}
	once       sync.Once
	sourceOnce sync.Once
	stop       func() bool
	closeErr   error
}

// Eight seconds of encoded read-ahead per track absorbs source/encoder jitter.
// The queued track has its own bounded reader; pipe backpressure bounds source
// fetching and no whole songs or per-listener encoders are created.
func readAheadAudio(parent context.Context, upstream io.ReadCloser) io.ReadCloser {
	ctx, cancel := context.WithCancel(parent)
	b := &bufferedAudio{ctx: ctx, cancel: cancel, upstream: upstream, packets: make(chan audioPacket, 32), done: make(chan struct{})}
	b.stop = context.AfterFunc(ctx, b.closeSource)
	go func() {
		defer close(b.done)
		defer close(b.packets)
		for {
			data := make([]byte, audioChunkBytes)
			n, err := io.ReadFull(upstream, data)
			if errors.Is(err, io.ErrUnexpectedEOF) {
				err = io.EOF
			}
			select {
			case b.packets <- audioPacket{data[:n], err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return b
}
func (b *bufferedAudio) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(b.pending.data) == 0 {
		select {
		case packet, open := <-b.packets:
			if !open {
				return 0, io.EOF
			}
			b.pending = packet
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		}
	}
	n := copy(p, b.pending.data)
	b.pending.data = b.pending.data[n:]
	if len(b.pending.data) != 0 {
		return n, nil
	}
	return n, b.pending.err
}
func (b *bufferedAudio) closeSource() {
	b.sourceOnce.Do(func() { b.closeErr = b.upstream.Close() })
}
func (b *bufferedAudio) Close() error {
	b.once.Do(func() {
		b.stop()
		b.cancel()
		b.closeSource()
		<-b.done
	})
	return b.closeErr
}
