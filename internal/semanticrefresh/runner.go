package semanticrefresh

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/store"
)

const cancellationCheckpointTimeout = 2 * time.Second
const generationIDLimit = 64

type RunLedger interface {
	StartOrResumeSemanticRefreshRun(
		context.Context,
		store.StartSemanticRefreshRunInput,
	) (store.SemanticRefreshRun, bool, error)
	UpdateSemanticRefreshRun(
		context.Context,
		store.SemanticRefreshRunUpdate,
	) (store.SemanticRefreshRun, error)
	TouchSemanticRefreshRunProgress(context.Context, string, time.Time) error
}

// Run executes one bounded semantic refresh stage at a time and durably
// checkpoints every stage boundary before advancing.
func Run(
	ctx context.Context,
	ledger RunLedger,
	executor StageExecutor,
	request Request,
) (Result, error) {
	result := Result{Capability: request.Capability}
	if ctx == nil {
		return result, fmt.Errorf("semantic refresh context is required")
	}
	if ledger == nil {
		return result, fmt.Errorf("semantic refresh run ledger is required")
	}
	if executor == nil {
		return result, fmt.Errorf("semantic refresh stage executor is required")
	}
	if request.NewRunIDFunc == nil {
		request.NewRunIDFunc = newOpaqueRunID
	}
	if err := request.Validate(); err != nil {
		return result, err
	}

	runID, err := nextRunID(request.NewRunIDFunc)
	if err != nil {
		return result, NewError(ErrorBackendBroken, store.SemanticRefreshRun{}, "", Debt{}, err)
	}
	run, _, err := ledger.StartOrResumeSemanticRefreshRun(ctx, store.StartSemanticRefreshRunInput{
		RunID:               runID,
		ProfileID:           request.ProfileID,
		PurgeEpoch:          request.PurgeEpoch,
		ProjectionWatermark: request.ProjectionWatermark,
		Now:                 request.Now(),
	})
	if err != nil {
		return result, NewError(ErrorRunConflict, store.SemanticRefreshRun{}, "", Debt{}, err)
	}
	run = normalizeLoadedRunGeneration(run)

	debt := Debt{}
	for {
		emitter, startErr := StartProgressEmitter(
			ctx,
			ledger,
			run,
			debt,
			request.Progress,
		)
		if startErr != nil {
			if ctx.Err() != nil {
				return persistCancelled(
					ctx,
					ledger,
					request,
					result,
					run,
					run.Stage,
					debt,
					errors.Join(ctx.Err(), startErr),
				)
			}
			if errors.Is(startErr, store.ErrSemanticRefreshRunStale) {
				return failureResult(result, run, debt),
					NewError(ErrorRunConflict, run, run.ReadinessState, debt, startErr)
			}
			return persistTerminalFailure(
				ctx,
				ledger,
				request,
				result,
				run,
				run.Stage,
				debt,
				startErr,
			)
		}

		completed, successor, nextRun, nextDebt, runErr := runOneLedgerRun(
			ctx,
			ledger,
			executor,
			request,
			result,
			run,
			debt,
			emitter,
		)
		if runErr != nil {
			return completed, runErr
		}
		if successor == nil {
			return completed, nil
		}
		run = nextRun
		debt = nextDebt
	}
}

