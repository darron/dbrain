package app

import (
	"context"
	"errors"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/semanticgc"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
)

const defaultSyncSemanticGCLockTimeout = 5 * time.Second

type syncSemanticGCStatus string

const (
	syncSemanticGCStatusOK      syncSemanticGCStatus = "ok"
	syncSemanticGCStatusSkipped syncSemanticGCStatus = "skipped"
	syncSemanticGCStatusError   syncSemanticGCStatus = "error"
)

type syncSemanticGCDeps struct {
	now func() time.Time
	run func(context.Context, config.Config, semanticgc.Options) (semanticgc.Result, syncSemanticGCFailurePhase, error)
}

type syncSemanticGCFailurePhase string

const (
	syncSemanticGCPhaseStoreOpen           syncSemanticGCFailurePhase = "store_open"
	syncSemanticGCPhaseRetrievalDatabaseID syncSemanticGCFailurePhase = "retrieval_database_id"
	syncSemanticGCPhaseCatalogPrune        syncSemanticGCFailurePhase = "catalog_prune"
	syncSemanticGCPhaseFilesystemUnlink    syncSemanticGCFailurePhase = "filesystem_unlink"
	syncSemanticGCPhaseStoreClose          syncSemanticGCFailurePhase = "store_close"
	syncSemanticGCPhaseApply               syncSemanticGCFailurePhase = "semantic_gc_apply"
)

type syncSemanticGCResult struct {
	Status               syncSemanticGCStatus `json:"status"`
	DurationMS           int64                `json:"duration_ms"`
	SkipReason           string               `json:"skip_reason,omitempty"`
	GenerationsPruned    int                  `json:"generations_pruned"`
	SegmentsPruned       int                  `json:"segments_pruned"`
	MemberRowsPruned     int64                `json:"member_rows_pruned"`
	FilesystemCandidates int                  `json:"filesystem_candidates"`
	FilesystemDeleted    int                  `json:"filesystem_deleted"`
	PrunableBytes        int64                `json:"prunable_bytes"`
	DeletedBytes         int64                `json:"deleted_bytes"`
	Error                string               `json:"error,omitempty"`

	Duration time.Duration `json:"-"`
	err      error         `json:"-"`
}

func defaultSyncSemanticGCDeps() syncSemanticGCDeps {
	return syncSemanticGCDeps{
		now: time.Now,
		run: func(ctx context.Context, cfg config.Config, opts semanticgc.Options) (semanticgc.Result, syncSemanticGCFailurePhase, error) {
			st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
			if err != nil {
				return semanticgc.Result{}, syncSemanticGCPhaseStoreOpen, err
			}
			databaseID, err := st.RetrievalDatabaseID(ctx)
			if err != nil {
				return semanticgc.Result{}, syncSemanticGCPhaseRetrievalDatabaseID, errors.Join(err, st.Close())
			}
			result, runErr := semanticgc.Run(ctx, st, cfg.CacheDir, databaseID, opts)
			closeErr := st.Close()
			if runErr != nil {
				phase := syncSemanticGCPhaseCatalogPrune
				if result.Applied {
					phase = syncSemanticGCPhaseFilesystemUnlink
				}
				return result, phase, errors.Join(runErr, closeErr)
			}
			if closeErr != nil {
				return result, syncSemanticGCPhaseStoreClose, closeErr
			}
			return result, "", nil
		},
	}
}

func completeSyncSemanticGCDeps(deps syncSemanticGCDeps) syncSemanticGCDeps {
	defaults := defaultSyncSemanticGCDeps()
	if deps.now == nil {
		deps.now = defaults.now
	}
	if deps.run == nil {
		deps.run = defaults.run
	}
	return deps
}

func maybeRunAutomaticSemanticGC(
	ctx context.Context,
	cfg config.Config,
	enabled bool,
	refresh semanticrefresh.Result,
	refreshErr error,
	deps syncSemanticGCDeps,
) *syncSemanticGCResult {
	if !enabled || refreshErr != nil || refresh.Outcome != semanticrefresh.OutcomeCompleted {
		return nil
	}
	result := runAutomaticSemanticGC(ctx, cfg, deps)
	return &result
}

func runAutomaticSemanticGC(ctx context.Context, cfg config.Config, deps syncSemanticGCDeps) syncSemanticGCResult {
	deps = completeSyncSemanticGCDeps(deps)
	startedAt := deps.now().UTC()
	gcResult, failurePhase, runErr := deps.run(ctx, cfg, semanticgc.Options{
		Now:             startedAt,
		GracePeriod:     defaultSemanticGCGracePeriod,
		RetainPublished: defaultSemanticGCRetainPublished,
		LockTimeout:     defaultSyncSemanticGCLockTimeout,
		Apply:           true,
		Vacuum:          false,
	})
	completedAt := deps.now().UTC()
	if completedAt.Before(startedAt) {
		completedAt = startedAt
	}
	result := syncSemanticGCResult{
		Status:               syncSemanticGCStatusOK,
		Duration:             completedAt.Sub(startedAt),
		GenerationsPruned:    len(gcResult.Catalog.PrunableGenerations),
		SegmentsPruned:       len(gcResult.Catalog.PrunableSegments),
		MemberRowsPruned:     gcResult.Catalog.PrunableMemberRows,
		FilesystemCandidates: len(gcResult.FilesystemArtifacts),
		FilesystemDeleted:    gcResult.DeletedFilesystemDirs,
		PrunableBytes:        gcResult.PrunableBytes,
		DeletedBytes:         gcResult.DeletedFilesystemBytes,
		err:                  runErr,
	}
	result.DurationMS = result.Duration.Milliseconds()
	if runErr == nil {
		return result
	}
	if errors.Is(runErr, context.DeadlineExceeded) {
		result.Status = syncSemanticGCStatusSkipped
		result.SkipReason = "semantic_lease_timeout"
		return result
	}
	if errors.Is(runErr, context.Canceled) {
		result.Status = syncSemanticGCStatusSkipped
		result.SkipReason = "semantic_gc_cancelled"
		return result
	}
	result.Status = syncSemanticGCStatusError
	if failurePhase == "" {
		failurePhase = syncSemanticGCPhaseApply
	}
	result.Error = string(failurePhase)
	return result
}
