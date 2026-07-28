package semanticlock

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
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