func runOneLedgerRun(
	ctx context.Context,
	ledger RunLedger,
	executor StageExecutor,
	request Request,
	baseResult Result,
	run store.SemanticRefreshRun,
	debt Debt,
	emitter *ProgressEmitter,
) (Result, *int64, store.SemanticRefreshRun, Debt, error) {
	for {
		executedStage := run.Stage
		outcome, executeErr := executor.Execute(emitter.Context(), run)
		var generationErr error
		outcome, generationErr = sanitizeStageOutcomeGeneration(run, outcome)
		if executeErr == nil {
			executeErr = generationErr
		}
		if stageOutcomeIsZero(outcome) && executeErr == nil {
			executeErr = fmt.Errorf("semantic refresh executor returned no outcome")
		}

		if !stageOutcomeIsZero(outcome) {
			persisted, updateErr := updateRunDurably(
				ctx,
				ledger,
				outcomeUpdate(request, run, outcome, store.SemanticRefreshRunRunning, "", ""),
			)
			if updateErr != nil {
				stopErr := stopProgressEmitter(emitter)
				if ctx.Err() != nil {
					result, terminalErr := persistCancelled(
						ctx,
						ledger,
						request,
						baseResult,
						run,
						executedStage,
						debt,
						errors.Join(ctx.Err(), updateErr, stopErr),
					)
					return result, nil, store.SemanticRefreshRun{}, Debt{}, terminalErr
				}
				return failureResult(baseResult, run, debt), nil, store.SemanticRefreshRun{}, Debt{},
					NewError(ErrorRunConflict, run, run.ReadinessState, debt, updateErr)
			}
			run = persisted
			debt = outcome.Debt
		}

		var publishErr error
		if ctx.Err() == nil && !stageOutcomeIsZero(outcome) {
			publishErr = emitter.Publish(run, debt)
		}

		parentErr := ctx.Err()
		if parentErr != nil {
			stopErr := stopProgressEmitter(emitter)
			cause := errors.Join(parentErr, executeErr, publishErr, stopErr)
			result, terminalErr := persistCancelled(
				ctx,
				ledger,
				request,
				baseResult,
				run,
				executedStage,
				debt,
				cause,
			)
			return result, nil, store.SemanticRefreshRun{}, Debt{}, terminalErr
		}

		if outcome.SuccessorWatermark != nil &&
			*outcome.SuccessorWatermark <= run.ProjectionWatermark {
			stopErr := stopProgressEmitter(emitter)
			cause := errors.Join(
				fmt.Errorf(
					"semantic successor watermark %d must exceed current watermark %d",
					*outcome.SuccessorWatermark,
					run.ProjectionWatermark,
				),
				stopErr,
			)
			result, terminalErr := persistTerminalCode(
				ctx,
				ledger,
				request,
				baseResult,
				run,
				executedStage,
				debt,
				ErrorRunConflict,
				cause,
			)
			return result, nil, store.SemanticRefreshRun{}, Debt{}, terminalErr
		}

		if publishErr != nil || executeErr != nil || emitter.Context().Err() != nil {
			stopErr := stopProgressEmitter(emitter)
			cause := firstMeaningfulError(stopErr, publishErr, executeErr, emitter.Context().Err())
			var executorRefreshErr *RefreshError
			if errors.As(executeErr, &executorRefreshErr) {
				cause = executorRefreshErr
			}
			if errors.Is(cause, store.ErrSemanticRefreshRunStale) {
				return failureResult(baseResult, run, debt), nil, store.SemanticRefreshRun{}, Debt{},
					NewError(ErrorRunConflict, errorRun(run, executedStage), run.ReadinessState, debt, cause)
			}
			result, terminalErr := persistTerminalFailure(
				ctx,
				ledger,
				request,
				baseResult,
				run,
				executedStage,
				debt,
				cause,
			)
			return result, nil, store.SemanticRefreshRun{}, Debt{}, terminalErr
		}

		if !outcome.Complete {
			continue
		}

		if stopErr := stopProgressEmitter(emitter); stopErr != nil {
			if errors.Is(stopErr, store.ErrSemanticRefreshRunStale) {
				return failureResult(baseResult, run, debt), nil, store.SemanticRefreshRun{}, Debt{},
					NewError(ErrorRunConflict, errorRun(run, executedStage), run.ReadinessState, debt, stopErr)
			}
			result, terminalErr := persistTerminalFailure(
				ctx,
				ledger,
				request,
				baseResult,
				run,
				executedStage,
				debt,
				stopErr,
			)
			return result, nil, store.SemanticRefreshRun{}, Debt{}, terminalErr
		}
		if ctx.Err() != nil {
			result, terminalErr := persistCancelled(
				ctx,
				ledger,
				request,
				baseResult,
				run,
				executedStage,
				debt,
				ctx.Err(),
			)
			return result, nil, store.SemanticRefreshRun{}, Debt{}, terminalErr
		}

		completed, updateErr := ledger.UpdateSemanticRefreshRun(
			ctx,
			runStateUpdate(request, run, store.SemanticRefreshRunCompleted, "", ""),
		)
		if updateErr != nil {
			if ctx.Err() != nil {
				result, terminalErr := persistCancelled(
					ctx,
					ledger,
					request,
					baseResult,
					run,
					executedStage,
					debt,
					errors.Join(ctx.Err(), updateErr),
				)
				return result, nil, store.SemanticRefreshRun{}, Debt{}, terminalErr
			}
			return failureResult(baseResult, run, debt), nil, store.SemanticRefreshRun{}, Debt{},
				NewError(ErrorRunConflict, run, run.ReadinessState, debt, updateErr)
		}
		if ctx.Err() != nil {
			return failureResult(baseResult, completed, debt), nil, store.SemanticRefreshRun{}, Debt{},
				NewError(
					ErrorCancelled,
					errorRun(completed, executedStage),
					completed.ReadinessState,
					debt,
					ctx.Err(),
				)
		}
		completedResult := completedRunResult(baseResult, completed, debt)
		if outcome.SuccessorWatermark == nil {
			return completedResult, nil, store.SemanticRefreshRun{}, Debt{}, nil
		}

		successor, successorDebt, successorErr := startSuccessor(
			ctx,
			ledger,
			request,
			completed,
			debt,
			*outcome.SuccessorWatermark,
		)
		if successorErr != nil {
			return failureResult(baseResult, completed, debt), nil, store.SemanticRefreshRun{}, Debt{}, successorErr
		}
		if ctx.Err() != nil {
			result, terminalErr := persistCancelled(
				ctx,
				ledger,
				request,
				baseResult,
				successor,
				store.SemanticRefreshProjection,
				successorDebt,
				ctx.Err(),
			)
			return result, nil, store.SemanticRefreshRun{}, Debt{}, terminalErr
		}
		return completedResult, outcome.SuccessorWatermark, successor, successorDebt, nil
	}
}

