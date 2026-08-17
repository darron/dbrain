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
	outcomes := classifyScheduledSyncOutcomes(settled)
	if len(outcomes) > 0 {
		return outcomes[0]
	}
	return notify.Outcome{
		Operation:  notify.OperationScheduledSyncAll,
		StartedAt:  settled.StartedAt,
		FinishedAt: settled.FinishedAt,
	}
}

func classifyScheduledSyncOutcomes(settled scheduledSyncOutcome) []notify.Outcome {
	outcome := notify.Outcome{
		Operation:  notify.OperationScheduledSyncAll,
		StartedAt:  settled.StartedAt,
		FinishedAt: settled.FinishedAt,
	}
	if settled.Status == scheduledSyncStatusOK {
		outcome.Status = notify.OutcomeSuccess
		return []notify.Outcome{outcome}
	}
	if settled.Status == scheduledSyncStatusCancelled || isCancellationOnly(settled.Err) {
		outcome.Status = notify.OutcomeCancelled
		return []notify.Outcome{outcome}
	}
	outcome.Status = notify.OutcomeFailure
	failureTypes := classifyScheduledFailureTypes(settled.Err)
	if len(failureTypes) == 0 {
		failureTypes = []notify.FailureType{notify.FailureUnknown}
	}
	outcomes := make([]notify.Outcome, 0, len(failureTypes))
	for _, failureType := range failureTypes {
		classified := outcome
		classified.FailureType = failureType
		definition, ok := notify.LookupFailure(failureType)
		if !ok {
			definition, _ = notify.LookupFailure(notify.FailureUnknown)
			classified.FailureType = notify.FailureUnknown
		}
		classified.ErrorCode = definition.ErrorCode
		outcomes = append(outcomes, classified)
	}
	return outcomes
}

func isClassifiedCancellation(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	var refreshErr *semanticrefresh.RefreshError
	return errors.As(err, &refreshErr) && refreshErr.Code == semanticrefresh.ErrorCancelled
}

func isCancellationOnly(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !isCancellationOnly(child) {
				return false
			}
		}
		return true
	}
	if refreshErr, ok := err.(*semanticrefresh.RefreshError); ok {
		return refreshErr.Code == semanticrefresh.ErrorCancelled
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if cause := wrapped.Unwrap(); cause != nil {
			return isCancellationOnly(cause)
		}
	}
	return isClassifiedCancellation(err)
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

func classifyScheduledFailureTypes(err error) []notify.FailureType {
	if err == nil {
		return nil
	}
	if isCancellationOnly(err) {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		failureTypes := make([]notify.FailureType, 0, len(joined.Unwrap()))
		for _, child := range joined.Unwrap() {
			for _, failureType := range classifyScheduledFailureTypes(child) {
				if !containsFailureType(failureTypes, failureType) {
					failureTypes = append(failureTypes, failureType)
				}
			}
		}
		return failureTypes
	}
	return []notify.FailureType{classifyScheduledFailure(err)}
}

func containsFailureType(failureTypes []notify.FailureType, want notify.FailureType) bool {
	for _, failureType := range failureTypes {
		if failureType == want {
			return true
		}
	}
	return false
}
