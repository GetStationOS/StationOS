package broadcast

import "sync"

// WatchChanges observes runtime/configuration changes without subscribing to audio
// or starting a station. Signals coalesce; observers reread the latest snapshot.
func (m *Manager) WatchChanges() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	m.mu.Lock()
	if m.closed {
		close(ch)
		m.mu.Unlock()
		return ch, func() {}
	}
	if m.watchers == nil {
		m.watchers = map[chan struct{}]struct{}{}
	}
	m.watchers[ch] = struct{}{}
	ch <- struct{}{}
	m.mu.Unlock()
	var once sync.Once
	return ch, func() { once.Do(func() { m.mu.Lock(); delete(m.watchers, ch); m.mu.Unlock() }) }
}

// Changed announces committed configuration changes to management views.
func (m *Manager) Changed() { m.mu.Lock(); defer m.mu.Unlock(); m.changedLocked() }
func (m *Manager) changedLocked() {
	for ch := range m.watchers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func sameRuntimeState(a, b State) bool {
	return a.RepeatRelaxed == b.RepeatRelaxed && a.Status == b.Status && a.Phase == b.Phase && a.PresenterStatus == b.PresenterStatus && a.Error == b.Error && a.Current.ID == b.Current.ID && a.Next.ID == b.Next.ID && a.Programme.Key == b.Programme.Key && a.Following.Key == b.Following.Key && a.Started.Equal(b.Started)
}
