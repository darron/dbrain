package semanticbuild

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/store"
)

type Progress struct {
	Stage              string     `json:"stage"`
	Scanned            int        `json:"scanned"`
	Current            int        `json:"current"`
	Generated          int        `json:"generated"`
	Quarantined        int        `json:"quarantined"`
	Blocked            int        `json:"blocked"`
	Failed             int        `json:"failed"`
	Remaining          int        `json:"remaining"`
	SnapshotCount      int        `json:"snapshot_count"`
	SnapshotsTruncated bool       `json:"snapshots_truncated"`
	LastSnapshot       *Progress  `json:"last_snapshot,omitempty"`
	Snapshots          []Progress `json:"-"`
}

type ChunkProgress struct {
	Progress
	Created int `json:"created"`
	Updated int `json:"updated"`
	Deleted int `json:"deleted"`
	// Deprecated: the durable dirty ledger resumes automatically.
	NextAfterSourceKey string                               `json:"next_after_source_key,omitempty"`
	HasMore            bool                                 `json:"has_more"`
	Checkpoint         *store.RetrievalProjectionCheckpoint `json:"checkpoint,omitempty"`
}

type ChunkOptions struct {
	Limit int
	// Deprecated: nonblank source cursors are rejected because the durable dirty
	// ledger provides the resume position.
	AfterSourceKey string
	Progress       func(ChunkProgress) error
	MaxDuration    time.Duration
	Now            func() time.Time
}

type ChunkStore interface {
	ProjectionWorkRevision(context.Context) (int64, error)
	ListDirtyRetrievalParents(context.Context, int64, int) ([]store.RetrievalParentWork, error)
	ApplyRetrievalProjection(context.Context, store.ApplyRetrievalProjectionInput) (store.ChunkReplaceResult, error)
	LoadRetrievalProjectionStaging(context.Context, retrievalchunk.Parent, int64) (store.RetrievalProjectionCheckpoint, bool, error)
	StageRetrievalProjectionBatch(context.Context, store.StageRetrievalProjectionInput) (store.RetrievalProjectionCheckpoint, error)
	PromoteRetrievalProjectionStaging(context.Context, store.RetrievalProjectionCheckpoint) (store.ChunkReplaceResult, error)
	BlockRetrievalProjectionTooLarge(context.Context, retrievalchunk.Parent, int64, string) error
}

type chunkExecutionLimits struct {
	GiantThreshold      int
	StageBatchSize      int
	StageBatchBytes     int
	HardChunkLimit      int
	HardOccurrenceLimit int
	HardStagedBytes     int64
}

var defaultChunkExecutionLimits = chunkExecutionLimits{
	GiantThreshold: 1_000, StageBatchSize: 1_000, StageBatchBytes: 4 << 20,
	HardChunkLimit:      store.MaxRetrievalProjectionChunks,
	HardOccurrenceLimit: store.MaxRetrievalProjectionOccurrences,
	HardStagedBytes:     store.MaxRetrievalProjectionStagedBytes,
}

var errProjectionStageBatchFull = errors.New("retrieval projection stage batch full")

func RunChunk(ctx context.Context, st ChunkStore, opts ChunkOptions) (ChunkProgress, error) {
	return runChunkWithLimits(ctx, st, opts, defaultChunkExecutionLimits, retrievalchunk.DefaultOptions())
}

func runChunkWithLimits(ctx context.Context, st ChunkStore, opts ChunkOptions, limits chunkExecutionLimits, chunkOpts retrievalchunk.Options) (ChunkProgress, error) {
	return runChunkWithLimitsAndPlanner(ctx, st, opts, limits, chunkOpts, retrievalchunk.PrepareStream)
}

type chunkPlanPreparer func(retrievalchunk.Parent, retrievalchunk.Options, int) (retrievalchunk.PreparedStreamPlan, error)

