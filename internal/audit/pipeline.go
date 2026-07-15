package audit

import (
	"context"
	"strings"
	"time"
)

func executePipeline(_ context.Context, s *runState, e RegistryEntry) Check {
	if strings.HasSuffix(string(e.ID), ".provenance") {
		if s.provenanceErr != nil {
			return unknownCheck(e, ErrorRead, s.now)
		}
		item, ok := s.provenance[e.ID]
		if !ok || !item.CutoverKnown {
			return unknownCheck(e, ErrorUnavailable, s.now)
		}
		ev := Evidence{"successful_count": item.SuccessfulCount, "complete_count": item.CompleteCount, "legacy_missing_count": item.LegacyMissingCount, "post_cutover_missing_count": item.PostCutoverMissingCount, "cutover_at": item.CutoverAt.UTC().Format(time.RFC3339), "missing_by_field": item.MissingByField}
		status := StatusPass
		if item.PostCutoverMissingCount > 0 {
			status = StatusFail
		} else if item.LegacyMissingCount > 0 {
			status = StatusWarn
		}
		return baseCheck(e, s.now, status, ConfidenceHigh, ev)
	}
	if s.pipelineErr != nil {
		return unknownCheck(e, ErrorRead, s.now)
	}
	stage := stageForCheck(e.ID)
	item, ok := s.pipeline[stage]
	if !ok {
		return unknownCheck(e, ErrorUnavailable, s.now)
	}
	if strings.HasSuffix(string(e.ID), ".pending_age") {
		ev := Evidence{"pending_count": item.Pending, "warn_after_seconds": seconds(DefaultPendingWarn), "fail_after_seconds": seconds(DefaultPendingFail)}
		if item.Pending == 0 {
			return baseCheck(e, s.now, StatusPass, ConfidenceHigh, ev)
		}
		if !item.OldestPendingKnown {
			return baseCheck(e, s.now, StatusUnknown, ConfidenceUnknown, ev)
		}
		if item.OldestPendingAt.After(s.now) {
			return baseCheck(e, s.now, StatusUnknown, ConfidenceUnknown, ev)
		}
		age := s.now.Sub(item.OldestPendingAt)
		ev["oldest_pending_age_seconds"] = seconds(age)
		return baseCheck(e, s.now, ClassifyAge(age, DefaultPendingWarn, DefaultPendingFail), ConfidenceHigh, ev)
	}
	byKind := make([]map[string]any, 0, len(item.ByKind))
	for _, row := range item.ByKind {
		byKind = append(byKind, map[string]any{"kind": row.Kind, "total": row.Total, "current": row.Current, "pending": row.Pending, "blocked": row.Blocked, "terminal": row.Terminal, "failed": row.Failed, "unknown": row.Unknown, "partition_valid": row.PartitionValid})
	}
	ev := Evidence{"total": item.Total, "current": item.Current, "pending": item.Pending, "blocked": item.Blocked, "terminal": item.Terminal, "failed": item.Failed, "unknown": item.Unknown, "partition_valid": item.PartitionValid, "by_kind": byKind}
	status := StatusPass
	if !item.PartitionValid || item.Unknown > 0 {
		status = StatusFail
	}
	return baseCheck(e, s.now, status, ConfidenceHigh, ev)
}
