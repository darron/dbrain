package runlock

import (
	"context"
	"sync"
)

var processGates sync.Map

type localGate struct {
	mu            sync.Mutex
	activeReaders int
	activeWriter  bool
	waiters       []*localWaiter
}

type localWaiter struct {
	mode    Mode
	ready   chan struct{}
	granted bool
}

type localLease struct {
	gate *localGate
	mode Mode
	once sync.Once
}

func gateFor(path string) *localGate {
	gate, _ := processGates.LoadOrStore(path, &localGate{})
	return gate.(*localGate)
}

func (g *localGate) tryAcquire(mode Mode) (*localLease, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.waiters) != 0 || !g.canGrant(mode) {
		return nil, false
	}
	g.markGranted(mode)
	return &localLease{gate: g, mode: mode}, true
}

func (g *localGate) acquire(ctx context.Context, mode Mode) (*localLease, error) {
	g.mu.Lock()
	if len(g.waiters) == 0 && g.canGrant(mode) {
		g.markGranted(mode)
		g.mu.Unlock()
		return &localLease{gate: g, mode: mode}, nil
	}
	waiter := &localWaiter{mode: mode, ready: make(chan struct{})}
	g.waiters = append(g.waiters, waiter)
	g.grantWaiters()
	g.mu.Unlock()

	select {
	case <-waiter.ready:
		return &localLease{gate: g, mode: mode}, nil
	case <-ctx.Done():
		g.mu.Lock()
		if waiter.granted {
			g.markReleased(mode)
			g.grantWaiters()
			g.mu.Unlock()
			return nil, ctx.Err()
		}
		for index, queued := range g.waiters {
			if queued == waiter {
				g.waiters = append(g.waiters[:index], g.waiters[index+1:]...)
				break
			}
		}
		g.grantWaiters()
		g.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (g *localGate) canGrant(mode Mode) bool {
	if mode == Shared {
		return !g.activeWriter
	}
	return !g.activeWriter && g.activeReaders == 0
}

func (g *localGate) markGranted(mode Mode) {
	if mode == Shared {
		g.activeReaders++
		return
	}
	g.activeWriter = true
}

func (g *localGate) markReleased(mode Mode) {
	if mode == Shared {
		g.activeReaders--
		return
	}
	g.activeWriter = false
}

func (g *localGate) grantWaiters() {
	for len(g.waiters) != 0 {
		waiter := g.waiters[0]
		if !g.canGrant(waiter.mode) {
			return
		}
		g.waiters = g.waiters[1:]
		g.markGranted(waiter.mode)
		waiter.granted = true
		close(waiter.ready)
		if waiter.mode == Exclusive {
			return
		}
	}
}

func (l *localLease) release() {
	if l == nil || l.gate == nil {
		return
	}
	l.once.Do(func() {
		l.gate.mu.Lock()
		l.gate.markReleased(l.mode)
		l.gate.grantWaiters()
		l.gate.mu.Unlock()
	})
}
