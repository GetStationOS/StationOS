package inference

import (
	"context"
	"errors"
	"sync"
)

// Gate admits one local request across all connections. Short live links have
// priority over queued show planning, then producer work. Running work retains its deadline and cancellation.
type Gate struct {
	mu                       sync.Mutex
	active                   bool
	urgent, live, background []*ticket
}
type ticket struct {
	ready   chan struct{}
	granted bool
}

type priority int

const (
	backgroundPriority priority = iota
	livePriority
	linkPriority
)

func (g *Gate) acquire(ctx context.Context, live bool) (func(), error) {
	p := backgroundPriority
	if live {
		p = livePriority
	}
	return g.acquirePriority(ctx, p)
}
func (g *Gate) acquirePriority(ctx context.Context, p priority) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if !g.active {
		g.active = true
		g.mu.Unlock()
		return g.release, nil
	}
	if len(g.urgent)+len(g.live)+len(g.background) >= 8 {
		g.mu.Unlock()
		return nil, errors.New("Local inference queue is full; try again at the next song boundary")
	}
	t := &ticket{ready: make(chan struct{})}
	if p == linkPriority {
		g.urgent = append(g.urgent, t)
	} else if p == livePriority {
		g.live = append(g.live, t)
	} else {
		g.background = append(g.background, t)
	}
	g.mu.Unlock()
	select {
	case <-t.ready:
		return g.release, nil
	case <-ctx.Done():
		g.mu.Lock()
		granted := t.granted
		for _, q := range []*[]*ticket{&g.urgent, &g.live, &g.background} {
			for i, x := range *q {
				if x == t {
					*q = append((*q)[:i], (*q)[i+1:]...)
					break
				}
			}
		}
		g.mu.Unlock()
		if granted {
			g.release()
		}
		return nil, ctx.Err()
	}
}
func (g *Gate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	q := &g.urgent
	if len(*q) == 0 {
		q = &g.live
	}
	if len(*q) == 0 {
		q = &g.background
	}
	if len(*q) == 0 {
		g.active = false
		return
	}
	t := (*q)[0]
	*q = (*q)[1:]
	t.granted = true
	close(t.ready)
}
