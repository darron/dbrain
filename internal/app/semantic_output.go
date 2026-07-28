package app

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
)

func writeSemanticStatus(dst io.Writer, status semanticbuild.Status) error {
	if _, err := fmt.Fprintf(dst, "Status: %s\n", status.Status); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Mode: %s\nProfile: %s\n", status.Mode, status.ProfileID); err != nil {
		return err
	}
	if err := writeSemanticBackendCapability(dst, status.BackendCapability); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Searchable: %t\n", status.Searchable); err != nil {
		return err
	}
	if status.Reason != "" {
		if _, err := fmt.Fprintf(dst, "Reason: %s\n", status.Reason); err != nil {
			return err
		}
	}
	if err := writeSemanticRefreshStatus(dst, status.LatestRun); err != nil {
		return err
	}
	if status.Store.Available {
		_, err := fmt.Fprintf(dst, "Parents: expected=%d current=%d empty=%d pending=%d blocked=%d error=%d\nDirty parents: %d\nEstimated not-ready chunks: %d\nChunks: %d\nReady embeddings: %d\nPending embeddings: %d\nBlocked embeddings: %d\nFailed embeddings: %d\nRetries: due=%d scheduled=%d\nIndex: active=%s l0=%d tombstones=%d building=%d stale=%d error=%d\n",
			status.Store.ExpectedParents, status.Store.CurrentParents, status.Store.EmptyParents,
			status.Store.PendingParents, status.Store.BlockedParents, status.Store.ErrorParents,
			status.Store.DirtyParents, status.Store.EstimatedNotReadyChunks, status.Store.ChunkCount,
			status.Store.ReadyEmbeddings, status.Store.PendingEmbeddings, status.Store.BlockedEmbeddings,
			status.Store.ErrorEmbeddings, status.Store.DueRetries, status.Store.ScheduledRetries,
			status.Store.ActiveGenerationID, status.Store.L0ReadyCount, status.Store.ActiveTombstones,
			status.Store.BuildingGenerations, status.Store.StaleGenerations, status.Store.ErrorGenerations)
		return err
	}
	return nil
}

func writeSemanticRefreshStatus(dst io.Writer, run *store.SemanticRefreshRun) error {
	if run == nil {
		return nil
	}
	if _, err := fmt.Fprintf(
		dst,
		"Refresh: run=%s state=%s stage=%s watermark=%d embedding_revision=%d last_progress=%s readiness=%s\n",
		safeSemanticRefreshOutputField(run.RunID, 64),
		run.State,
		run.Stage,
		run.ProjectionWatermark,
		run.EmbeddingRevision,
		run.LastProgressAt.UTC().Format(time.RFC3339),
		safeSemanticRefreshOutputField(run.ReadinessState, 64),
	); err != nil {
		return err
	}
	if run.ErrorCode == "" {
		return nil
	}
	_, err := fmt.Fprintf(
		dst,
		"Refresh error: code=%s checkpoint=%s\n",
		safeSemanticRefreshOutputField(run.ErrorCode, 64),
		safeSemanticRefreshOutputField(run.Checkpoint, 256),
	)
	return err
}

func writeSemanticRefreshProgress(dst io.Writer, progress semanticrefresh.Progress) error {
	_, err := fmt.Fprintf(
		dst,
		"Semantic refresh progress: run=%s profile=%s stage=%s checkpoint=%s readiness=%s projected_parents=%d embedded_chunks=%d flushed_vectors=%d compacted_vectors=%d verified_vectors=%d successor_runs=%d dirty_parents=%d pending_embeddings=%d due_retries=%d scheduled_retries=%d blocked_embeddings=%d failed_embeddings=%d l0=%d tombstones=%d segments=%d at=%s\n",
		safeSemanticRefreshOutputField(progress.RunID, 64),
		safeSemanticRefreshOutputField(progress.ProfileID, 192),
		progress.Stage,
		safeSemanticRefreshOutputField(progress.Checkpoint, 256),
		safeSemanticRefreshOutputField(progress.Readiness, 64),
		progress.Counters.ProjectedParents,
		progress.Counters.EmbeddedChunks,
		progress.Counters.FlushedVectors,
		progress.Counters.CompactedVectors,
		progress.Counters.VerifiedVectors,
		progress.Counters.SuccessorRuns,
		progress.Debt.DirtyParents,
		progress.Debt.PendingEmbeddings,
		progress.Debt.DueRetries,
		progress.Debt.ScheduledRetries,
		progress.Debt.BlockedEmbeddings,
		progress.Debt.FailedEmbeddings,
		progress.Debt.L0Ready,
		progress.Debt.Tombstones,
		progress.Debt.Segments,
		progress.At.UTC().Format(time.RFC3339),
	)
	return err
}

