package semanticruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/semanticindex"
)

var (
	ErrManagerClosed           = errors.New("semantic runtime manager is shut down")
	ErrLeaseClosed             = errors.New("semantic runtime lease is closed")
	ErrRootRetired             = errors.New("semantic runtime root was retired")
	ErrRetainLoadGuardRequired = errors.New("semantic runtime cold load requires a retained generation guard")
)

// LoadWaitTimeoutError reports that a caller stopped waiting for a shared
// load. The manager-owned load may still finish and populate the warm cache.
type LoadWaitTimeoutError struct {
	Duration time.Duration
}

func (e *LoadWaitTimeoutError) Error() string {
	return fmt.Sprintf("semantic runtime root load wait exceeded %s", e.Duration)
}

func (e *LoadWaitTimeoutError) Timeout() bool { return true }

// RootKey is the complete identity of one validated immutable semantic root.
type RootKey struct {
	CacheDir         string
	DatabaseID       string
	ProfileID        string
	GenerationID     string
	SnapshotRevision int64
	PurgeEpoch       int64
	BackendVersion   string
	DescriptorSHA256 string
}

// LoadedSearcher couples a searcher to the callback that releases its native
// resources.
type LoadedSearcher struct {
	Searcher semanticindex.Searcher
	Close    func() error
}

type Loader func(context.Context, RootKey) (LoadedSearcher, error)

// RetainLoadGuard retains the caller's already-acquired shared-generation
// guard for a detached cold load.
type RetainLoadGuard func() (release func() error, err error)

type rootScope struct {
	cacheDir   string
	databaseID string
	profileID  string
}

func (k RootKey) scope() rootScope {
	return rootScope{cacheDir: k.CacheDir, databaseID: k.DatabaseID, profileID: k.ProfileID}
}

type scopeState struct {
	key      RootKey
	sequence uint64
}

type loadFlight struct {
	key      RootKey
	sequence uint64
	done     chan struct{}
	entry    *cacheEntry
	err      error
}

type cacheEntry struct {
	manager *Manager
	key     RootKey
	loaded  LoadedSearcher

	searchMu sync.Mutex
	refs     int
	ops      int
	retired  bool
	closing  bool
}

// Manager owns process-local immutable roots for one runtime/store lifetime.
type Manager struct {
	mu sync.Mutex

	loader          Loader
	loadWaitTimeout time.Duration
	shutdownContext context.Context
	cancelShutdown  context.CancelFunc

	entries    map[RootKey]*cacheEntry
	allEntries map[*cacheEntry]struct{}
	flights    map[RootKey]*loadFlight
	scopes     map[rootScope]scopeState
	sequence   uint64
	loads      int

	shutdown    bool
	drained     chan struct{}
	drainedDone bool
	closeErrors []error
}

func New(loader Loader, loadWaitTimeout time.Duration) *Manager {
	shutdownContext, cancelShutdown := context.WithCancel(context.Background())
	return &Manager{
		loader:          loader,
		loadWaitTimeout: loadWaitTimeout,
		shutdownContext: shutdownContext,
		cancelShutdown:  cancelShutdown,
		entries:         make(map[RootKey]*cacheEntry),
		allEntries:      make(map[*cacheEntry]struct{}),
		flights:         make(map[RootKey]*loadFlight),
		scopes:          make(map[rootScope]scopeState),
		drained:         make(chan struct{}),
	}
}

