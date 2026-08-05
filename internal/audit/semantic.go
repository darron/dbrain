package audit

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/darron/dbrain/internal/metrics"
)

var semanticStageOrder = []SemanticStage{"projection", "embedding", "flush", "compaction", "verification", "readiness"}

func executeSemantic(_ context.Context, s *runState, entry RegistryEntry) Check {
	if s.semanticErr != nil {
		return unknownCheck(entry, semanticInspectionErrorCode(s.semanticErr), s.now)
	}
	capability, invariantValid := semanticCapabilityEvidence(s.semantic)
	switch entry.ID {
	case CheckSemanticCurrentReadiness:
		return executeSemanticCurrent(s, entry, capability, invariantValid)
	case CheckSemanticLatestAttachedRefresh:
		return executeSemanticLatest(s, entry, capability)
	case CheckSemanticStageSummary:
		return executeSemanticStages(s, entry, capability)
	default:
		return unknownCheck(entry, ErrorUnavailable, s.now)
	}
}

func executeSemanticCurrent(s *runState, entry RegistryEntry, capability string, invariantValid bool) Check {
	readiness := s.semantic.Readiness
	if !readiness.Valid() {
		readiness = "unavailable"
		invariantValid = false
	}
	backend := s.semantic.Backend
	if !backend.Valid() {
		backend = "none"
		invariantValid = false
	}
	evidence := Evidence{
		"configured":              s.semantic.Configured,
		"capability":              capability,
		"backend":                 string(backend),
		"readiness":               string(readiness),
		"dirty_parent_count":      nonnegativeSemanticCount(s.semantic.DirtyParentCount),
		"pending_parent_count":    nonnegativeSemanticCount(s.semantic.PendingParentCount),
		"due_embedding_count":     nonnegativeSemanticCount(s.semantic.DueEmbeddingCount),
		"blocked_embedding_count": nonnegativeSemanticCount(s.semantic.BlockedEmbeddingCount),
		"failed_embedding_count":  nonnegativeSemanticCount(s.semantic.FailedEmbeddingCount),
		"indexed_vector_count":    nonnegativeSemanticCount(s.semantic.IndexedVectorCount),
		"l0_vector_count":         nonnegativeSemanticCount(s.semantic.L0VectorCount),
		"tombstone_count":         nonnegativeSemanticCount(s.semantic.TombstoneCount),
		"segment_count":           nonnegativeSemanticCount(s.semantic.SegmentCount),
	}
	if s.semantic.ProfileID.Valid() {
		evidence["profile_id"] = string(s.semantic.ProfileID)
	} else {
		evidence["profile_id"] = "none"
		if s.semantic.Configured && capability == "available" {
			invariantValid = false
		}
	}
	if s.semantic.ActiveGenerationID.Valid() {
		evidence["active_generation_id"] = string(s.semantic.ActiveGenerationID)
	} else {
		evidence["active_generation_id"] = "none"
	}
	if min(s.semantic.DirtyParentCount, s.semantic.PendingParentCount, s.semantic.DueEmbeddingCount,
		s.semantic.BlockedEmbeddingCount, s.semantic.FailedEmbeddingCount, s.semantic.IndexedVectorCount,
		s.semantic.L0VectorCount, s.semantic.TombstoneCount, s.semantic.SegmentCount) < 0 {
		invariantValid = false
	}

	status, confidence := StatusFail, ConfidenceHigh
	switch {
	case capability == "disabled":
		// Disabled/not-configured is an explicit optional state, not evidence of
		// a successful semantic runtime.
		return baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, Evidence{
			"configured": s.semantic.Configured, "capability": capability,
			"backend": string(backend), "readiness": "disabled",
		})
	case capability == "unsupported":
		return baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, Evidence{
			"configured": s.semantic.Configured, "capability": capability,
			"backend": string(backend), "readiness": string(readiness),
		})
	case !invariantValid:
		status = StatusFail
	case readiness == "ready":
		status = StatusPass
	case readiness == "catching_up":
		status = StatusWarn
	}
	return baseCheck(entry, s.now, status, confidence, evidence)
}