func writeSemanticRefreshResult(
	dst io.Writer,
	result semanticrefresh.Result,
	elapsed time.Duration,
) error {
	if result.Outcome == semanticrefresh.OutcomeSkipped {
		_, err := fmt.Fprintf(
			dst,
			"Semantic refresh: skipped reason=%s capability=%s backend=%s version=%s elapsed=%s\n",
			safeSemanticRefreshOutputField(result.SkipReason, 64),
			result.Capability.State,
			safeSemanticRefreshOutputField(result.Capability.Backend, 64),
			safeSemanticRefreshOutputField(result.Capability.Version, 64),
			semanticRefreshElapsed(elapsed),
		)
		return err
	}

	run := result.Run
	if run == nil {
		return fmt.Errorf("semantic refresh completed without a run")
	}
	if _, err := fmt.Fprintf(
		dst,
		"Semantic refresh: completed capability=%s backend=%s version=%s run=%s profile=%s generation=%s state=%s stage=%s readiness=%s elapsed=%s\n",
		result.Capability.State,
		safeSemanticRefreshOutputField(result.Capability.Backend, 64),
		safeSemanticRefreshOutputField(result.Capability.Version, 64),
		safeSemanticRefreshOutputField(run.RunID, 64),
		safeSemanticRefreshOutputField(run.ProfileID, 192),
		safeSemanticRefreshOutputField(run.CurrentGenerationID, 64),
		run.State,
		run.Stage,
		safeSemanticRefreshOutputField(run.ReadinessState, 64),
		semanticRefreshElapsed(elapsed),
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(
		dst,
		"Semantic refresh counts: projected_parents=%d embedded_chunks=%d flushed_vectors=%d compacted_vectors=%d verified_vectors=%d successor_runs=%d\n",
		run.Counters.ProjectedParents,
		run.Counters.EmbeddedChunks,
		run.Counters.FlushedVectors,
		run.Counters.CompactedVectors,
		run.Counters.VerifiedVectors,
		run.Counters.SuccessorRuns,
	); err != nil {
		return err
	}
	_, err := fmt.Fprintf(
		dst,
		"Semantic refresh debt: dirty_parents=%d pending_embeddings=%d due_retries=%d scheduled_retries=%d blocked_embeddings=%d failed_embeddings=%d indexed=%d l0=%d tombstones=%d segments=%d\n",
		result.Debt.DirtyParents,
		result.Debt.PendingEmbeddings,
		result.Debt.DueRetries,
		result.Debt.ScheduledRetries,
		result.Debt.BlockedEmbeddings,
		result.Debt.FailedEmbeddings,
		run.Counters.VerifiedVectors,
		result.Debt.L0Ready,
		result.Debt.Tombstones,
		result.Debt.Segments,
	)
	return err
}

func writeSemanticRefreshError(
	dst io.Writer,
	refreshErr *semanticrefresh.RefreshError,
	elapsed time.Duration,
) error {
	if refreshErr == nil {
		return fmt.Errorf("semantic refresh error is unavailable")
	}
	_, err := fmt.Fprintf(
		dst,
		"Semantic refresh failed: code=%s run=%s stage=%s checkpoint=%s readiness=%s dirty_parents=%d pending_embeddings=%d due_retries=%d scheduled_retries=%d blocked_embeddings=%d failed_embeddings=%d l0=%d tombstones=%d segments=%d elapsed=%s\n",
		safeSemanticRefreshOutputField(refreshErr.Code, 64),
		safeSemanticRefreshOutputField(refreshErr.RunID, 64),
		refreshErr.Stage,
		safeSemanticRefreshOutputField(refreshErr.Checkpoint, 256),
		safeSemanticRefreshOutputField(refreshErr.Readiness, 64),
		refreshErr.Debt.DirtyParents,
		refreshErr.Debt.PendingEmbeddings,
		refreshErr.Debt.DueRetries,
		refreshErr.Debt.ScheduledRetries,
		refreshErr.Debt.BlockedEmbeddings,
		refreshErr.Debt.FailedEmbeddings,
		refreshErr.Debt.L0Ready,
		refreshErr.Debt.Tombstones,
		refreshErr.Debt.Segments,
		semanticRefreshElapsed(elapsed),
	)
	return err
}

func boundedSemanticRefreshResult(result semanticrefresh.Result) semanticrefresh.Result {
	if result.Run == nil {
		return result
	}
	run := *result.Run
	run.RunID = safeSemanticRefreshOutputField(run.RunID, 64)
	run.ProfileID = safeSemanticRefreshOutputField(run.ProfileID, 192)
	run.Checkpoint = safeSemanticRefreshOutputField(run.Checkpoint, 256)
	run.CurrentGenerationID = safeSemanticRefreshOutputField(run.CurrentGenerationID, 64)
	run.ErrorCode = safeSemanticRefreshOutputField(run.ErrorCode, 64)
	run.ErrorText = ""
	run.ReadinessState = safeSemanticRefreshOutputField(run.ReadinessState, 64)
	result.Run = &run
	return result
}

func safeSemanticRefreshOutputField(value string, limit int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || len(value) > limit {
		return ""
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case strings.ContainsRune("._:=+-", character):
		default:
			return ""
		}
	}
	return value
}

