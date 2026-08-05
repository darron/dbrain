package app

import (
	"context"
	"errors"
	"io/fs"

	"github.com/darron/dbrain/internal/notify"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/syncjob"
)

type scheduledSyncFailureBoundary string

const (
	scheduledBoundaryConfigResolution scheduledSyncFailureBoundary = "config_resolution"
	scheduledBoundaryMetricsOpen      scheduledSyncFailureBoundary = "metrics_open"
	scheduledBoundaryMetricsClose     scheduledSyncFailureBoundary = "metrics_close"
	scheduledBoundaryStoreOpen        scheduledSyncFailureBoundary = "store_open"
	scheduledBoundaryStoreClose       scheduledSyncFailureBoundary = "store_close"
	scheduledBoundaryOptions          scheduledSyncFailureBoundary = "options"
	scheduledBoundaryOutput           scheduledSyncFailureBoundary = "output"
)

type scheduledSyncBoundaryError struct {
	boundary scheduledSyncFailureBoundary
	cause    error
}

func wrapScheduledSyncBoundary(boundary scheduledSyncFailureBoundary, cause error) error {
	if cause == nil {
		return nil
	}
	switch boundary {
	case scheduledBoundaryConfigResolution,
		scheduledBoundaryMetricsOpen,
		scheduledBoundaryMetricsClose,
		scheduledBoundaryStoreOpen,
		scheduledBoundaryStoreClose,
		scheduledBoundaryOptions,
		scheduledBoundaryOutput:
		return &scheduledSyncBoundaryError{boundary: boundary, cause: cause}
	default:
		return cause
	}
}

func (e *scheduledSyncBoundaryError) Error() string { return e.cause.Error() }
func (e *scheduledSyncBoundaryError) Unwrap() error { return e.cause }

func classifyScheduledSyncOutcome(settled scheduledSyncOutcome) notify.Outcome {
	outcome := notify.Outcome{
		Operation:  notify.OperationScheduledSyncAll,
		StartedAt:  settled.StartedAt,
		FinishedAt: settled.FinishedAt,
	}
	if settled.Status == scheduledSyncStatusOK {
		outcome.Status = notify.OutcomeSuccess
		return outcome
	}
	if settled.Status == scheduledSyncStatusCancelled || isClassifiedCancellation(settled.Err) {
		outcome.Status = notify.OutcomeCancelled
		return outcome
	}
	outcome.Status = notify.OutcomeFailure
	outcome.FailureType = classifyScheduledFailure(settled.Err)
	definition, ok := notify.LookupFailure(outcome.FailureType)
	if !ok {
		definition, _ = notify.LookupFailure(notify.FailureUnknown)
		outcome.FailureType = notify.FailureUnknown
	}
	outcome.ErrorCode = definition.ErrorCode
	return outcome
}

func isClassifiedCancellation(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	var refreshErr *semanticrefresh.RefreshError
	return errors.As(err, &refreshErr) && refreshErr.Code == semanticrefresh.ErrorCancelled
}

func classifyScheduledFailure(err error) notify.FailureType {
	// Preserve errors.Join branch order so the primary operation failure wins
	// over a secondary cleanup failure from metrics or output settlement.
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if failureType := classifyScheduledFailure(child); failureType != notify.FailureUnknown {
				return failureType
			}
		}
		return notify.FailureUnknown
	}
	var stageErr *syncjob.StageError
	if errors.As(err, &stageErr) {
		if stageErr.Stage() == "apple_notes" && errors.Is(err, fs.ErrPermission) {
			return notify.FailureAppleNotesPermission
		}
		if failureType, ok := notify.LookupStageFailure(stageErr.Stage()); ok {
			return failureType
		}
	}
	var boundaryErr *scheduledSyncBoundaryError
	if errors.As(err, &boundaryErr) {
		switch boundaryErr.boundary {
		case scheduledBoundaryConfigResolution:
			return notify.FailureConfigResolution
		case scheduledBoundaryMetricsOpen:
			return notify.FailureMetricsOpen
		case scheduledBoundaryMetricsClose:
			return notify.FailureMetricsClose
		case scheduledBoundaryStoreOpen:
			return notify.FailureStoreOpen
		case scheduledBoundaryStoreClose:
			return notify.FailureStoreClose
		case scheduledBoundaryOptions:
			return notify.FailureOptions
		case scheduledBoundaryOutput:
			return notify.FailureOutput
		default:
			return notify.FailureUnknown
		}
	}
	var refreshErr *semanticrefresh.RefreshError
	if errors.As(err, &refreshErr) {
		if failureType, ok := notify.LookupSemanticFailure(refreshErr.Code); ok {
			return failureType
		}
	}
	return notify.FailureUnknown
}