func (m *Manager) Acquire(ctx context.Context, key RootKey, retain RetainLoadGuard) (*Lease, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrManagerClosed
	}

	m.mu.Lock()
	if m.shutdown {
		m.mu.Unlock()
		return nil, ErrManagerClosed
	}

	scope := key.scope()
	state, current := m.scopes[scope]
	if !current || state.key != key {
		m.sequence++
		state = scopeState{key: key, sequence: m.sequence}
		m.scopes[scope] = state
		m.retireOtherEntriesLocked(scope, key)
	}

	flight := m.flights[key]
	if flight != nil && flight.sequence == state.sequence {
		m.mu.Unlock()
		return m.waitForFlight(ctx, flight)
	}

	if entry := m.entries[key]; entry != nil && !entry.retired && !entry.closing {
		entry.refs++
		m.mu.Unlock()
		return newLease(entry), nil
	}

	if m.loader == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("semantic runtime loader is nil")
	}
	if retain == nil {
		m.mu.Unlock()
		return nil, ErrRetainLoadGuardRequired
	}
	flight = &loadFlight{
		key:      key,
		sequence: state.sequence,
		done:     make(chan struct{}),
	}
	m.flights[key] = flight
	m.loads++
	m.mu.Unlock()

	release, err := retain()
	if err != nil {
		m.finishFlight(flight, fmt.Errorf("retain semantic runtime load guard: %w", err))
		return m.waitForFlight(ctx, flight)
	}
	if release == nil {
		m.finishFlight(flight, errors.New("retain semantic runtime load guard: release callback is nil"))
		return m.waitForFlight(ctx, flight)
	}

	m.mu.Lock()
	shutdown := m.shutdown
	currentFlight := m.flights[key] == flight
	m.mu.Unlock()
	if shutdown || !currentFlight {
		dispositionErr := ErrRootRetired
		if shutdown {
			dispositionErr = ErrManagerClosed
		}
		m.finishFlight(flight, errors.Join(dispositionErr, release()))
		return m.waitForFlight(ctx, flight)
	}

	go m.runLoad(flight, release)
	return m.waitForFlight(ctx, flight)
}