func startSuccessor(
	ctx context.Context,
	ledger RunLedger,
	request Request,
	completed store.SemanticRefreshRun,
	debt Debt,
	watermark int64,
) (store.SemanticRefreshRun, Debt, error) {
	if ctx.Err() != nil {
		return store.SemanticRefreshRun{}, Debt{},
			NewError(ErrorCancelled, completed, completed.ReadinessState, debt, ctx.Err())
	}
	runID, err := nextRunID(request.NewRunIDFunc)
	if err != nil {
		return store.SemanticRefreshRun{}, Debt{},
			NewError(ErrorBackendBroken, completed, completed.ReadinessState, debt, err)
	}
	if ctx.Err() != nil {
		return store.SemanticRefreshRun{}, Debt{},
			NewError(ErrorCancelled, completed, completed.ReadinessState, debt, ctx.Err())
	}
	if completed.Counters.SuccessorRuns == 1<<63-1 {
		return store.SemanticRefreshRun{}, Debt{},
			NewError(
				ErrorRunConflict,
				completed,
				completed.ReadinessState,
				debt,
				fmt.Errorf("semantic successor count overflow"),
			)
	}
	successorCounters := store.SemanticRefreshCounters{
		SuccessorRuns: completed.Counters.SuccessorRuns + 1,
	}
	successor, resumed, err := ledger.StartOrResumeSemanticRefreshRun(
		ctx,
		store.StartSemanticRefreshRunInput{
			RunID:               runID,
			ProfileID:           completed.ProfileID,
			PurgeEpoch:          completed.PurgeEpoch,
			ProjectionWatermark: watermark,
			InitialCounters:     successorCounters,
			Now:                 request.Now(),
		},
	)
	if err != nil {
		if ctx.Err() != nil {
			return store.SemanticRefreshRun{}, Debt{},
				NewError(ErrorCancelled, completed, completed.ReadinessState, debt, ctx.Err())
		}
		return store.SemanticRefreshRun{}, Debt{},
			NewError(ErrorRunConflict, completed, completed.ReadinessState, debt, err)
	}
	successor = normalizeLoadedRunGeneration(successor)
	if resumed ||
		successor.RunID != runID ||
		successor.ProfileID != completed.ProfileID ||
		successor.PurgeEpoch != completed.PurgeEpoch ||
		successor.ProjectionWatermark != watermark ||
		successor.Stage != store.SemanticRefreshProjection ||
		successor.Counters != successorCounters {
		return store.SemanticRefreshRun{}, Debt{},
			NewError(
				ErrorRunConflict,
				completed,
				completed.ReadinessState,
				debt,
				fmt.Errorf("semantic successor run collided with existing state"),
			)
	}
	return successor, debt, nil
}

