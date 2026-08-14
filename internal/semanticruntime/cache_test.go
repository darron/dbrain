package semanticruntime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/semanticindex"
)

const testWait = 2 * time.Second

type fakeSearcher struct {
	search func(context.Context, []float32, semanticindex.SearchOptions) ([]semanticindex.Hit, semanticindex.Status, error)
}

func (s *fakeSearcher) Search(ctx context.Context, query []float32, options semanticindex.SearchOptions) ([]semanticindex.Hit, semanticindex.Status, error) {
	if s.search != nil {
		return s.search(ctx, query, options)
	}
	return []semanticindex.Hit{{ChunkID: "chunk-1"}}, semanticindex.Status{State: semanticindex.StateSearched}, nil
}

type fakeGuard struct {
	retains   atomic.Int32
	releases  atomic.Int32
	released  chan struct{}
	onRelease func() error
}

func newFakeGuard() *fakeGuard {
	return &fakeGuard{released: make(chan struct{})}
}

func (g *fakeGuard) Retain() (func() error, error) {
	g.retains.Add(1)
	var once sync.Once
	return func() error {
		var err error
		once.Do(func() {
			g.releases.Add(1)
			close(g.released)
			if g.onRelease != nil {
				err = g.onRelease()
			}
		})
		return err
	}, nil
}

func testRootKey(generation string) RootKey {
	return RootKey{
		CacheDir:         "/cache",
		DatabaseID:       "database",
		ProfileID:        "profile",
		GenerationID:     generation,
		SnapshotRevision: 17,
		PurgeEpoch:       3,
		BackendVersion:   "2.26.0",
		DescriptorSHA256: generation + "-descriptor",
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(testWait):
		t.Fatal(message)
	}
}

func assertNotClosed(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal(message)
	case <-time.After(25 * time.Millisecond):
	}
}

