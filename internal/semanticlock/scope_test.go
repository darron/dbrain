package semanticlock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestScopeDerivesExactDatabaseLockPaths(t *testing.T) {
	cacheDir := t.TempDir()
	scope, err := NewScope(cacheDir, "database-1")
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	resolvedCache, err := filepath.EvalSymlinks(cacheDir)
	if err != nil {
		t.Fatalf("resolve cache directory: %v", err)
	}

	maintenance, err := scope.AcquireMaintenanceShared(t.Context(), "owner=test\n")
	if err != nil {
		t.Fatalf("AcquireMaintenanceShared: %v", err)
	}
	wantMaintenance := filepath.Join(resolvedCache, "semantic", "database-1", "locks", "maintenance.lock")
	if maintenance.Path() != wantMaintenance {
		_ = maintenance.Close()
		t.Fatalf("maintenance path = %q, want %q", maintenance.Path(), wantMaintenance)
	}
	if maintenance.Family() != FamilyMaintenance || maintenance.Mode() != ModeShared {
		_ = maintenance.Close()
		t.Fatalf("maintenance diagnostics = family %q mode %q", maintenance.Family(), maintenance.Mode())
	}
	if err := maintenance.Close(); err != nil {
		t.Fatalf("Close maintenance: %v", err)
	}

	generation, err := scope.AcquireGenerationShared(t.Context(), "owner=test\n")
	if err != nil {
		t.Fatalf("AcquireGenerationShared: %v", err)
	}
	wantGeneration := filepath.Join(resolvedCache, "semantic", "database-1", "locks", "generation.lock")
	if generation.Path() != wantGeneration {
		_ = generation.Close()
		t.Fatalf("generation path = %q, want %q", generation.Path(), wantGeneration)
	}
	if generation.Family() != FamilyGeneration || generation.Mode() != ModeShared {
		_ = generation.Close()
		t.Fatalf("generation diagnostics = family %q mode %q", generation.Family(), generation.Mode())
	}
	if err := generation.Close(); err != nil {
		t.Fatalf("Close generation: %v", err)
	}
}

func TestScopeRejectsUnsafeDatabaseIdentifiers(t *testing.T) {
	tests := []string{
		"",
		".",
		"..",
		"../database",
		"database/child",
		`database\child`,
		"database id",
		"database.id",
		"database\nid",
		"数据库",
		strings.Repeat("a", 129),
	}
	for _, databaseID := range tests {
		t.Run(databaseID, func(t *testing.T) {
			scope, err := NewScope(t.TempDir(), databaseID)
			if scope != nil {
				t.Fatalf("unsafe database ID %q returned scope", databaseID)
			}
			if !errors.Is(err, ErrInvalidDatabaseID) {
				t.Fatalf("NewScope(%q) error = %v, want ErrInvalidDatabaseID", databaseID, err)
			}
		})
	}
}

func TestScopeRejectsSymlinkDescendantsBeforeCreatingOutsideDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink regression; Windows reparse behavior is covered by cross-compilation")
	}
	tests := []struct {
		name     string
		linkPath func(string) string
	}{
		{
			name: "semantic root",
			linkPath: func(cacheDir string) string {
				return filepath.Join(cacheDir, "semantic")
			},
		},
		{
			name: "database root",
			linkPath: func(cacheDir string) string {
				semanticDir := filepath.Join(cacheDir, "semantic")
				if err := os.Mkdir(semanticDir, 0o755); err != nil {
					t.Fatalf("create semantic directory: %v", err)
				}
				return filepath.Join(semanticDir, "database-1")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cacheDir := t.TempDir()
			outside := t.TempDir()
			sentinelPath := filepath.Join(outside, "sentinel")
			if err := os.WriteFile(sentinelPath, []byte("unchanged"), 0o600); err != nil {
				t.Fatalf("write sentinel: %v", err)
			}
			if err := os.Symlink(outside, test.linkPath(cacheDir)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			scope, err := NewScope(cacheDir, "database-1")
			if err != nil {
				t.Fatalf("NewScope: %v", err)
			}

			lease, err := scope.AcquireMaintenanceShared(t.Context(), "owner=test\n")
			if lease != nil {
				_ = lease.Close()
				t.Fatal("acquired through symlink descendant")
			}
			if err == nil {
				t.Fatal("symlink descendant was not rejected")
			}
			if _, err := os.Stat(filepath.Join(outside, "database-1")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("outside database directory was created: %v", err)
			}
			if _, err := os.Stat(filepath.Join(outside, "locks")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("outside lock directory was created: %v", err)
			}
			sentinel, err := os.ReadFile(sentinelPath)
			if err != nil {
				t.Fatalf("read sentinel: %v", err)
			}
			if string(sentinel) != "unchanged" {
				t.Fatalf("outside sentinel changed to %q", sentinel)
			}
		})
	}
}