func persistTerminalFailure(
	ctx context.Context,
	ledger RunLedger,
	request Request,
	baseResult Result,
	run store.SemanticRefreshRun,
	executedStage store.SemanticRefreshStage,
	debt Debt,
	cause error,
) (Result, error) {
	code := refreshErrorCode(executedStage, cause)
	return persistTerminalCode(
		ctx,
		ledger,
		request,
		baseResult,
		run,
		executedStage,
		debt,
		code,
		cause,
	)
}

func persistTerminalCode(
	ctx context.Context,
	ledger RunLedger,
	request Request,
	baseResult Result,
	run store.SemanticRefreshRun,
	executedStage store.SemanticRefreshStage,
	debt Debt,
	code string,
	cause error,
) (Result, error) {
	refreshErr := NewError(
		code,
		errorRun(run, executedStage),
		run.ReadinessState,
		debt,
		cause,
	)
	failed, err := updateRunDurably(
		ctx,
		ledger,
		runStateUpdate(
			request,
			run,
			store.SemanticRefreshRunFailed,
			refreshErr.Code,
			refreshErr.Message,
		),
	)
	if err != nil {
		return failureResult(baseResult, run, debt),
			NewError(
				ErrorRunConflict,
				errorRun(run, executedStage),
				run.ReadinessState,
				debt,
				err,
			)
	}
	var executorRefreshErr *RefreshError
	if errors.As(cause, &executorRefreshErr) {
		preservedCause := executorRefreshErr.cause
		*executorRefreshErr = *NewError(
			code,
			errorRun(failed, executedStage),
			failed.ReadinessState,
			debt,
			preservedCause,
		)
		return failureResult(baseResult, failed, debt), executorRefreshErr
	}
	return failureResult(baseResult, failed, debt),
		NewError(code, errorRun(failed, executedStage), failed.ReadinessState, debt, cause)
}

func persistCancelled(
	ctx context.Context,
	ledger RunLedger,
	request Request,
	baseResult Result,
	run store.SemanticRefreshRun,
	executedStage store.SemanticRefreshStage,
	debt Debt,
	cause error,
) (Result, error) {
	checkpointCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		cancellationCheckpointTimeout,
	)
	defer cancel()
	cancelled, err := ledger.UpdateSemanticRefreshRun(
		checkpointCtx,
		runStateUpdate(
			request,
			run,
			store.SemanticRefreshRunCancelled,
			ErrorCancelled,
			ErrorCancelled,
		),
	)
	if err != nil {
		combined := errors.Join(cause, err)
		return failureResult(baseResult, run, debt),
			NewError(
				ErrorCancelled,
				errorRun(run, executedStage),
				run.ReadinessState,
				debt,
				combined,
			)
	}
	return failureResult(baseResult, cancelled, debt),
		NewError(
			ErrorCancelled,
			errorRun(cancelled, executedStage),
			cancelled.ReadinessState,
			debt,
			cause,
		)
}

func outcomeUpdate(
	request Request,
	run store.SemanticRefreshRun,
	outcome StageOutcome,
	state store.SemanticRefreshRunState,
	errorCode string,
	errorText string,
) store.SemanticRefreshRunUpdate {
	stage := outcome.NextStage
	if stage == "" {
		stage = run.Stage
	}
	return store.SemanticRefreshRunUpdate{
		RunID:               run.RunID,
		ExpectedVersion:     run.Version,
		EmbeddingRevision:   outcome.EmbeddingRevision,
		Stage:               stage,
		Checkpoint:          boundedDiagnostic(outcome.Checkpoint, errorCheckpointLimit),
		Counters:            outcome.Counters,
		CurrentGenerationID: outcome.CurrentGenerationID,
		State:               state,
		ErrorCode:           boundedProtocolField(errorCode, errorCodeLimit),
		ErrorText:           boundedDiagnostic(errorText, errorMessageLimit),
		ReadinessState:      boundedProtocolField(outcome.Readiness, errorReadinessLimit),
		Now:                 request.Now(),
	}
}

func runStateUpdate(
	request Request,
	run store.SemanticRefreshRun,
	state store.SemanticRefreshRunState,
	errorCode string,
	errorText string,
) store.SemanticRefreshRunUpdate {
	return store.SemanticRefreshRunUpdate{
		RunID:               run.RunID,
		ExpectedVersion:     run.Version,
		EmbeddingRevision:   run.EmbeddingRevision,
		Stage:               run.Stage,
		Checkpoint:          boundedDiagnostic(run.Checkpoint, errorCheckpointLimit),
		Counters:            run.Counters,
		CurrentGenerationID: run.CurrentGenerationID,
		State:               state,
		ErrorCode:           boundedProtocolField(errorCode, errorCodeLimit),
		ErrorText:           boundedDiagnostic(errorText, errorMessageLimit),
		ReadinessState:      boundedProtocolField(run.ReadinessState, errorReadinessLimit),
		Now:                 request.Now(),
	}
}