func (m *Manager) waitForFlight(ctx context.Context, flight *loadFlight) (*Lease, error) {
	if m.loadWaitTimeout <= 0 {
		select {
		case <-flight.done:
			return m.acquireFlightEntry(flight)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	timer := time.NewTimer(m.loadWaitTimeout)
	defer timer.Stop()
	select {
	case <-flight.done:
		return m.acquireFlightEntry(flight)
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, &LoadWaitTimeoutError{Duration: m.loadWaitTimeout}
	}
}

func (m *Manager) acquireFlightEntry(flight *loadFlight) (*Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if flight.err != nil {
		return nil, flight.err
	}
	if m.shutdown {
		return nil, ErrManagerClosed
	}
	entry := flight.entry
	if entry == nil || entry.retired || entry.closing || m.entries[flight.key] != entry {
		return nil, ErrRootRetired
	}
	entry.refs++
	return newLease(entry), nil
}

func (m *Manager) runLoad(flight *loadFlight, release func() error) {
	loaded, loadErr := m.loader(m.shutdownContext, flight.key)
	if loadErr == nil && loaded.Searcher == nil {
		loadErr = errors.New("semantic runtime loader returned a nil searcher")
	}
	if loadErr == nil && loaded.Close == nil {
		loadErr = errors.New("semantic runtime loader returned a nil close callback")
	}

	var entry *cacheEntry
	var dispositionErr error
	if loadErr == nil {
		m.mu.Lock()
		state := m.scopes[flight.key.scope()]
		currentFlight := m.flights[flight.key] == flight
		switch {
		case m.shutdown:
			dispositionErr = ErrManagerClosed
		case !currentFlight || state.key != flight.key || state.sequence != flight.sequence:
			dispositionErr = ErrRootRetired
		default:
			entry = &cacheEntry{manager: m, key: flight.key, loaded: loaded}
			m.entries[flight.key] = entry
			m.allEntries[entry] = struct{}{}
			flight.entry = entry
		}
		m.mu.Unlock()
	}

	var discardCloseErr error
	if entry == nil && loaded.Close != nil {
		discardCloseErr = loaded.Close()
		m.recordCloseError(discardCloseErr)
	}
	releaseErr := release()
	if releaseErr != nil && entry != nil {
		m.mu.Lock()
		if m.entries[entry.key] == entry {
			delete(m.entries, entry.key)
		}
		entry.retired = true
		flight.entry = nil
		m.scheduleEntryCloseLocked(entry)
		m.mu.Unlock()
	}

	flightErr := errors.Join(loadErr, dispositionErr, discardCloseErr, releaseErr)
	m.finishFlight(flight, flightErr)
}

func (m *Manager) finishFlight(flight *loadFlight, err error) {
	m.mu.Lock()
	flight.err = err
	if m.flights[flight.key] == flight {
		delete(m.flights, flight.key)
	}
	m.loads--
	close(flight.done)
	m.checkDrainedLocked()
	m.mu.Unlock()
}

func (m *Manager) retireOtherEntriesLocked(scope rootScope, current RootKey) {
	for key, entry := range m.entries {
		if key != current && key.scope() == scope {
			delete(m.entries, key)
			entry.retired = true
			m.scheduleEntryCloseLocked(entry)
		}
	}
}

func (m *Manager) scheduleEntryCloseLocked(entry *cacheEntry) {
	if entry == nil || entry.closing || !entry.retired || entry.refs != 0 || entry.ops != 0 {
		return
	}
	entry.closing = true
	if m.entries[entry.key] == entry {
		delete(m.entries, entry.key)
	}
	go m.closeEntry(entry)
}

func (m *Manager) closeEntry(entry *cacheEntry) {
	err := entry.loaded.Close()
	m.mu.Lock()
	if err != nil {
		m.closeErrors = append(m.closeErrors, err)
	}
	delete(m.allEntries, entry)
	m.checkDrainedLocked()
	m.mu.Unlock()
}

func (m *Manager) recordCloseError(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	m.closeErrors = append(m.closeErrors, err)
	m.mu.Unlock()
}

func (m *Manager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if m == nil {
		return nil
	}

	m.mu.Lock()
	if !m.shutdown {
		m.shutdown = true
		m.cancelShutdown()
		for key, entry := range m.entries {
			delete(m.entries, key)
			entry.retired = true
			m.scheduleEntryCloseLocked(entry)
		}
		m.checkDrainedLocked()
	}
	drained := m.drained
	m.mu.Unlock()

	select {
	case <-drained:
		return m.joinedCloseErrors()
	default:
	}

	select {
	case <-drained:
		return m.joinedCloseErrors()
	case <-ctx.Done():
		return errors.Join(ctx.Err(), m.joinedCloseErrors())
	}
}

func (m *Manager) Close() error {
	return m.Shutdown(context.Background())
}

func (m *Manager) checkDrainedLocked() {
	if m.shutdown && m.loads == 0 && len(m.allEntries) == 0 && !m.drainedDone {
		m.drainedDone = true
		close(m.drained)
	}
}

func (m *Manager) joinedCloseErrors() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return errors.Join(m.closeErrors...)
}

// Lease is one reference to an immutable cached root.
type Lease struct {
	mu sync.Mutex

	entry      *cacheEntry
	closed     bool
	operations int
	released   bool
}

func newLease(entry *cacheEntry) *Lease {
	return &Lease{entry: entry}
}

func (l *Lease) Search(ctx context.Context, query []float32, options semanticindex.SearchOptions) ([]semanticindex.Hit, semanticindex.Status, error) {
	if l == nil {
		return nil, semanticindex.Status{}, ErrLeaseClosed
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, semanticindex.Status{}, ErrLeaseClosed
	}
	entry := l.entry
	manager := entry.manager
	manager.mu.Lock()
	if entry.closing {
		manager.mu.Unlock()
		l.mu.Unlock()
		return nil, semanticindex.Status{}, ErrLeaseClosed
	}
	l.operations++
	entry.ops++
	manager.mu.Unlock()
	l.mu.Unlock()

	defer l.finishOperation()
	entry.searchMu.Lock()
	defer entry.searchMu.Unlock()
	return entry.loaded.Searcher.Search(ctx, query, options)
}

func (l *Lease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.operations == 0 {
		l.releaseEntryLocked()
	}
	return nil
}

func (l *Lease) finishOperation() {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entry
	manager := entry.manager
	manager.mu.Lock()
	l.operations--
	entry.ops--
	if l.closed && l.operations == 0 && !l.released {
		l.released = true
		entry.refs--
	}
	manager.scheduleEntryCloseLocked(entry)
	manager.checkDrainedLocked()
	manager.mu.Unlock()
}

// releaseEntryLocked drops the lease's entry reference. l.mu must be held.
func (l *Lease) releaseEntryLocked() {
	if l.released {
		return
	}
	l.released = true
	entry := l.entry
	manager := entry.manager
	manager.mu.Lock()
	entry.refs--
	manager.scheduleEntryCloseLocked(entry)
	manager.checkDrainedLocked()
	manager.mu.Unlock()
}