func TestScopesForDistinctDatabasesDoNotContend(t *testing.T) {
	cacheDir := t.TempDir()
	firstScope, err := NewScope(cacheDir, "database-1")
	if err != nil {
		t.Fatalf("NewScope first: %v", err)
	}
	secondScope, err := NewScope(cacheDir, "database-2")
	if err != nil {
		t.Fatalf("NewScope second: %v", err)
	}

	first, err := firstScope.AcquireMaintenanceExclusive(t.Context(), "owner=first\n")
	if err != nil {
		t.Fatalf("AcquireMaintenanceExclusive first: %v", err)
	}
	defer func() { _ = first.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	second, err := secondScope.AcquireMaintenanceExclusive(ctx, "owner=second\n")
	if err != nil {
		t.Fatalf("distinct database lock contended: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close second: %v", err)
	}
}

func TestSharedLeasesCoexistWithinEachFamily(t *testing.T) {
	scope, err := NewScope(t.TempDir(), "database-1")
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	tests := []struct {
		name    string
		acquire func(context.Context, string) (*Lease, error)
	}{
		{name: "maintenance", acquire: scope.AcquireMaintenanceShared},
		{name: "generation", acquire: scope.AcquireGenerationShared},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first, err := test.acquire(t.Context(), "owner=first\n")
			if err != nil {
				t.Fatalf("acquire first shared lease: %v", err)
			}
			defer func() { _ = first.Close() }()

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			second, err := test.acquire(ctx, "owner=second\n")
			if err != nil {
				t.Fatalf("second shared lease did not coexist: %v", err)
			}
			if err := second.Close(); err != nil {
				t.Fatalf("Close second shared lease: %v", err)
			}
		})
	}
}

func TestMaintenanceAcquisitionCancellationHasTypedDiagnostics(t *testing.T) {
	scope, err := NewScope(t.TempDir(), "database-1")
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	holder, err := scope.AcquireMaintenanceShared(t.Context(), "owner=holder\n")
	if err != nil {
		t.Fatalf("AcquireMaintenanceShared holder: %v", err)
	}
	defer func() { _ = holder.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	waiter, err := scope.AcquireMaintenanceExclusive(ctx, "owner=waiter\n")
	if waiter != nil {
		_ = waiter.Close()
		t.Fatal("exclusive maintenance waiter unexpectedly acquired")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire error = %v, want deadline exceeded", err)
	}
	var acquireErr *AcquireError
	if !errors.As(err, &acquireErr) {
		t.Fatalf("acquire error type = %T, want *AcquireError", err)
	}
	if acquireErr.Family != FamilyMaintenance || acquireErr.Mode != ModeExclusive {
		t.Fatalf("acquire diagnostics = family %q mode %q", acquireErr.Family, acquireErr.Mode)
	}
	if acquireErr.Path != holder.Path() {
		t.Fatalf("acquire path = %q, want %q", acquireErr.Path, holder.Path())
	}
}

func TestExclusiveGenerationRequiresLiveExclusiveMaintenance(t *testing.T) {
	scope, err := NewScope(t.TempDir(), "database-1")
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	maintenance, err := scope.AcquireMaintenanceExclusive(t.Context(), "owner=maintenance\n")
	if err != nil {
		t.Fatalf("AcquireMaintenanceExclusive: %v", err)
	}

	generation, err := maintenance.AcquireGenerationExclusive(t.Context(), "owner=generation\n")
	if err != nil {
		_ = maintenance.Close()
		t.Fatalf("AcquireGenerationExclusive: %v", err)
	}
	if generation.Family() != FamilyGeneration || generation.Mode() != ModeExclusive {
		_ = generation.Close()
		_ = maintenance.Close()
		t.Fatalf("generation diagnostics = family %q mode %q", generation.Family(), generation.Mode())
	}

	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	reader, err := scope.AcquireGenerationShared(ctx, "owner=reader\n")
	cancel()
	if reader != nil {
		_ = reader.Close()
		_ = generation.Close()
		_ = maintenance.Close()
		t.Fatal("shared generation acquired during exclusive generation lease")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		_ = generation.Close()
		_ = maintenance.Close()
		t.Fatalf("shared generation error = %v, want deadline exceeded", err)
	}

	if err := generation.Close(); err != nil {
		_ = maintenance.Close()
		t.Fatalf("Close generation first: %v", err)
	}
	if err := generation.Close(); err != nil {
		_ = maintenance.Close()
		t.Fatalf("Close generation second: %v", err)
	}
	if err := maintenance.Close(); err != nil {
		t.Fatalf("Close maintenance first: %v", err)
	}
	if err := maintenance.Close(); err != nil {
		t.Fatalf("Close maintenance second: %v", err)
	}

	generation, err = maintenance.AcquireGenerationExclusive(t.Context(), "owner=late\n")
	if generation != nil {
		_ = generation.Close()
		t.Fatal("closed maintenance lease acquired generation exclusive")
	}
	if !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("closed maintenance error = %v, want ErrLeaseClosed", err)
	}
}