func stageOutcomeIsZero(outcome StageOutcome) bool {
	return outcome == (StageOutcome{})
}

func sanitizeStageOutcomeGeneration(
	run store.SemanticRefreshRun,
	outcome StageOutcome,
) (StageOutcome, error) {
	if validGenerationID(outcome.CurrentGenerationID) {
		return outcome, nil
	}
	outcome.CurrentGenerationID = ""
	if validGenerationID(run.CurrentGenerationID) {
		outcome.CurrentGenerationID = run.CurrentGenerationID
	}
	return outcome, fmt.Errorf("semantic refresh generation ID is invalid")
}

func validGenerationID(generationID string) bool {
	if generationID == "" {
		return true
	}
	return len(generationID) <= generationIDLimit &&
		boundedProtocolField(generationID, generationIDLimit) == generationID
}

func normalizeLoadedRunGeneration(run store.SemanticRefreshRun) store.SemanticRefreshRun {
	if !validGenerationID(run.CurrentGenerationID) {
		run.CurrentGenerationID = ""
	}
	return run
}

func refreshErrorCode(stage store.SemanticRefreshStage, cause error) string {
	var refreshErr *RefreshError
	if errors.As(cause, &refreshErr) {
		return refreshErr.Code
	}
	switch stage {
	case store.SemanticRefreshProjection:
		return ErrorProjection
	case store.SemanticRefreshEmbedding:
		return ErrorEmbedding
	case store.SemanticRefreshFlush:
		return ErrorFlush
	case store.SemanticRefreshCompaction:
		return ErrorCompaction
	case store.SemanticRefreshVerify:
		return ErrorVerify
	case store.SemanticRefreshReadiness:
		return ErrorReadiness
	default:
		return ErrorBackendBroken
	}
}

func updateRunDurably(
	ctx context.Context,
	ledger RunLedger,
	update store.SemanticRefreshRunUpdate,
) (store.SemanticRefreshRun, error) {
	if ctx.Err() == nil {
		return ledger.UpdateSemanticRefreshRun(ctx, update)
	}
	checkpointCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		cancellationCheckpointTimeout,
	)
	defer cancel()
	return ledger.UpdateSemanticRefreshRun(checkpointCtx, update)
}

func stopProgressEmitter(emitter *ProgressEmitter) error {
	if emitter == nil {
		return nil
	}
	firstErr := emitter.Stop()
	<-emitter.done
	secondErr := emitter.Stop()
	return firstMeaningfulError(firstErr, secondErr)
}

func firstMeaningfulError(errs ...error) error {
	for _, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func errorRun(run store.SemanticRefreshRun, stage store.SemanticRefreshStage) store.SemanticRefreshRun {
	run.Stage = stage
	return run
}

func failureResult(
	base Result,
	run store.SemanticRefreshRun,
	debt Debt,
) Result {
	base.Outcome = ""
	base.SkipReason = ""
	base.Run = &run
	base.Debt = debt
	return base
}

func completedRunResult(
	base Result,
	run store.SemanticRefreshRun,
	debt Debt,
) Result {
	base.Outcome = OutcomeCompleted
	base.Run = &run
	base.Debt = debt
	return base
}

func nextRunID(generator func() (string, error)) (string, error) {
	runID, err := generator()
	if err != nil {
		return "", err
	}
	if len(runID) != 32 {
		return "", fmt.Errorf("semantic refresh run ID must be 32 lowercase hexadecimal characters")
	}
	for _, character := range runID {
		isDigit := character >= '0' && character <= '9'
		isLowerHex := character >= 'a' && character <= 'f'
		if !isDigit && !isLowerHex {
			return "", fmt.Errorf("semantic refresh run ID must be 32 lowercase hexadecimal characters")
		}
	}
	return runID, nil
}

func newOpaqueRunID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate semantic refresh run ID: %w", err)
	}
	return hex.EncodeToString(random[:]), nil
}