func runChunkWithLimitsAndPlanner(ctx context.Context, st ChunkStore, opts ChunkOptions, limits chunkExecutionLimits, chunkOpts retrievalchunk.Options, prepare chunkPlanPreparer) (ChunkProgress, error) {
	progress := ChunkProgress{Progress: Progress{Stage: "chunk", Snapshots: make([]Progress, 0)}}
	if opts.Limit <= 0 {
		return progress, fmt.Errorf("semantic chunk limit must be positive")
	}
	if limits.StageBatchBytes <= 0 {
		limits.StageBatchBytes = 4 << 20
	}
	if limits.HardOccurrenceLimit <= 0 {
		limits.HardOccurrenceLimit = store.MaxRetrievalProjectionOccurrences
	}
	if limits.HardStagedBytes <= 0 {
		limits.HardStagedBytes = store.MaxRetrievalProjectionStagedBytes
	}
	if limits.GiantThreshold <= 0 || limits.StageBatchSize <= 0 || limits.HardChunkLimit <= limits.GiantThreshold {
		return progress, fmt.Errorf("invalid semantic giant projection limits")
	}
	if strings.TrimSpace(opts.AfterSourceKey) != "" {
		return progress, fmt.Errorf("semantic chunk --after-source-key is no longer supported; rerun without it because the durable dirty queue resumes automatically")
	}
	watermark, err := st.ProjectionWorkRevision(ctx)
	if err != nil {
		return progress, err
	}
	work, err := st.ListDirtyRetrievalParents(ctx, watermark, opts.Limit+1)
	if err != nil {
		return progress, err
	}
	selected := work
	if len(selected) > opts.Limit {
		selected = selected[:opts.Limit]
		progress.HasMore = true
	}
	progress.Remaining = len(selected)
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	var deadline time.Time
	if opts.MaxDuration > 0 {
		deadline = now().Add(opts.MaxDuration)
	}
	for _, selectedWork := range selected {
		if progress.Scanned > 0 && deadlineReached(deadline, now) {
			progress.HasMore = true
			return progress, nil
		}
		if err := ctx.Err(); err != nil {
			return progress, err
		}
		parent := selectedWork.Parent
		progress.Scanned++
		projectionHash, buildErr := retrievalchunk.ParentProjectionHash(parent)
		if buildErr != nil {
			progress.Blocked++
			progress.Remaining--
			continue
		}
		checkpoint, staged, err := st.LoadRetrievalProjectionStaging(ctx, parent, selectedWork.DirtyRevision)
		if err != nil {
			var stale *store.RetrievalProjectionStaleWorkError
			if errors.As(err, &stale) {
				progress.Current++
				progress.Remaining--
				continue
			}
			progress.Failed++
			return progress, err
		}
		if staged {
			if checkpoint.ProjectionHash != projectionHash {
				progress.Failed++
				return progress, fmt.Errorf("loaded retrieval projection checkpoint hash does not match parent")
			}
			result, paused, err := resumeGiantProjection(ctx, st, parent, selectedWork.DirtyRevision, checkpoint, chunkOpts, limits, deadline, now, &progress, opts.Progress)
			if err != nil {
				var stale *store.RetrievalProjectionStaleWorkError
				if errors.As(err, &stale) {
					progress.Current++
					progress.Remaining--
					continue
				}
				progress.Failed++
				return progress, err
			}
			if paused {
				return progress, nil
			}
			finishChunkProjectionProgress(&progress, result, checkpoint.StagedChunks)
			progress.Remaining--
			if err := recordChunkSnapshot(&progress, opts.Progress); err != nil {
				return progress, err
			}
			continue
		}

		plan, err := prepare(parent, chunkOpts, limits.HardOccurrenceLimit)
		if err != nil {
			var occurrenceLimit *retrievalchunk.PreparedStreamOccurrenceLimitError
			if errors.As(err, &occurrenceLimit) {
				if err := st.BlockRetrievalProjectionTooLarge(ctx, parent, selectedWork.DirtyRevision, projectionHash); err != nil {
					progress.Failed++
					return progress, err
				}
				progress.Blocked++
				progress.Remaining--
				continue
			}
			progress.Blocked++
			progress.Remaining--
			continue
		}
		encodedPlan, err := plan.MarshalBinary()
		if err != nil {
			progress.Failed++
			return progress, err
		}
		projection := retrievalchunk.Projection{ParentHash: projectionHash, Chunks: make([]retrievalchunk.Chunk, 0), Occurrences: make([]retrievalchunk.Occurrence, 0)}
		rows := make([]store.RetrievalProjectionStageRow, 0, limits.GiantThreshold+1)
		seen := make(map[string]struct{}, limits.GiantThreshold+1)
		batchBytes := 0
		cursor, done, streamErr := retrievalchunk.StreamPrepared(parent, chunkOpts, plan, retrievalchunk.Cursor{}, func(chunk retrievalchunk.Chunk, occurrence retrievalchunk.Occurrence) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			rows = append(rows, store.RetrievalProjectionStageRow{Chunk: chunk, Occurrence: occurrence})
			batchBytes += stagedProjectionRowBytes(chunk, occurrence)
			projection.Occurrences = append(projection.Occurrences, occurrence)
			if _, ok := seen[chunk.ID]; !ok {
				seen[chunk.ID] = struct{}{}
				projection.Chunks = append(projection.Chunks, chunk)
			}
			if len(seen) > limits.GiantThreshold || len(rows) >= limits.StageBatchSize || batchBytes >= limits.StageBatchBytes {
				return errProjectionStageBatchFull
			}
			return nil
		})
		if streamErr != nil && !errors.Is(streamErr, errProjectionStageBatchFull) {
			if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
				return progress, streamErr
			}
			progress.Blocked++
			progress.Remaining--
			continue
		}
		if done {
			status := store.RetrievalProjectionCurrent
			reason := ""
			if len(projection.Chunks) == 0 {
				status = store.RetrievalProjectionEmpty
				reason = "no_chunkable_content"
			}
			result, err := st.ApplyRetrievalProjection(ctx, store.ApplyRetrievalProjectionInput{
				ParentKind: parent.Kind, ParentSourceKey: parent.SourceKey,
				DirtyRevision: selectedWork.DirtyRevision, Projection: projection,
				Status: status, Reason: reason,
			})
			if err != nil {
				var stale *store.RetrievalProjectionStaleWorkError
				if errors.As(err, &stale) {
					progress.Current++
					progress.Remaining--
					continue
				}
				progress.Failed++
				return progress, err
			}
			progress.Remaining--
			progress.Created += result.Created
			progress.Updated += result.Updated
			progress.Deleted += result.Deleted
			if len(projection.Chunks) == 0 {
				progress.Blocked++
			} else if result.Created == 0 && result.Updated == 0 && result.Deleted == 0 {
				progress.Current++
			} else {
				progress.Generated++
			}
			if err := recordChunkSnapshot(&progress, opts.Progress); err != nil {
				return progress, err
			}
			continue
		}

		checkpoint, err = st.StageRetrievalProjectionBatch(ctx, store.StageRetrievalProjectionInput{
			DirtyRevision: selectedWork.DirtyRevision, ParentKind: parent.Kind,
			ParentSourceKey: parent.SourceKey, ProjectionHash: projectionHash,
			Cursor: cursor, Rows: rows, PreparedPlan: encodedPlan,
		})
		if err != nil {
			progress.Failed++
			return progress, err
		}
		if checkpointExceedsProjectionLimits(checkpoint, limits) {
			if err := st.BlockRetrievalProjectionTooLarge(ctx, parent, selectedWork.DirtyRevision, projectionHash); err != nil {
				progress.Failed++
				return progress, err
			}
			progress.Blocked++
			progress.Remaining--
			if err := recordChunkSnapshot(&progress, opts.Progress); err != nil {
				return progress, err
			}
			continue
		}
		progress.Checkpoint = &checkpoint
		if deadlineReached(deadline, now) {
			if err := recordChunkSnapshot(&progress, opts.Progress); err != nil {
				return progress, err
			}
			return progress, nil
		}
		result, paused, err := resumeGiantProjection(ctx, st, parent, selectedWork.DirtyRevision, checkpoint, chunkOpts, limits, deadline, now, &progress, opts.Progress)
		if err != nil {
			progress.Failed++
			return progress, err
		}
		if paused {
			return progress, nil
		}
		finishChunkProjectionProgress(&progress, result, checkpoint.StagedChunks)
		progress.Remaining--
		if err := recordChunkSnapshot(&progress, opts.Progress); err != nil {
			return progress, err
		}
	}
	return progress, nil
}