func TestClosingMaintenanceClosesItsExclusiveGenerationFirst(t *testing.T) {
	scope, err := NewScope(t.TempDir(), "database-1")
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	maintenance, err := scope.AcquireMaintenanceExclusive(t.Context(), "owner=maintenance\n")
	if err != nil {
		t.Fatalf("AcquireMaintenanceExclusive: %v", err)
	}
	generation, err := maintenance.AcquireGenerationExclusive(t.Context(), "owner=generation\n")
	if err != nil {
		_ = maintenance.Close()
		t.Fatalf("AcquireGenerationExclusive: %v", err)
	}

	if err := maintenance.Close(); err != nil {
		t.Fatalf("Close maintenance: %v", err)
	}
	if err := generation.Close(); err != nil {
		t.Fatalf("Close generation after parent: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	reader, err := scope.AcquireGenerationShared(ctx, "owner=reader\n")
	if err != nil {
		t.Fatalf("generation remained held after maintenance close: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close reader: %v", err)
	}
}

type blockingLeaseCloser struct {
	started chan struct{}
	release chan struct{}
	err     error
	once    sync.Once
}

func newBlockingLeaseCloser(err error) *blockingLeaseCloser {
	return &blockingLeaseCloser{
		started: make(chan struct{}),
		release: make(chan struct{}),
		err:     err,
	}
}

func (c *blockingLeaseCloser) Close() error {
	c.once.Do(func() { close(c.started) })
	<-c.release
	return c.err
}

func TestLeaseConcurrentCloseWaitsAndReplaysResult(t *testing.T) {
	closeErr := errors.New("synthetic lease close failure")
	closer := newBlockingLeaseCloser(closeErr)
	lease := &Lease{lock: closer}

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- lease.Close()
	}()
	<-closer.started

	secondStarted := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondResult <- lease.Close()
	}()
	<-secondStarted
	select {
	case err := <-secondResult:
		t.Fatalf("concurrent Close returned before underlying close completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(closer.release)
	if err := <-firstResult; !errors.Is(err, closeErr) {
		t.Fatalf("first Close error = %v, want %v", err, closeErr)
	}
	if err := <-secondResult; !errors.Is(err, closeErr) {
		t.Fatalf("second Close error = %v, want replay of %v", err, closeErr)
	}
	if err := lease.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("later Close error = %v, want replay of %v", err, closeErr)
	}
}

func TestExclusiveMaintenanceConcurrentCloseWaitsForChildrenAndReplaysAllErrors(t *testing.T) {
	childErr := errors.New("synthetic child close failure")
	maintenanceErr := errors.New("synthetic maintenance close failure")
	childCloser := newBlockingLeaseCloser(childErr)
	maintenanceCloser := newBlockingLeaseCloser(maintenanceErr)
	close(maintenanceCloser.release)

	child := &Lease{lock: childCloser}
	maintenance := &Lease{lock: maintenanceCloser}
	parent := &ExclusiveMaintenanceLease{
		lease:    maintenance,
		children: map[*Lease]struct{}{child: {}},
	}

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- parent.Close()
	}()
	<-childCloser.started
	select {
	case <-maintenanceCloser.started:
		t.Fatal("maintenance released before child close completed")
	default:
	}

	secondStarted := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondResult <- parent.Close()
	}()
	<-secondStarted
	select {
	case err := <-secondResult:
		t.Fatalf("concurrent parent Close returned before child close completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(childCloser.release)
	for index, result := range []<-chan error{firstResult, secondResult} {
		err := <-result
		if !errors.Is(err, childErr) || !errors.Is(err, maintenanceErr) {
			t.Fatalf("parent Close %d error = %v, want child and maintenance errors", index+1, err)
		}
	}
	if err := parent.Close(); !errors.Is(err, childErr) || !errors.Is(err, maintenanceErr) {
		t.Fatalf("later parent Close error = %v, want replay of child and maintenance errors", err)
	}
}

func TestExclusiveMaintenanceCloseWaitsForChildCloseAlreadyInProgress(t *testing.T) {
	childErr := errors.New("synthetic child close failure")
	childCloser := newBlockingLeaseCloser(childErr)
	maintenanceCloser := newBlockingLeaseCloser(nil)
	close(maintenanceCloser.release)

	child := &Lease{lock: childCloser}
	maintenance := &Lease{lock: maintenanceCloser}
	parent := &ExclusiveMaintenanceLease{
		lease:    maintenance,
		children: map[*Lease]struct{}{child: {}},
	}
	child.onClose = func() {
		parent.mu.Lock()
		delete(parent.children, child)
		parent.mu.Unlock()
	}

	childResult := make(chan error, 1)
	go func() {
		childResult <- child.Close()
	}()
	<-childCloser.started

	parentResult := make(chan error, 1)
	go func() {
		parentResult <- parent.Close()
	}()
	select {
	case <-maintenanceCloser.started:
		t.Fatal("maintenance released while independently closing child was blocked")
	case err := <-parentResult:
		t.Fatalf("parent Close returned while independently closing child was blocked: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(childCloser.release)
	if err := <-childResult; !errors.Is(err, childErr) {
		t.Fatalf("child Close error = %v, want %v", err, childErr)
	}
	if err := <-parentResult; !errors.Is(err, childErr) {
		t.Fatalf("parent Close error = %v, want child error %v", err, childErr)
	}
	select {
	case <-maintenanceCloser.started:
	default:
		t.Fatal("maintenance was not released after child close completed")
	}
}

func TestGenerationLeaseRetainKeepsKernelLeaseUntilFinalReference(t *testing.T) {
	scope, err := NewScope(t.TempDir(), "database-retained-generation")
	if err != nil {
		t.Fatal(err)
	}
	shared, err := scope.AcquireGenerationShared(t.Context(), "owner=query\n")
	if err != nil {
		t.Fatal(err)
	}
	retained, err := shared.Retain()
	if err != nil {
		t.Fatal(err)
	}
	if err := shared.Close(); err != nil {
		t.Fatal(err)
	}

	maintenance, err := scope.AcquireMaintenanceExclusive(t.Context(), "owner=refresh\n")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = maintenance.Close() }()
	blockedCtx, cancelBlocked := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancelBlocked()
	if generation, err := maintenance.AcquireGenerationExclusive(blockedCtx, "owner=refresh\n"); generation != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("generation=%#v error=%v want retained shared lease contention", generation, err)
	}

	if err := retained.Close(); err != nil {
		t.Fatal(err)
	}
	if err := retained.Close(); err != nil {
		t.Fatalf("idempotent retained close: %v", err)
	}
	generation, err := maintenance.AcquireGenerationExclusive(t.Context(), "owner=refresh\n")
	if err != nil {
		t.Fatalf("acquire after final retained release: %v", err)
	}
	if err := generation.Close(); err != nil {
		t.Fatal(err)
	}
}