func executeSemanticLatest(s *runState, entry RegistryEntry, capability string) Check {
	switch capability {
	case "disabled":
		return baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, Evidence{"refresh_state": "skipped"})
	case "unsupported":
		return baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, Evidence{"refresh_state": "unsupported"})
	}
	if check := semanticActivityUnknown(s, entry, false); check != nil {
		return *check
	}
	latest := s.semantic.Latest
	evidence := semanticLatestEvidence(latest, s.now)
	switch latest.State {
	case "succeeded":
		return baseCheck(entry, s.now, StatusPass, ConfidenceHigh, evidence)
	case "failed", "canceled":
		return baseCheck(entry, s.now, StatusFail, ConfidenceHigh, evidence)
	case "skipped":
		return baseCheck(entry, s.now, StatusWarn, ConfidenceHigh, evidence)
	default:
		return baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, Evidence{"refresh_state": "unknown"})
	}
}

func executeSemanticStages(s *runState, entry RegistryEntry, capability string) Check {
	switch capability {
	case "disabled":
		return baseCheck(entry, s.now, StatusPass, ConfidenceHigh, Evidence{"stages": fixedSemanticStageEvidence("skipped")})
	case "unsupported":
		return baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, Evidence{"stages": fixedSemanticStageEvidence("unknown")})
	}
	if check := semanticActivityUnknown(s, entry, true); check != nil {
		return *check
	}
	return baseCheck(entry, s.now, StatusPass, ConfidenceHigh, Evidence{"stages": semanticStageEvidence(s.semantic.Latest.Stages)})
}

func semanticActivityUnknown(s *runState, entry RegistryEntry, stages bool) *Check {
	evidence := Evidence{"refresh_state": "unknown"}
	if stages {
		evidence = Evidence{"stages": fixedSemanticStageEvidence("unknown")}
	}
	if s.metricsErr != nil {
		check := baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, evidence)
		check.ErrorCode = semanticInspectionErrorCode(s.metricsErr)
		return &check
	}
	if s.metrics.ByteBudgetExhausted || s.metrics.EventBudgetExhausted {
		check := baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, evidence)
		check.ErrorCode = ErrorBudgetExhausted
		return &check
	}
	if s.metrics.ParseErrorCount > 0 {
		check := baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, evidence)
		check.ErrorCode = ErrorParse
		return &check
	}
	if !s.semanticActivityPresent {
		check := baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, evidence)
		check.ErrorCode = ErrorUnavailable
		return &check
	}
	if s.semanticActivityIncomplete {
		check := baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, evidence)
		return &check
	}
	return nil
}

func semanticCapabilityEvidence(snapshot SemanticAuditSnapshot) (string, bool) {
	switch {
	case snapshot.Readiness == "disabled":
		return "disabled", snapshot.Backend == "none"
	case snapshot.CapabilityAvailable:
		return "available", snapshot.Backend == "ollama"
	case snapshot.Backend == "unsupported":
		return "unsupported", true
	default:
		return "unavailable", snapshot.Backend == "ollama" || snapshot.Backend == "none"
	}
}

func attachSemanticActivity(snapshot *SemanticAuditSnapshot, activity metrics.SemanticActivity, now time.Time) (present, incomplete bool) {
	if !activity.Present {
		return false, false
	}
	latest := SemanticRefreshSnapshot{
		State:       SemanticRefreshState(activity.Latest.State),
		StartedAt:   activity.Latest.StartedAt.UTC(),
		CompletedAt: activity.Latest.CompletedAt.UTC(),
		FailureAt:   activity.Latest.FailureAt.UTC(),
		Duration:    activity.Latest.Duration,
		ErrorCode:   SemanticErrorCode(activity.Latest.ErrorCode),
	}
	valid := latest.State.Valid() && latest.Duration >= 0 && semanticRefreshTimestampsValid(latest, now)
	counters := activity.Latest.Counters
	values := []int64{counters.ProjectedParents, counters.EmbeddedChunks, counters.FlushedVectors, counters.CompactedVectors, counters.VerifiedVectors, counters.SuccessorRuns}
	converted := make([]int, len(values))
	for index, value := range values {
		if value < 0 || uint64(value) > uint64(math.MaxInt) {
			valid = false
			continue
		}
		converted[index] = int(value)
	}
	latest.ProjectedParentCount, latest.EmbeddedChunkCount = converted[0], converted[1]
	latest.FlushedVectorCount, latest.CompactedVectorCount = converted[2], converted[3]
	latest.VerifiedVectorCount, latest.SuccessorRunCount = converted[4], converted[5]
	if latest.ErrorCode != "" && !latest.ErrorCode.Valid() {
		valid = false
	}
	latest.Stages, valid = convertSemanticStages(activity.Latest.Stages, valid)
	if !valid {
		latest = SemanticRefreshSnapshot{State: "unknown", Stages: unknownSemanticStageSnapshots()}
	}
	snapshot.Latest = latest
	return true, activity.Incomplete || !valid
}