func resumeGiantProjection(ctx context.Context, st ChunkStore, parent retrievalchunk.Parent, dirtyRevision int64, checkpoint store.RetrievalProjectionCheckpoint, chunkOpts retrievalchunk.Options, limits chunkExecutionLimits, deadline time.Time, now func() time.Time, progress *ChunkProgress, callback func(ChunkProgress) error) (store.ChunkReplaceResult, bool, error) {
	if checkpointExceedsProjectionLimits(checkpoint, limits) {
		if err := st.BlockRetrievalProjectionTooLarge(ctx, parent, dirtyRevision, checkpoint.ProjectionHash); err != nil {
			return store.ChunkReplaceResult{}, false, err
		}
		progress.Checkpoint = nil
		progress.Blocked++
		progress.Remaining--
		return store.ChunkReplaceResult{}, true, nil
	}
	var plan retrievalchunk.PreparedStreamPlan
	var planBytes []byte
	var err error
	if checkpoint.PreparedPlan != "" {
		planBytes = []byte(checkpoint.PreparedPlan)
		plan, err = retrievalchunk.ParsePreparedStreamPlan(parent, chunkOpts, planBytes, limits.HardOccurrenceLimit)
	} else {
		plan, err = retrievalchunk.PrepareStream(parent, chunkOpts, limits.HardOccurrenceLimit)
		if err == nil {
			planBytes, err = plan.MarshalBinary()
		}
	}
	if err != nil {
		var occurrenceLimit *retrievalchunk.PreparedStreamOccurrenceLimitError
		if errors.As(err, &occurrenceLimit) {
			if blockErr := st.BlockRetrievalProjectionTooLarge(ctx, parent, dirtyRevision, checkpoint.ProjectionHash); blockErr != nil {
				return store.ChunkReplaceResult{}, false, blockErr
			}
			progress.Checkpoint = nil
			progress.Blocked++
			progress.Remaining--
			return store.ChunkReplaceResult{}, true, nil
		}
		return store.ChunkReplaceResult{}, false, err
	}
	for {
		if checkpoint.SectionKey == "" {
			result, err := st.PromoteRetrievalProjectionStaging(ctx, checkpoint)
			var tooLarge *store.RetrievalProjectionTooLargeError
			if errors.As(err, &tooLarge) {
				if blockErr := st.BlockRetrievalProjectionTooLarge(ctx, parent, dirtyRevision, checkpoint.ProjectionHash); blockErr != nil {
					return store.ChunkReplaceResult{}, false, blockErr
				}
				progress.Checkpoint = nil
				progress.Blocked++
				progress.Remaining--
				return store.ChunkReplaceResult{}, true, nil
			}
			if err == nil {
				progress.Checkpoint = nil
			}
			return result, false, err
		}
		rows := make([]store.RetrievalProjectionStageRow, 0, limits.StageBatchSize)
		batchBytes := 0
		cursor, done, err := retrievalchunk.StreamPrepared(parent, chunkOpts, plan, retrievalchunk.Cursor{SectionKey: checkpoint.SectionKey, NextBoundary: checkpoint.NextBoundary}, func(chunk retrievalchunk.Chunk, occurrence retrievalchunk.Occurrence) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			rows = append(rows, store.RetrievalProjectionStageRow{Chunk: chunk, Occurrence: occurrence})
			batchBytes += stagedProjectionRowBytes(chunk, occurrence)
			if len(rows) >= limits.StageBatchSize || batchBytes >= limits.StageBatchBytes {
				return errProjectionStageBatchFull
			}
			return nil
		})
		if err != nil && !errors.Is(err, errProjectionStageBatchFull) {
			return store.ChunkReplaceResult{}, false, err
		}
		if done {
			cursor = retrievalchunk.Cursor{}
		}
		checkpoint, err = st.StageRetrievalProjectionBatch(ctx, store.StageRetrievalProjectionInput{
			WorkID: checkpoint.WorkID, DirtyRevision: dirtyRevision, ParentKind: parent.Kind,
			ParentSourceKey: parent.SourceKey, ProjectionHash: checkpoint.ProjectionHash,
			Cursor: cursor, Rows: rows, PreparedPlan: planBytes,
		})
		if err != nil {
			return store.ChunkReplaceResult{}, false, err
		}
		progress.Checkpoint = &checkpoint
		if checkpointExceedsProjectionLimits(checkpoint, limits) {
			if err := st.BlockRetrievalProjectionTooLarge(ctx, parent, dirtyRevision, checkpoint.ProjectionHash); err != nil {
				return store.ChunkReplaceResult{}, false, err
			}
			progress.Checkpoint = nil
			progress.Blocked++
			progress.Remaining--
			if err := recordChunkSnapshot(progress, callback); err != nil {
				return store.ChunkReplaceResult{}, false, err
			}
			return store.ChunkReplaceResult{}, true, nil
		}
		if err := recordChunkSnapshot(progress, callback); err != nil {
			return store.ChunkReplaceResult{}, false, err
		}
		if deadlineReached(deadline, now) {
			return store.ChunkReplaceResult{}, true, nil
		}
	}
}

