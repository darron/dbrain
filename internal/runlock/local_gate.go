package runlock

import (
	"context"
	"sync"
)

var processGates = localGateRegistry{
	entries: make(map[string]*localGateEntry),
}

type localGateRegistry struct {
	mu      sync.Mutex
	entries map[string]*localGateEntry
}

type localGateEntry struct {
	gate localGate
	refs int
}

type localGateReference struct {
	registry *localGateRegistry
	path     string
	entry    *localGateEntry
	once     sync.Once
}

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
	gate      *localGate
	mode      Mode
	reference *localGateReference
	once      sync.Once
}

func tryAcquireProcessGate(path string, mode Mode) (*localLease, bool) {
	reference := processGates.reference(path)
	lease, ok := reference.entry.gate.tryAcquire(mode)
	if !ok {
		reference.release()
		return nil, false
	}
	lease.reference = reference
	return lease, true
}

func acquireProcessGate(ctx context.Context, path string, mode Mode) (*localLease, error) {
	reference := processGates.reference(path)
	lease, err := reference.entry.gate.acquire(ctx, mode)
	if err != nil {
		reference.release()
		return nil, err
	}
	lease.reference = reference
	return lease, nil
}

func (r *localGateRegistry) reference(path string) *localGateReference {
	r.mu.Lock()
	entry := r.entries[path]
	if entry == nil {
		entry = &localGateEntry{}
		r.entries[path] = entry
	}
	entry.refs++
	r.mu.Unlock()
	return &localGateReference{registry: r, path: path, entry: entry}
}

func (r *localGateReference) release() {
	if r == nil || r.registry == nil || r.entry == nil {
		return
	}
	r.once.Do(func() {
		r.registry.mu.Lock()
		r.entry.refs--
		if r.entry.refs == 0 && r.registry.entries[r.path] == r.entry {
			delete(r.registry.entries, r.path)
		}
		r.registry.mu.Unlock()
	})
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
		l.reference.release()
	})
}
