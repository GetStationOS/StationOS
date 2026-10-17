// Package changefeed supplies process-local, coalescing committed-data notifications.
// Observers subscribe before reading a snapshot. Slow readers never block writers.
package changefeed

import "sync"

type Bus struct {
	mu       sync.Mutex
	watchers map[string]map[chan struct{}]struct{}
}

func (b *Bus) Watch(topic string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	if b == nil {
		return ch, func() {}
	}
	b.mu.Lock()
	if b.watchers == nil {
		b.watchers = make(map[string]map[chan struct{}]struct{})
	}
	if b.watchers[topic] == nil {
		b.watchers[topic] = make(map[chan struct{}]struct{})
	}
	b.watchers[topic][ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() { once.Do(func() { b.mu.Lock(); delete(b.watchers[topic], ch); b.mu.Unlock() }) }
}

// Publish must run only after the transaction/statement has successfully committed.
func (b *Bus) Publish(topic string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.watchers[topic] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