func TestManagerSharesIdenticalRoot(t *testing.T) {
	var loads atomic.Int32
	closed := make(chan struct{})
	manager := New(func(context.Context, RootKey) (LoadedSearcher, error) {
		loads.Add(1)
		return LoadedSearcher{
			Searcher: &fakeSearcher{},
			Close: func() error {
				close(closed)
				return nil
			},
		}, nil
	}, time.Second)
	guard := newFakeGuard()

	first, err := manager.Acquire(context.Background(), testRootKey("generation-1"), guard.Retain)
	if err != nil {
		t.Fatalf("Acquire first lease: %v", err)
	}
	second, err := manager.Acquire(context.Background(), testRootKey("generation-1"), func() (func() error, error) {
		t.Fatal("warm acquisition retained a second generation guard")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Acquire second lease: %v", err)
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("loader calls = %d, want 1", got)
	}
	if got := guard.retains.Load(); got != 1 {
		t.Fatalf("guard retains = %d, want 1", got)
	}
	if got := guard.releases.Load(); got != 1 {
		t.Fatalf("guard releases = %d, want 1", got)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("Close first lease: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close second lease: %v", err)
	}
	assertNotClosed(t, closed, "warm cache entry closed before retirement")
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	waitClosed(t, closed, "shutdown did not close the cached searcher")
}

func TestManagerSingleFlightsConcurrentMisses(t *testing.T) {
	const callers = 12
	var loads atomic.Int32
	started := make(chan struct{})
	unblock := make(chan struct{})
	manager := New(func(context.Context, RootKey) (LoadedSearcher, error) {
		if loads.Add(1) == 1 {
			close(started)
		}
		<-unblock
		return LoadedSearcher{Searcher: &fakeSearcher{}, Close: func() error { return nil }}, nil
	}, time.Second)
	guard := newFakeGuard()
	start := make(chan struct{})
	results := make(chan *Lease, callers)
	errs := make(chan error, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	for range callers {
		go func() {
			ready.Done()
			<-start
			lease, err := manager.Acquire(context.Background(), testRootKey("generation-1"), guard.Retain)
			results <- lease
			errs <- err
		}()
	}
	ready.Wait()
	close(start)
	waitClosed(t, started, "loader did not start")
	time.Sleep(25 * time.Millisecond)
	close(unblock)

	for range callers {
		if err := <-errs; err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		lease := <-results
		if lease == nil {
			t.Fatal("Acquire returned a nil lease")
		}
		if err := lease.Close(); err != nil {
			t.Fatalf("Close lease: %v", err)
		}
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("loader calls = %d, want 1", got)
	}
	if got := guard.retains.Load(); got != 1 {
		t.Fatalf("guard retains = %d, want 1", got)
	}
	if got := guard.releases.Load(); got != 1 {
		t.Fatalf("guard releases = %d, want 1", got)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestManagerWaiterCancellationDoesNotCancelSharedLoad(t *testing.T) {
	loaderStarted := make(chan struct{})
	loaderFinished := make(chan struct{})
	loaderContextCanceled := make(chan struct{})
	unblock := make(chan struct{})
	manager := New(func(ctx context.Context, _ RootKey) (LoadedSearcher, error) {
		close(loaderStarted)
		go func() {
			<-ctx.Done()
			close(loaderContextCanceled)
		}()
		<-unblock
		close(loaderFinished)
		return LoadedSearcher{Searcher: &fakeSearcher{}, Close: func() error { return nil }}, nil
	}, time.Second)
	guard := newFakeGuard()
	callerContext, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := manager.Acquire(callerContext, testRootKey("generation-1"), guard.Retain)
		result <- err
	}()
	waitClosed(t, loaderStarted, "loader did not start")
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire error = %v, want context.Canceled", err)
	}
	assertNotClosed(t, loaderContextCanceled, "caller cancellation canceled manager-owned load context")
	close(unblock)
	waitClosed(t, loaderFinished, "loader did not finish")
	waitClosed(t, guard.released, "generation guard was not released")

	lease, err := manager.Acquire(context.Background(), testRootKey("generation-1"), nil)
	if err != nil {
		t.Fatalf("Acquire warm lease: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("Close lease: %v", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	waitClosed(t, loaderContextCanceled, "shutdown did not cancel manager load context")
}

func TestManagerLoadWaitTimeoutDoesNotCancelSharedLoad(t *testing.T) {
	loaderStarted := make(chan struct{})
	loaderContextCanceled := make(chan struct{})
	unblock := make(chan struct{})
	manager := New(func(ctx context.Context, _ RootKey) (LoadedSearcher, error) {
		close(loaderStarted)
		go func() {
			<-ctx.Done()
			close(loaderContextCanceled)
		}()
		<-unblock
		return LoadedSearcher{Searcher: &fakeSearcher{}, Close: func() error { return nil }}, nil
	}, 20*time.Millisecond)
	guard := newFakeGuard()

	_, err := manager.Acquire(context.Background(), testRootKey("generation-1"), guard.Retain)
	var timeoutErr *LoadWaitTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Acquire error = %T %v, want *LoadWaitTimeoutError", err, err)
	}
	waitClosed(t, loaderStarted, "loader did not start")
	assertNotClosed(t, loaderContextCanceled, "load wait timeout canceled manager-owned load context")
	close(unblock)
	waitClosed(t, guard.released, "generation guard was not released")

	lease, err := manager.Acquire(context.Background(), testRootKey("generation-1"), nil)
	if err != nil {
		t.Fatalf("Acquire warm lease after timed-out load: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("Close lease: %v", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestManagerRetainsGenerationGuardUntilDetachedLoadFinishes(t *testing.T) {
	loaderStarted := make(chan struct{})
	unblock := make(chan struct{})
	manager := New(func(context.Context, RootKey) (LoadedSearcher, error) {
		close(loaderStarted)
		<-unblock
		return LoadedSearcher{Searcher: &fakeSearcher{}, Close: func() error { return nil }}, nil
	}, time.Second)
	guard := newFakeGuard()
	result := make(chan error, 1)
	var lease *Lease
	go func() {
		var err error
		lease, err = manager.Acquire(context.Background(), testRootKey("generation-1"), guard.Retain)
		result <- err
	}()
	waitClosed(t, loaderStarted, "loader did not start")
	if got := guard.retains.Load(); got != 1 {
		t.Fatalf("guard retains while loading = %d, want 1", got)
	}
	if got := guard.releases.Load(); got != 0 {
		t.Fatalf("guard releases while loading = %d, want 0", got)
	}
	close(unblock)
	if err := <-result; err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := guard.releases.Load(); got != 1 {
		t.Fatalf("guard releases after load = %d, want 1", got)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("Close lease: %v", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestManagerDoesNotExposeRootUntilGuardReleaseFinishes(t *testing.T) {
	releaseStarted := make(chan struct{})
	allowRelease := make(chan struct{})
	var unblockRelease sync.Once
	unblock := func() { unblockRelease.Do(func() { close(allowRelease) }) }
	t.Cleanup(unblock)
	manager := New(func(context.Context, RootKey) (LoadedSearcher, error) {
		return LoadedSearcher{Searcher: &fakeSearcher{}, Close: func() error { return nil }}, nil
	}, time.Second)
	retain := func() (func() error, error) {
		return func() error {
			close(releaseStarted)
			<-allowRelease
			return nil
		}, nil
	}
	type acquireResult struct {
		lease *Lease
		err   error
	}
	firstResult := make(chan acquireResult, 1)
	go func() {
		lease, err := manager.Acquire(context.Background(), testRootKey("generation-1"), retain)
		firstResult <- acquireResult{lease: lease, err: err}
	}()
	waitClosed(t, releaseStarted, "guard release did not start")
	manager.mu.Lock()
	entry, published := manager.entries[testRootKey("generation-1")]
	flight := manager.flights[testRootKey("generation-1")]
	manager.mu.Unlock()
	if !published || entry == nil || !entry.pending {
		t.Fatal("root was not tracked as a pending candidate before retained guard release finished")
	}
	if flight == nil {
		t.Fatal("cold load flight disappeared before retained guard release finished")
	}

	secondResult := make(chan acquireResult, 1)
	go func() {
		lease, err := manager.Acquire(context.Background(), testRootKey("generation-1"), nil)
		secondResult <- acquireResult{lease: lease, err: err}
	}()
	select {
	case result := <-secondResult:
		if result.lease != nil {
			_ = result.lease.Close()
		}
		t.Fatalf("warm acquisition returned before guard release finished: %v", result.err)
	case <-time.After(25 * time.Millisecond):
	}

	unblock()
	first := <-firstResult
	if first.err != nil {
		t.Fatalf("Acquire first lease: %v", first.err)
	}
	second := <-secondResult
	if second.err != nil {
		t.Fatalf("Acquire second lease: %v", second.err)
	}
	if err := first.lease.Close(); err != nil {
		t.Fatalf("Close first lease: %v", err)
	}
	if err := second.lease.Close(); err != nil {
		t.Fatalf("Close second lease: %v", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestLeaseSearchRejectsRetiredRoot(t *testing.T) {
	manager := New(func(context.Context, RootKey) (LoadedSearcher, error) {
		return LoadedSearcher{Searcher: &fakeSearcher{}, Close: func() error { return nil }}, nil
	}, time.Second)
	oldGuard := newFakeGuard()
	newGuard := newFakeGuard()
	oldLease, err := manager.Acquire(context.Background(), testRootKey("generation-1"), oldGuard.Retain)
	if err != nil {
		t.Fatalf("Acquire old lease: %v", err)
	}
	newLease, err := manager.Acquire(context.Background(), testRootKey("generation-2"), newGuard.Retain)
	if err != nil {
		t.Fatalf("Acquire new lease: %v", err)
	}

	_, _, err = oldLease.Search(context.Background(), nil, semanticindex.SearchOptions{})
	if !errors.Is(err, ErrRootRetired) {
		t.Fatalf("retired Search error = %v, want ErrRootRetired", err)
	}
	if err := oldLease.Close(); err != nil {
		t.Fatalf("Close old lease: %v", err)
	}
	if err := newLease.Close(); err != nil {
		t.Fatalf("Close new lease: %v", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestLeaseSearchRejectsAfterShutdown(t *testing.T) {
	manager := New(func(context.Context, RootKey) (LoadedSearcher, error) {
		return LoadedSearcher{Searcher: &fakeSearcher{}, Close: func() error { return nil }}, nil
	}, time.Second)
	lease, err := manager.Acquire(context.Background(), testRootKey("generation-1"), newFakeGuard().Retain)
	if err != nil {
		t.Fatalf("Acquire lease: %v", err)
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := manager.Shutdown(shutdownContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want context.DeadlineExceeded", err)
	}

	_, _, err = lease.Search(context.Background(), nil, semanticindex.SearchOptions{})
	if !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("post-shutdown Search error = %v, want ErrManagerClosed", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("Close lease: %v", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("final Shutdown: %v", err)
	}
}

func TestManagerWaitsForDiscardedSearcherCloseBeforeFlightCompletes(t *testing.T) {
	releaseErr := errors.New("release retained generation guard")
	closeStarted := make(chan struct{})
	allowClose := make(chan struct{})
	manager := New(func(context.Context, RootKey) (LoadedSearcher, error) {
		return LoadedSearcher{
			Searcher: &fakeSearcher{},
			Close: func() error {
				close(closeStarted)
				<-allowClose
				return nil
			},
		}, nil
	}, time.Second)
	result := make(chan error, 1)
	go func() {
		_, err := manager.Acquire(context.Background(), testRootKey("generation-1"), func() (func() error, error) {
			return func() error { return releaseErr }, nil
		})
		result <- err
	}()
	waitClosed(t, closeStarted, "discarded searcher close did not start")
	manager.mu.Lock()
	flight := manager.flights[testRootKey("generation-1")]
	manager.mu.Unlock()
	if flight == nil {
		t.Fatal("load flight completed before discarded searcher close finished")
	}
	close(allowClose)
	if err := <-result; !errors.Is(err, releaseErr) {
		t.Fatalf("Acquire error = %v, want release error", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestManagerDiscardsPublishedRootWhenGuardReleaseFails(t *testing.T) {
	releaseErr := errors.New("release retained generation guard")
	closed := make(chan struct{}, 2)
	var loads atomic.Int32
	manager := New(func(context.Context, RootKey) (LoadedSearcher, error) {
		loads.Add(1)
		return LoadedSearcher{
			Searcher: &fakeSearcher{},
			Close: func() error {
				closed <- struct{}{}
				return nil
			},
		}, nil
	}, time.Second)
	_, err := manager.Acquire(context.Background(), testRootKey("generation-1"), func() (func() error, error) {
		return func() error { return releaseErr }, nil
	})
	if !errors.Is(err, releaseErr) {
		t.Fatalf("Acquire error = %v, want guard release error", err)
	}
	waitClosed(t, closed, "searcher published before guard release failure was not discarded")

	lease, err := manager.Acquire(context.Background(), testRootKey("generation-1"), newFakeGuard().Retain)
	if err != nil {
		t.Fatalf("Acquire after guard release failure: %v", err)
	}
	if got := loads.Load(); got != 2 {
		t.Fatalf("loader calls = %d, want 2 after failed guard release", got)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("Close lease: %v", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestManagerShutdownDeadlineIsNotBlockedByRetainGuard(t *testing.T) {
	retainStarted := make(chan struct{})
	allowRetain := make(chan struct{})
	var unblockRetain sync.Once
	unblock := func() { unblockRetain.Do(func() { close(allowRetain) }) }
	t.Cleanup(unblock)
	manager := New(func(ctx context.Context, _ RootKey) (LoadedSearcher, error) {
		return LoadedSearcher{}, ctx.Err()
	}, time.Second)
	acquireDone := make(chan error, 1)
	go func() {
		_, err := manager.Acquire(context.Background(), testRootKey("generation-1"), func() (func() error, error) {
			close(retainStarted)
			<-allowRetain
			return func() error { return nil }, nil
		})
		acquireDone <- err
	}()
	waitClosed(t, retainStarted, "retain guard did not start")

	shutdownContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- manager.Shutdown(shutdownContext) }()
	select {
	case err := <-shutdownDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Shutdown error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(100 * time.Millisecond):
		unblock()
		<-shutdownDone
		t.Fatal("Shutdown deadline was blocked by RetainLoadGuard")
	}

	unblock()
	if err := <-acquireDone; !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Acquire error = %v, want ErrManagerClosed", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestManagerRetiresOlderGenerationAfterLastLease(t *testing.T) {
	firstClosed := make(chan struct{})
	manager := New(func(_ context.Context, key RootKey) (LoadedSearcher, error) {
		closeFn := func() error { return nil }
		if key.GenerationID == "generation-1" {
			closeFn = func() error { close(firstClosed); return nil }
		}
		return LoadedSearcher{Searcher: &fakeSearcher{}, Close: closeFn}, nil
	}, time.Second)
	first, err := manager.Acquire(context.Background(), testRootKey("generation-1"), newFakeGuard().Retain)
	if err != nil {
		t.Fatalf("Acquire first generation: %v", err)
	}
	second, err := manager.Acquire(context.Background(), testRootKey("generation-2"), newFakeGuard().Retain)
	if err != nil {
		t.Fatalf("Acquire second generation: %v", err)
	}
	assertNotClosed(t, firstClosed, "retired searcher closed while its lease remained open")
	if err := first.Close(); err != nil {
		t.Fatalf("Close first lease: %v", err)
	}
	waitClosed(t, firstClosed, "retired searcher did not close after its final lease")
	if err := second.Close(); err != nil {
		t.Fatalf("Close second lease: %v", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestManagerDiscardsOlderLoadThatFinishesAfterNewerRoot(t *testing.T) {
	firstStarted := make(chan struct{})
	finishFirst := make(chan struct{})
	firstClosed := make(chan struct{})
	var firstLoads atomic.Int32
	var secondLoads atomic.Int32
	manager := New(func(_ context.Context, key RootKey) (LoadedSearcher, error) {
		if key.GenerationID == "generation-1" {
			firstLoads.Add(1)
			close(firstStarted)
			<-finishFirst
			return LoadedSearcher{
				Searcher: &fakeSearcher{},
				Close: func() error {
					close(firstClosed)
					return nil
				},
			}, nil
		}
		secondLoads.Add(1)
		return LoadedSearcher{Searcher: &fakeSearcher{}, Close: func() error { return nil }}, nil
	}, time.Second)
	firstResult := make(chan error, 1)
	go func() {
		_, err := manager.Acquire(context.Background(), testRootKey("generation-1"), newFakeGuard().Retain)
		firstResult <- err
	}()
	waitClosed(t, firstStarted, "older load did not start")

	second, err := manager.Acquire(context.Background(), testRootKey("generation-2"), newFakeGuard().Retain)
	if err != nil {
		t.Fatalf("Acquire newer root: %v", err)
	}
	close(finishFirst)
	if err := <-firstResult; !errors.Is(err, ErrRootRetired) {
		t.Fatalf("older Acquire error = %v, want ErrRootRetired", err)
	}
	waitClosed(t, firstClosed, "older out-of-order load was not discarded")

	warm, err := manager.Acquire(context.Background(), testRootKey("generation-2"), nil)
	if err != nil {
		t.Fatalf("Acquire warm newer root: %v", err)
	}
	if got := firstLoads.Load(); got != 1 {
		t.Fatalf("older loader calls = %d, want 1", got)
	}
	if got := secondLoads.Load(); got != 1 {
		t.Fatalf("newer loader calls = %d, want 1", got)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close newer lease: %v", err)
	}
	if err := warm.Close(); err != nil {
		t.Fatalf("Close warm lease: %v", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestManagerRejectsAcquireAfterShutdown(t *testing.T) {
	var loads atomic.Int32
	manager := New(func(context.Context, RootKey) (LoadedSearcher, error) {
		loads.Add(1)
		return LoadedSearcher{Searcher: &fakeSearcher{}, Close: func() error { return nil }}, nil
	}, time.Second)
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	_, err := manager.Acquire(context.Background(), testRootKey("generation-1"), newFakeGuard().Retain)
	if !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Acquire error = %v, want ErrManagerClosed", err)
	}
	if got := loads.Load(); got != 0 {
		t.Fatalf("loader calls after shutdown = %d, want 0", got)
	}
}

func TestLeaseCloseConcurrentWithSearchDefersUnderlyingClose(t *testing.T) {
	searchStarted := make(chan struct{})
	unblockSearch := make(chan struct{})
	underlyingClosed := make(chan struct{})
	searcher := &fakeSearcher{search: func(context.Context, []float32, semanticindex.SearchOptions) ([]semanticindex.Hit, semanticindex.Status, error) {
		close(searchStarted)
		<-unblockSearch
		return nil, semanticindex.Status{State: semanticindex.StateSearched}, nil
	}}
	manager := New(func(context.Context, RootKey) (LoadedSearcher, error) {
		return LoadedSearcher{Searcher: searcher, Close: func() error { close(underlyingClosed); return nil }}, nil
	}, time.Second)
	lease, err := manager.Acquire(context.Background(), testRootKey("generation-1"), newFakeGuard().Retain)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	searchDone := make(chan error, 1)
	go func() {
		_, _, err := lease.Search(context.Background(), nil, semanticindex.SearchOptions{})
		searchDone <- err
	}()
	waitClosed(t, searchStarted, "search did not start")
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- manager.Shutdown(context.Background()) }()
	if err := lease.Close(); err != nil {
		t.Fatalf("Close lease: %v", err)
	}
	if _, _, err := lease.Search(context.Background(), nil, semanticindex.SearchOptions{}); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("Search after Close error = %v, want ErrLeaseClosed", err)
	}
	assertNotClosed(t, underlyingClosed, "underlying searcher closed during an active search")
	close(unblockSearch)
	if err := <-searchDone; err != nil {
		t.Fatalf("Search: %v", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	waitClosed(t, underlyingClosed, "underlying searcher did not close after active search returned")
}

func TestRetirementConcurrentWithSearchClosesAfterSearch(t *testing.T) {
	searchStarted := make(chan struct{})
	unblockSearch := make(chan struct{})
	firstClosed := make(chan struct{})
	manager := New(func(_ context.Context, key RootKey) (LoadedSearcher, error) {
		searcher := &fakeSearcher{}
		closeFn := func() error { return nil }
		if key.GenerationID == "generation-1" {
			searcher.search = func(context.Context, []float32, semanticindex.SearchOptions) ([]semanticindex.Hit, semanticindex.Status, error) {
				close(searchStarted)
				<-unblockSearch
				return nil, semanticindex.Status{State: semanticindex.StateSearched}, nil
			}
			closeFn = func() error { close(firstClosed); return nil }
		}
		return LoadedSearcher{Searcher: searcher, Close: closeFn}, nil
	}, time.Second)
	first, err := manager.Acquire(context.Background(), testRootKey("generation-1"), newFakeGuard().Retain)
	if err != nil {
		t.Fatalf("Acquire first generation: %v", err)
	}
	searchDone := make(chan error, 1)
	go func() {
		_, _, err := first.Search(context.Background(), nil, semanticindex.SearchOptions{})
		searchDone <- err
	}()
	waitClosed(t, searchStarted, "search did not start")
	second, err := manager.Acquire(context.Background(), testRootKey("generation-2"), newFakeGuard().Retain)
	if err != nil {
		t.Fatalf("Acquire second generation: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close first lease: %v", err)
	}
	assertNotClosed(t, firstClosed, "retirement closed a searcher during an active search")
	close(unblockSearch)
	if err := <-searchDone; err != nil {
		t.Fatalf("Search: %v", err)
	}
	waitClosed(t, firstClosed, "retired searcher did not close after search returned")
	if err := second.Close(); err != nil {
		t.Fatalf("Close second lease: %v", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestManagerShutdownDuringLoadStartsReaper(t *testing.T) {
	loaderStarted := make(chan struct{})
	loaderCanceled := make(chan struct{})
	finishNativeLoad := make(chan struct{})
	discardedClosed := make(chan struct{})
	guard := newFakeGuard()
	guard.onRelease = func() error {
		select {
		case <-discardedClosed:
			return nil
		default:
			return errors.New("guard released before discarded searcher closed")
		}
	}
	manager := New(func(ctx context.Context, _ RootKey) (LoadedSearcher, error) {
		close(loaderStarted)
		go func() {
			<-ctx.Done()
			close(loaderCanceled)
		}()
		<-finishNativeLoad
		return LoadedSearcher{
			Searcher: &fakeSearcher{},
			Close: func() error {
				close(discardedClosed)
				return nil
			},
		}, nil
	}, time.Second)
	acquireDone := make(chan error, 1)
	go func() {
		_, err := manager.Acquire(context.Background(), testRootKey("generation-1"), guard.Retain)
		acquireDone <- err
	}()
	waitClosed(t, loaderStarted, "loader did not start")
	shutdownContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := manager.Shutdown(shutdownContext)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want context.DeadlineExceeded", err)
	}
	waitClosed(t, loaderCanceled, "shutdown did not cancel cooperative loader context")
	assertNotClosed(t, discardedClosed, "shutdown force-closed a non-preemptible load")
	close(finishNativeLoad)
	waitClosed(t, discardedClosed, "reaper did not close searcher returned after shutdown")
	waitClosed(t, guard.released, "reaper did not release retained generation guard")
	if err := <-acquireDone; !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Acquire error = %v, want ErrManagerClosed", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestManagerClosesLoadedSearcherAfterLastLeaseOnShutdown(t *testing.T) {
	underlyingClosed := make(chan struct{})
	manager := New(func(context.Context, RootKey) (LoadedSearcher, error) {
		return LoadedSearcher{
			Searcher: &fakeSearcher{},
			Close: func() error {
				close(underlyingClosed)
				return nil
			},
		}, nil
	}, time.Second)
	lease, err := manager.Acquire(context.Background(), testRootKey("generation-1"), newFakeGuard().Retain)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- manager.Shutdown(context.Background()) }()
	assertNotClosed(t, underlyingClosed, "shutdown closed a searcher with an outstanding lease")
	if err := lease.Close(); err != nil {
		t.Fatalf("Close lease: %v", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	waitClosed(t, underlyingClosed, "shutdown did not close searcher after final lease")
}

func TestManagerShutdownJoinsCloseErrors(t *testing.T) {
	firstErr := errors.New("close first root")
	secondErr := errors.New("close second root")
	manager := New(func(_ context.Context, key RootKey) (LoadedSearcher, error) {
		closeErr := firstErr
		if key.DatabaseID == "database-2" {
			closeErr = secondErr
		}
		return LoadedSearcher{Searcher: &fakeSearcher{}, Close: func() error { return closeErr }}, nil
	}, time.Second)
	firstKey := testRootKey("generation-1")
	secondKey := firstKey
	secondKey.DatabaseID = "database-2"
	first, err := manager.Acquire(context.Background(), firstKey, newFakeGuard().Retain)
	if err != nil {
		t.Fatalf("Acquire first root: %v", err)
	}
	second, err := manager.Acquire(context.Background(), secondKey, newFakeGuard().Retain)
	if err != nil {
		t.Fatalf("Acquire second root: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close first lease: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close second lease: %v", err)
	}
	err = manager.Shutdown(context.Background())
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("Shutdown error = %v, want both close errors", err)
	}
}

func TestManagerSerializesSearchesPerRoot(t *testing.T) {
	firstStarted := make(chan struct{})
	unblockFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	var calls atomic.Int32
	searcher := &fakeSearcher{search: func(context.Context, []float32, semanticindex.SearchOptions) ([]semanticindex.Hit, semanticindex.Status, error) {
		switch calls.Add(1) {
		case 1:
			close(firstStarted)
			<-unblockFirst
		case 2:
			close(secondStarted)
		}
		return nil, semanticindex.Status{State: semanticindex.StateSearched}, nil
	}}
	manager := New(func(context.Context, RootKey) (LoadedSearcher, error) {
		return LoadedSearcher{Searcher: searcher, Close: func() error { return nil }}, nil
	}, time.Second)
	first, err := manager.Acquire(context.Background(), testRootKey("generation-1"), newFakeGuard().Retain)
	if err != nil {
		t.Fatalf("Acquire first lease: %v", err)
	}
	second, err := manager.Acquire(context.Background(), testRootKey("generation-1"), nil)
	if err != nil {
		t.Fatalf("Acquire second lease: %v", err)
	}
	var searches sync.WaitGroup
	searches.Add(2)
	go func() {
		defer searches.Done()
		_, _, _ = first.Search(context.Background(), nil, semanticindex.SearchOptions{})
	}()
	waitClosed(t, firstStarted, "first search did not start")
	go func() {
		defer searches.Done()
		_, _, _ = second.Search(context.Background(), nil, semanticindex.SearchOptions{})
	}()
	assertNotClosed(t, secondStarted, "native searches ran concurrently")
	close(unblockFirst)
	waitClosed(t, secondStarted, "second search did not run after first returned")
	searches.Wait()
	if err := first.Close(); err != nil {
		t.Fatalf("Close first lease: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close second lease: %v", err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