func finishChunkProjectionProgress(progress *ChunkProgress, result store.ChunkReplaceResult, chunkCount int) {
	progress.Created += result.Created
	progress.Updated += result.Updated
	progress.Deleted += result.Deleted
	if result.Created == 0 && result.Updated == 0 && result.Deleted == 0 {
		progress.Current++
	} else if chunkCount > 0 {
		progress.Generated++
	}
}

func checkpointExceedsProjectionLimits(checkpoint store.RetrievalProjectionCheckpoint, limits chunkExecutionLimits) bool {
	return checkpoint.StagedChunks > limits.HardChunkLimit ||
		checkpoint.StagedOccurrences > limits.HardOccurrenceLimit ||
		checkpoint.StagedBytes > limits.HardStagedBytes
}

func stagedProjectionRowBytes(chunk retrievalchunk.Chunk, occurrence retrievalchunk.Occurrence) int {
	return len(chunk.ID) + len(chunk.ParentKind) + len(chunk.ParentSourceKey) + len(chunk.EvidenceRole) +
		len(chunk.SectionKey) + len(chunk.Heading) + len(chunk.HeadingHash) + len(chunk.ProjectionVersion) +
		len(chunk.ChunkerVersion) + len(chunk.InputContentHash) + len(chunk.TextHash) + len(chunk.Text) +
		len(occurrence.ChunkID) + len(occurrence.SectionKey) + 256
}

func deadlineReached(deadline time.Time, now func() time.Time) bool {
	return !deadline.IsZero() && !now().Before(deadline)
}

func recordChunkSnapshot(progress *ChunkProgress, callback func(ChunkProgress) error) error {
	progress.SnapshotCount++
	progress.SnapshotsTruncated = progress.SnapshotCount > 1
	snapshot := progress.Progress
	snapshot.Snapshots = make([]Progress, 0)
	snapshot.LastSnapshot = nil
	progress.Snapshots = []Progress{snapshot}
	progress.LastSnapshot = &snapshot
	if callback != nil {
		return callback(*progress)
	}
	return nil
}
