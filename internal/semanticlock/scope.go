package semanticlock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/darron/dbrain/internal/runlock"
)

const maxDatabaseIDBytes = 128

type Family string

const (
	FamilyMaintenance Family = "maintenance"
	FamilyGeneration  Family = "generation"
)

type Mode string

const (
	ModeShared    Mode = "shared"
	ModeExclusive Mode = "exclusive"
)

var (
	ErrInvalidDatabaseID = errors.New("invalid semantic database ID")
	ErrLeaseClosed       = errors.New("semantic lock lease is closed")
)

type AcquireError struct {
	Family Family
	Mode   Mode
	Path   string
	cause  error
}

func (e *AcquireError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("acquire semantic %s %s lock %s: %v", e.Family, e.Mode, e.Path, e.cause)
}

func (e *AcquireError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type Scope struct {
	databaseID      string
	lockRoot        string
	maintenancePath string
	generationPath  string
}

func NewScope(cacheDir string, databaseID string) (*Scope, error) {
	if err := validateDatabaseID(databaseID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cacheDir) == "" {
		return nil, errors.New("semantic cache directory is empty")
	}
	absoluteCache, err := filepath.Abs(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("resolve semantic cache directory: %w", err)
	}
	if resolvedCache, resolveErr := filepath.EvalSymlinks(absoluteCache); resolveErr == nil {
		absoluteCache = resolvedCache
	}
	lockRoot := filepath.Join(filepath.Clean(absoluteCache), "semantic", databaseID, "locks")
	return &Scope{
		databaseID:      databaseID,
		lockRoot:        lockRoot,
		maintenancePath: filepath.Join(lockRoot, "maintenance.lock"),
		generationPath:  filepath.Join(lockRoot, "generation.lock"),
	}, nil
}

func (s *Scope) AcquireMaintenanceShared(ctx context.Context, metadata string) (*Lease, error) {
	return s.acquire(ctx, FamilyMaintenance, ModeShared, metadata)
}

func (s *Scope) AcquireMaintenanceExclusive(ctx context.Context, metadata string) (*ExclusiveMaintenanceLease, error) {
	lease, err := s.acquire(ctx, FamilyMaintenance, ModeExclusive, metadata)
	if err != nil {
		return nil, err
	}
	return &ExclusiveMaintenanceLease{
		scope:    s,
		lease:    lease,
		children: make(map[*Lease]struct{}),
	}, nil
}

func (s *Scope) AcquireGenerationShared(ctx context.Context, metadata string) (*Lease, error) {
	return s.acquire(ctx, FamilyGeneration, ModeShared, metadata)
}

func (s *Scope) acquire(ctx context.Context, family Family, mode Mode, metadata string) (*Lease, error) {
	if s == nil {
		return nil, acquireError(family, mode, "", errors.New("semantic lock scope is nil"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, acquireError(family, mode, s.path(family), err)
	}
	if err := os.MkdirAll(s.lockRoot, 0o755); err != nil {
		return nil, acquireError(family, mode, s.path(family), fmt.Errorf("create semantic lock root: %w", err))
	}
	runMode := runlock.Shared
	if mode == ModeExclusive {
		runMode = runlock.Exclusive
	}
	path := s.path(family)
	lock, err := runlock.AcquireContext(ctx, path, runlock.AcquireOptions{
		Mode:     runMode,
		Metadata: semanticLockMetadata(s.databaseID, family, mode, metadata),
	})
	if err != nil {
		return nil, acquireError(family, mode, path, err)
	}
	return &Lease{
		lock:   lock,
		path:   lock.Path(),
		family: family,
		mode:   mode,
	}, nil
}

func (s *Scope) path(family Family) string {
	if s == nil {
		return ""
	}
	if family == FamilyGeneration {
		return s.generationPath
	}
	return s.maintenancePath
}

type Lease struct {
	mu      sync.Mutex
	lock    *runlock.Lock
	path    string
	family  Family
	mode    Mode
	onClose func()
}

func (l *Lease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	lock := l.lock
	l.lock = nil
	onClose := l.onClose
	l.onClose = nil
	l.mu.Unlock()
	if lock == nil {
		return nil
	}
	err := lock.Close()
	if onClose != nil {
		onClose()
	}
	return err
}

func (l *Lease) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func (l *Lease) Family() Family {
	if l == nil {
		return ""
	}
	return l.family
}

func (l *Lease) Mode() Mode {
	if l == nil {
		return ""
	}
	return l.mode
}

type ExclusiveMaintenanceLease struct {
	mu       sync.Mutex
	scope    *Scope
	lease    *Lease
	children map[*Lease]struct{}
}

func (l *ExclusiveMaintenanceLease) AcquireGenerationExclusive(ctx context.Context, metadata string) (*Lease, error) {
	if l == nil {
		return nil, acquireError(FamilyGeneration, ModeExclusive, "", ErrLeaseClosed)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lease == nil {
		path := ""
		if l.scope != nil {
			path = l.scope.generationPath
		}
		return nil, acquireError(FamilyGeneration, ModeExclusive, path, ErrLeaseClosed)
	}
	generation, err := l.scope.acquire(ctx, FamilyGeneration, ModeExclusive, metadata)
	if err != nil {
		return nil, err
	}
	generation.onClose = func() {
		l.mu.Lock()
		delete(l.children, generation)
		l.mu.Unlock()
	}
	l.children[generation] = struct{}{}
	return generation, nil
}

func (l *ExclusiveMaintenanceLease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	maintenance := l.lease
	l.lease = nil
	children := make([]*Lease, 0, len(l.children))
	for child := range l.children {
		children = append(children, child)
	}
	l.children = nil
	l.mu.Unlock()
	if maintenance == nil {
		return nil
	}
	var errs []error
	for _, child := range children {
		errs = append(errs, child.Close())
	}
	errs = append(errs, maintenance.Close())
	return errors.Join(errs...)
}

func (l *ExclusiveMaintenanceLease) Path() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lease != nil {
		return l.lease.Path()
	}
	if l.scope == nil {
		return ""
	}
	return l.scope.maintenancePath
}

func (l *ExclusiveMaintenanceLease) Family() Family {
	return FamilyMaintenance
}

func (l *ExclusiveMaintenanceLease) Mode() Mode {
	return ModeExclusive
}

func validateDatabaseID(databaseID string) error {
	if len(databaseID) == 0 || len(databaseID) > maxDatabaseIDBytes {
		return fmt.Errorf("%w", ErrInvalidDatabaseID)
	}
	for _, character := range []byte(databaseID) {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' ||
			character == '_' {
			continue
		}
		return fmt.Errorf("%w", ErrInvalidDatabaseID)
	}
	return nil
}

func acquireError(family Family, mode Mode, path string, cause error) error {
	return &AcquireError{Family: family, Mode: mode, Path: path, cause: cause}
}

func semanticLockMetadata(databaseID string, family Family, mode Mode, metadata string) string {
	return fmt.Sprintf(
		"database_id=%s\nfamily=%s\nmode=%s\n%s",
		databaseID,
		family,
		mode,
		metadata,
	)
}
