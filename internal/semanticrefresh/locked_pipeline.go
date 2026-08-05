package semanticrefresh

import (
	"context"
	"errors"
	"fmt"

	"github.com/darron/dbrain/internal/semanticlock"
	"github.com/darron/dbrain/internal/store"
)

type refreshLockScope interface {
	acquireMaintenanceExclusive(context.Context, string) (refreshMaintenanceLease, error)
}

type refreshMaintenanceLease interface {
	acquireGenerationExclusive(context.Context, string) (refreshGenerationLease, error)
	Close() error
}

type refreshGenerationLease interface {
	Close() error
}

type semanticLockScopeAdapter struct {
	scope *semanticlock.Scope
}

func (a semanticLockScopeAdapter) acquireMaintenanceExclusive(
	ctx context.Context,
	metadata string,
) (refreshMaintenanceLease, error) {
	lease, err := a.scope.AcquireMaintenanceExclusive(ctx, metadata)
	if err != nil {
		return nil, err
	}
	return semanticMaintenanceLeaseAdapter{lease: lease}, nil
}

type semanticMaintenanceLeaseAdapter struct {
	lease *semanticlock.ExclusiveMaintenanceLease
}

func (a semanticMaintenanceLeaseAdapter) acquireGenerationExclusive(
	ctx context.Context,
	metadata string,
) (refreshGenerationLease, error) {
	lease, err := a.lease.AcquireGenerationExclusive(ctx, metadata)
	if err != nil {
		return nil, err
	}
	return lease, nil
}

func (a semanticMaintenanceLeaseAdapter) Close() error {
	return a.lease.Close()
}

type lockedPipeline struct {
	executor StageExecutor
	locks    refreshLockScope
}

// NewLockedPipeline wraps one semantic stage executor so each Execute call
// owns one exclusive maintenance lease. Publication stages also own generation
// exclusively, in the required maintenance-before-generation order.
func NewLockedPipeline(executor StageExecutor, scope *semanticlock.Scope) (StageExecutor, error) {
	if scope == nil {
		return nil, fmt.Errorf("semantic refresh lock scope is required")
	}
	return newLockedPipeline(executor, semanticLockScopeAdapter{scope: scope})
}

func newLockedPipeline(executor StageExecutor, locks refreshLockScope) (StageExecutor, error) {
	if executor == nil {
		return nil, fmt.Errorf("semantic refresh stage executor is required")
	}
	if locks == nil {
		return nil, fmt.Errorf("semantic refresh lock scope is required")
	}
	return &lockedPipeline{executor: executor, locks: locks}, nil
}

func (p *lockedPipeline) Execute(
	ctx context.Context,
	run store.SemanticRefreshRun,
	progress StageProgressCallback,
) (outcome StageOutcome, resultErr error) {
	if ctx == nil {
		return StageOutcome{}, fmt.Errorf("semantic refresh pipeline context is required")
	}
	if err := ctx.Err(); err != nil {
		return StageOutcome{}, err
	}

	maintenance, err := p.locks.acquireMaintenanceExclusive(
		ctx,
		refreshLockMetadata(run, "maintenance"),
	)
	if err != nil {
		return StageOutcome{}, refreshLockError(ctx, run, err)
	}
	defer func() {
		if closeErr := maintenance.Close(); closeErr != nil {
			resultErr = NewError(
				ErrorLockUnavailable,
				run,
				run.ReadinessState,
				outcome.Debt,
				errors.Join(resultErr, closeErr),
			)
		}
	}()

	executeCtx := context.WithValue(ctx, refreshMaintenanceLeaseContextKey{}, maintenance)
	var generation refreshGenerationLease
	if refreshStagePublishesGeneration(run.Stage) {
		generation, err = maintenance.acquireGenerationExclusive(
			ctx,
			refreshLockMetadata(run, "generation"),
		)
		if err != nil {
			return StageOutcome{}, refreshLockError(ctx, run, err)
		}
		defer func() {
			if closeErr := generation.Close(); closeErr != nil {
				resultErr = NewError(
					ErrorLockUnavailable,
					run,
					run.ReadinessState,
					outcome.Debt,
					errors.Join(resultErr, closeErr),
				)
			}
		}()
	}
	return p.executor.Execute(executeCtx, run, progress)
}

func refreshStagePublishesGeneration(stage store.SemanticRefreshStage) bool {
	switch stage {
	case store.SemanticRefreshFlush, store.SemanticRefreshCompaction:
		return true
	default:
		return false
	}
}

type refreshMaintenanceLeaseContextKey struct{}

func withRefreshGenerationExclusive(
	ctx context.Context,
	operation string,
	work func(context.Context) error,
) (resultErr error) {
	if ctx == nil {
		return fmt.Errorf("semantic generation publication context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	maintenance, ok := ctx.Value(refreshMaintenanceLeaseContextKey{}).(refreshMaintenanceLease)
	if !ok || maintenance == nil {
		return semanticLockFailure{
			cause: fmt.Errorf("semantic generation publication requires exclusive maintenance"),
		}
	}
	generation, err := maintenance.acquireGenerationExclusive(ctx, operation)
	if err != nil {
		return refreshLockCause(ctx, err)
	}
	defer func() {
		if closeErr := generation.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, semanticLockFailure{cause: closeErr})
		}
	}()
	return work(ctx)
}

type semanticLockFailure struct {
	cause error
}

func (e semanticLockFailure) Error() string {
	return ErrorLockUnavailable
}

func (e semanticLockFailure) Unwrap() error {
	return e.cause
}

func refreshLockCause(ctx context.Context, err error) error {
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	return semanticLockFailure{cause: err}
}

func refreshLockError(
	ctx context.Context,
	run store.SemanticRefreshRun,
	err error,
) error {
	cause := refreshLockCause(ctx, err)
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return NewError(
		ErrorLockUnavailable,
		run,
		run.ReadinessState,
		Debt{},
		cause,
	)
}

func refreshLockMetadata(run store.SemanticRefreshRun, family string) string {
	return fmt.Sprintf(
		"operation=semantic_refresh\nfamily=%s\nrun_id=%s\nstage=%s\n",
		family,
		boundedProtocolField(run.RunID, errorRunIDLimit),
		safeRefreshStage(run.Stage),
	)
}