func semanticRefreshElapsed(elapsed time.Duration) time.Duration {
	if elapsed < 0 {
		return 0
	}
	return elapsed.Round(time.Millisecond)
}

func writeSemanticBackendCapability(dst io.Writer, capability semanticindex.Capability) error {
	if _, err := fmt.Fprintf(dst, "Backend: state=%s", capability.State); err != nil {
		return err
	}
	if capability.Backend != "" {
		if _, err := fmt.Fprintf(dst, " backend=%s", capability.Backend); err != nil {
			return err
		}
	}
	if capability.Version != "" {
		if _, err := fmt.Fprintf(dst, " version=%s", capability.Version); err != nil {
			return err
		}
	}
	if capability.State == semanticindex.CapabilitySupportedBroken {
		_, reason := capability.Admit(capability.Backend, capability.Version)
		reason = strings.TrimPrefix(reason, "native_backend_broken")
		reason = strings.TrimSpace(strings.TrimPrefix(reason, ":"))
		if reason != "" {
			if _, err := fmt.Fprintf(dst, " reason=%s", reason); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(dst)
	return err
}

func writeSemanticProgressSnapshot(dst io.Writer, progress semanticbuild.Progress) error {
	_, err := fmt.Fprintf(dst, "Progress: stage=%s interrupted=%t scanned=%d current=%d generated=%d quarantined=%d blocked=%d failed=%d remaining=%d\n",
		progress.Stage, progress.Interrupted, progress.Scanned, progress.Current, progress.Generated,
		progress.Quarantined, progress.Blocked, progress.Failed, progress.Remaining)
	return err
}

func writeSemanticProgress(dst io.Writer, progress semanticbuild.Progress) error {
	_, err := fmt.Fprintf(dst, "Stage: %s\nInterrupted: %t\nScanned: %d\nCurrent: %d\nGenerated: %d\nQuarantined: %d\nBlocked: %d\nFailed: %d\nRemaining: %d\n",
		progress.Stage, progress.Interrupted, progress.Scanned, progress.Current, progress.Generated,
		progress.Quarantined, progress.Blocked, progress.Failed, progress.Remaining)
	return err
}

func writeSemanticVerifyProgress(dst io.Writer, progress semanticbuild.VerifyProgress) error {
	if err := writeSemanticProgress(dst, progress.Progress); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Counters repaired: %t\n", progress.CountersRepaired); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Has more: %t\n", progress.HasMore); err != nil {
		return err
	}
	if progress.Resume != "" {
		_, err := fmt.Fprintf(dst, "Resume: %s\n", progress.Resume)
		return err
	}
	return nil
}

func writeSemanticChunkProgress(dst io.Writer, progress semanticbuild.ChunkProgress) error {
	if err := writeSemanticProgress(dst, progress.Progress); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Created: %d\nUpdated: %d\nDeleted: %d\nHas more: %t\n", progress.Created, progress.Updated, progress.Deleted, progress.HasMore); err != nil {
		return err
	}
	if progress.HasMore {
		_, err := fmt.Fprintln(dst, "Resume: rerun semantic chunk without a cursor; the durable dirty queue resumes automatically.")
		return err
	}
	return nil
}
