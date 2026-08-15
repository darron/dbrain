package remote

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockingRuntimeOwner struct {
	release <-chan struct{}
	events  *[]string
	mu      *sync.Mutex
}

func (o blockingRuntimeOwner) Shutdown(ctx context.Context) error {
	select {
	case <-o.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o blockingRuntimeOwner) Close() error {
	<-o.release
	o.mu.Lock()
	*o.events = append(*o.events, "runtime-close")
	o.mu.Unlock()
	return nil
}

func TestCloseOwnedStoreKeepsStoreAliveUntilRuntimeDrain(t *testing.T) {
	release := make(chan struct{})
	events := []string{}
	var mu sync.Mutex
	owner := blockingRuntimeOwner{release: release, events: &events, mu: &mu}
	storeClosed := make(chan struct{})

	err := closeOwnedStore(owner, func() error {
		mu.Lock()
		events = append(events, "store-close")
		mu.Unlock()
		close(storeClosed)
		return nil
	}, 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup error = %v, want shutdown deadline", err)
	}
	select {
	case <-storeClosed:
		t.Fatal("store closed before runtime drain")
	default:
	}

	close(release)
	select {
	case <-storeClosed:
	case <-time.After(time.Second):
		t.Fatal("asynchronous reaper did not close store")
	}
	mu.Lock()
	defer mu.Unlock()
	assertEventOrder(t, events, "runtime-close", "store-close")
}

type errorRuntimeOwner struct{ err error }

func (o errorRuntimeOwner) Shutdown(context.Context) error { return o.err }
func (o errorRuntimeOwner) Close() error                   { return o.err }

func TestCloseOwnedStoreJoinsRuntimeAndStoreErrors(t *testing.T) {
	runtimeErr := errors.New("runtime shutdown failed")
	storeErr := errors.New("store close failed")
	err := closeOwnedStore(errorRuntimeOwner{err: runtimeErr}, func() error { return storeErr }, time.Second)
	if !errors.Is(err, runtimeErr) || !errors.Is(err, storeErr) {
		t.Fatalf("cleanup error = %v, want joined runtime and store errors", err)
	}
}

func TestRunRemoteAsyncCleanupLogsErrors(t *testing.T) {
	serverErr := errors.New("http server cleanup failed")
	ownerErr := errors.New("runtime or store cleanup failed")
	var log bytes.Buffer

	runRemoteAsyncCleanup(
		func() error { return serverErr },
		func() error { return ownerErr },
		&log,
	)

	output := log.String()
	if !strings.Contains(output, "component=http_server") || !strings.Contains(output, serverErr.Error()) {
		t.Fatalf("async cleanup log = %q, missing HTTP server error", output)
	}
	if !strings.Contains(output, "component=runtime_or_store") || !strings.Contains(output, ownerErr.Error()) {
		t.Fatalf("async cleanup log = %q, missing owner error", output)
	}
}