func convertSemanticStages(values []metrics.SemanticStageRecord, valid bool) ([]SemanticStageSnapshot, bool) {
	if len(values) != len(semanticStageOrder) {
		return unknownSemanticStageSnapshots(), false
	}
	byStage := make(map[SemanticStage]SemanticStageSnapshot, len(values))
	for _, value := range values {
		stage := SemanticStage(value.Stage)
		status := SemanticStageStatus(value.Status)
		if !stage.Valid() || !status.Valid() || value.Duration < 0 {
			valid = false
		}
		if _, duplicate := byStage[stage]; duplicate {
			valid = false
		}
		byStage[stage] = SemanticStageSnapshot{Stage: stage, Status: status, Duration: value.Duration}
	}
	out := make([]SemanticStageSnapshot, 0, len(semanticStageOrder))
	for _, stage := range semanticStageOrder {
		value, ok := byStage[stage]
		if !ok {
			valid = false
			value = SemanticStageSnapshot{Stage: stage, Status: "unknown"}
		}
		out = append(out, value)
	}
	return out, valid
}

func semanticRefreshTimestampsValid(latest SemanticRefreshSnapshot, now time.Time) bool {
	switch latest.State {
	case "succeeded", "skipped", "failed", "canceled":
		if latest.StartedAt.IsZero() || latest.CompletedAt.IsZero() || latest.CompletedAt.Before(latest.StartedAt) || latest.CompletedAt.After(now) {
			return false
		}
	default:
		return true
	}
	if latest.State == "failed" || latest.State == "canceled" {
		return !latest.FailureAt.IsZero() && !latest.FailureAt.Before(latest.StartedAt) && !latest.FailureAt.After(latest.CompletedAt)
	}
	return latest.FailureAt.IsZero()
}

func semanticLatestEvidence(latest SemanticRefreshSnapshot, now time.Time) Evidence {
	evidence := Evidence{
		"refresh_state":          string(latest.State),
		"duration_seconds":       seconds(latest.Duration),
		"projected_parent_count": latest.ProjectedParentCount,
		"embedded_chunk_count":   latest.EmbeddedChunkCount,
		"flushed_vector_count":   latest.FlushedVectorCount,
		"compacted_vector_count": latest.CompactedVectorCount,
		"verified_vector_count":  latest.VerifiedVectorCount,
		"successor_run_count":    latest.SuccessorRunCount,
	}
	if !latest.StartedAt.IsZero() {
		evidence["started_at"] = latest.StartedAt.UTC().Format(time.RFC3339)
	}
	if !latest.CompletedAt.IsZero() {
		evidence["completed_at"] = latest.CompletedAt.UTC().Format(time.RFC3339)
		evidence["age_seconds"] = seconds(now.Sub(latest.CompletedAt))
	}
	if !latest.FailureAt.IsZero() {
		evidence["failure_at"] = latest.FailureAt.UTC().Format(time.RFC3339)
	}
	if latest.ErrorCode.Valid() {
		evidence["semantic_error_code"] = string(latest.ErrorCode)
	}
	return evidence
}

func semanticStageEvidence(stages []SemanticStageSnapshot) []map[string]any {
	out := make([]map[string]any, 0, len(stages))
	for _, stage := range stages {
		out = append(out, map[string]any{"stage": string(stage.Stage), "status": string(stage.Status), "duration_seconds": seconds(stage.Duration)})
	}
	return out
}

func fixedSemanticStageEvidence(status SemanticStageStatus) []map[string]any {
	rows := make([]map[string]any, 0, len(semanticStageOrder))
	for _, stage := range semanticStageOrder {
		rows = append(rows, map[string]any{"stage": string(stage), "status": string(status), "duration_seconds": int64(0)})
	}
	return rows
}

func unknownSemanticStageSnapshots() []SemanticStageSnapshot {
	out := make([]SemanticStageSnapshot, 0, len(semanticStageOrder))
	for _, stage := range semanticStageOrder {
		out = append(out, SemanticStageSnapshot{Stage: stage, Status: "unknown"})
	}
	return out
}

func semanticInspectionErrorCode(err error) ErrorCode {
	switch {
	case errors.Is(err, errCapabilityUnavailable):
		return ErrorUnavailable
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorTimeout
	case errors.Is(err, context.Canceled):
		return ErrorCanceled
	default:
		return ErrorRead
	}
}

func nonnegativeSemanticCount(value int) int {
	return max(value, 0)
}
