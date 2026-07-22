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
	Interrupted        bool       `json:"interrupted"`
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
	AfterSourceKey  string
	Progress        func(ChunkProgress) error
	MaxDuration     time.Duration
	UntilIdle       bool
	Now             func() time.Time
	commandDeadline time.Time
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
	progress := ChunkProgress{Progress: Progress{Stage: "chunk", Snapshots: make([]Progress, 0)}}
	if opts.MaxDuration < 0 {
		return progress, fmt.Errorf("semantic chunk max duration must not be negative")
	}
	workCtx := ctx
	cancel := func() {}
	if opts.MaxDuration > 0 {
		workCtx, cancel = context.WithTimeout(ctx, opts.MaxDuration)
	}
	defer cancel()
	progress, err := runChunkWithLimits(workCtx, st, opts, defaultChunkExecutionLimits, retrievalchunk.DefaultOptions())
	if commandContextInterrupted(ctx, workCtx, opts.MaxDuration, err) {
		progress.Interrupted = true
		progress.HasMore = true
		finalizeChunkAggregate(&progress)
		return progress, nil
	}
	return progress, err
}

func runChunkWithLimits(ctx context.Context, st ChunkStore, opts ChunkOptions, limits chunkExecutionLimits, chunkOpts retrievalchunk.Options) (ChunkProgress, error) {
	if opts.UntilIdle {
		return runChunkUntilIdle(ctx, st, opts, limits, chunkOpts)
	}
	return runChunkWithLimitsAndPlanner(ctx, st, opts, limits, chunkOpts, retrievalchunk.PrepareStreamCommandSessionContext)
}

func runChunkUntilIdle(ctx context.Context, st ChunkStore, opts ChunkOptions, limits chunkExecutionLimits, chunkOpts retrievalchunk.Options) (ChunkProgress, error) {
	progress := ChunkProgress{Progress: Progress{Stage: "chunk", Snapshots: make([]Progress, 0)}}
	if opts.MaxDuration < 0 {
		return progress, fmt.Errorf("semantic chunk max duration must not be negative")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	deadline := opts.commandDeadline
	if deadline.IsZero() && opts.MaxDuration > 0 {
		deadline = now().Add(opts.MaxDuration)
	}
	for {
		if err := ctx.Err(); err != nil {
			return progress, err
		}
		if deadlineReached(deadline, now) {
			progress.Interrupted = true
			progress.HasMore = true
			finalizeChunkAggregate(&progress)
			return progress, nil
		}
		base := progress
		pageOpts := opts
		pageOpts.UntilIdle = false
		pageOpts.MaxDuration = 0
		pageOpts.commandDeadline = deadline
		pageOpts.Progress = func(page ChunkProgress) error {
			if opts.Progress == nil {
				return nil
			}
			return opts.Progress(mergeChunkProgress(base, page))
		}
		page, err := runChunkWithLimitsAndPlanner(ctx, st, pageOpts, limits, chunkOpts, retrievalchunk.PrepareStreamCommandSessionContext)
		progress = mergeChunkProgress(progress, page)
		if err != nil {
			finalizeChunkAggregate(&progress)
			return progress, err
		}
		pending := page.HasMore || page.Checkpoint != nil || page.Remaining > 0
		if deadlineReached(deadline, now) {
			progress.Interrupted = true
			progress.HasMore = true
			finalizeChunkAggregate(&progress)
			return progress, nil
		}
		if pending {
			continue
		}
		if page.Scanned == 0 {
			progress.HasMore = false
			progress.Remaining = 0
			progress.Checkpoint = nil
			finalizeChunkAggregate(&progress)
			return progress, nil
		}
		// A nonempty page can finish without HasMore even if new work arrived
		// after its watermark. Probe once more and stop only on an empty page.
	}
}

func mergeChunkProgress(total, page ChunkProgress) ChunkProgress {
	total.Scanned += page.Scanned
	total.Interrupted = total.Interrupted || page.Interrupted
	total.Current += page.Current
	total.Generated += page.Generated
	total.Quarantined += page.Quarantined
	total.Blocked += page.Blocked
	total.Failed += page.Failed
	total.Created += page.Created
	total.Updated += page.Updated
	total.Deleted += page.Deleted
	total.Remaining = page.Remaining
	total.HasMore = page.HasMore
	total.Checkpoint = page.Checkpoint
	total.SnapshotCount += page.SnapshotCount
	total.SnapshotsTruncated = total.SnapshotCount > 1
	finalizeChunkAggregate(&total)
	return total
}

func finalizeChunkAggregate(progress *ChunkProgress) {
	if progress.SnapshotCount == 0 {
		progress.Snapshots = make([]Progress, 0)
		progress.LastSnapshot = nil
		return
	}
	snapshot := progress.Progress
	snapshot.Snapshots = make([]Progress, 0)
	snapshot.LastSnapshot = nil
	progress.Snapshots = []Progress{snapshot}
	progress.LastSnapshot = &snapshot
}

type chunkPlanPreparer func(context.Context, retrievalchunk.Parent, retrievalchunk.Options, int) (retrievalchunk.PreparedStreamSession, error)

func runChunkWithLimitsAndPlanner(ctx context.Context, st ChunkStore, opts ChunkOptions, limits chunkExecutionLimits, chunkOpts retrievalchunk.Options, prepare chunkPlanPreparer) (ChunkProgress, error) {
	progress := ChunkProgress{Progress: Progress{Stage: "chunk", Snapshots: make([]Progress, 0)}}
	if opts.Limit <= 0 {
		return progress, fmt.Errorf("semantic chunk limit must be positive")
	}
	if opts.MaxDuration < 0 {
		return progress, fmt.Errorf("semantic chunk max duration must not be negative")
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
	deadline := opts.commandDeadline
	if deadline.IsZero() && opts.MaxDuration > 0 {
		deadline = now().Add(opts.MaxDuration)
	}
	for _, selectedWork := range selected {
		if progress.Scanned > 0 && deadlineReached(deadline, now) {
			progress.Interrupted = true
			progress.HasMore = true
			finalizeChunkAggregate(&progress)
			return progress, nil
		}
		if err := ctx.Err(); err != nil {
			return progress, err
		}
		parent := selectedWork.Parent
		progress.Scanned++
		projectionHash, buildErr := retrievalchunk.ParentProjectionHashContext(ctx, parent)
		if buildErr != nil {
			progress.Failed++
			return progress, fmt.Errorf("hash retrieval projection %s %s: %w", parent.Kind, parent.SourceKey, buildErr)
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
			result, paused, err := resumeGiantProjection(ctx, st, parent, selectedWork.DirtyRevision, checkpoint, nil, chunkOpts, limits, deadline, now, &progress, opts.Progress)
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
				if deadlineReached(deadline, now) {
					progress.Interrupted = true
				}
				progress.HasMore = progress.Checkpoint != nil || progress.Remaining > 0
				finalizeChunkAggregate(&progress)
				return progress, nil
			}
			finishChunkProjectionProgress(&progress, result, checkpoint.StagedChunks)
			progress.Remaining--
			if err := recordChunkSnapshot(&progress, opts.Progress); err != nil {
				return progress, err
			}
			continue
		}

		session, err := prepare(ctx, parent, chunkOpts, limits.HardOccurrenceLimit)
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
			progress.Failed++
			return progress, fmt.Errorf("plan retrieval projection %s %s: %w", parent.Kind, parent.SourceKey, err)
		}
		encodedPlan, planDigest, err := session.MarshalPlanBinary()
		if err != nil {
			progress.Failed++
			return progress, err
		}
		projection := retrievalchunk.Projection{ParentHash: projectionHash, Chunks: make([]retrievalchunk.Chunk, 0), Occurrences: make([]retrievalchunk.Occurrence, 0)}
		rows := make([]store.RetrievalProjectionStageRow, 0, limits.GiantThreshold+1)
		seen := make(map[string]struct{}, limits.GiantThreshold+1)
		batchBytes := 0
		cursor, done, streamErr := session.StreamContext(ctx, retrievalchunk.Cursor{}, func(chunk retrievalchunk.Chunk, occurrence retrievalchunk.Occurrence) error {
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
			Cursor: cursor, Rows: rows, PreparedPlan: encodedPlan, PreparedPlanDigest: planDigest,
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
			progress.Interrupted = true
			if err := recordChunkSnapshot(&progress, opts.Progress); err != nil {
				return progress, err
			}
			return progress, nil
		}
		result, paused, err := resumeGiantProjection(ctx, st, parent, selectedWork.DirtyRevision, checkpoint, &session, chunkOpts, limits, deadline, now, &progress, opts.Progress)
		if err != nil {
			progress.Failed++
			return progress, err
		}
		if paused {
			if deadlineReached(deadline, now) {
				progress.Interrupted = true
			}
			progress.HasMore = progress.Checkpoint != nil || progress.Remaining > 0
			finalizeChunkAggregate(&progress)
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

func resumeGiantProjection(ctx context.Context, st ChunkStore, parent retrievalchunk.Parent, dirtyRevision int64, checkpoint store.RetrievalProjectionCheckpoint, preparedSession *retrievalchunk.PreparedStreamSession, chunkOpts retrievalchunk.Options, limits chunkExecutionLimits, deadline time.Time, now func() time.Time, progress *ChunkProgress, callback func(ChunkProgress) error) (store.ChunkReplaceResult, bool, error) {
	if checkpointExceedsProjectionLimits(checkpoint, limits) {
		if err := st.BlockRetrievalProjectionTooLarge(ctx, parent, dirtyRevision, checkpoint.ProjectionHash); err != nil {
			return store.ChunkReplaceResult{}, false, err
		}
		progress.Checkpoint = nil
		progress.Blocked++
		progress.Remaining--
		return store.ChunkReplaceResult{}, true, nil
	}
	var session retrievalchunk.PreparedStreamSession
	var planBytes []byte
	var planDigest string
	var err error
	if preparedSession != nil {
		session = *preparedSession
		planDigest = session.PlanDigest()
		if planDigest == "" {
			_, planDigest, err = session.MarshalPlanBinary()
		}
	} else if checkpoint.PreparedPlan != "" {
		planBytes = []byte(checkpoint.PreparedPlan)
		session, err = retrievalchunk.ParsePreparedStreamSessionContext(ctx, parent, chunkOpts, planBytes, limits.HardOccurrenceLimit)
		planDigest = session.PlanDigest()
	} else {
		session, err = retrievalchunk.PrepareStreamCommandSessionContext(ctx, parent, chunkOpts, limits.HardOccurrenceLimit)
		if err == nil {
			_, planDigest, err = session.MarshalPlanBinary()
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
		cursor, done, err := session.StreamContext(ctx, retrievalchunk.Cursor{SectionKey: checkpoint.SectionKey, NextBoundary: checkpoint.NextBoundary}, func(chunk retrievalchunk.Chunk, occurrence retrievalchunk.Occurrence) error {
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
			Cursor: cursor, Rows: rows, PreparedPlanDigest: planDigest,
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
		reachedDeadline := deadlineReached(deadline, now)
		if reachedDeadline {
			progress.Interrupted = true
		}
		if err := recordChunkSnapshot(progress, callback); err != nil {
			return store.ChunkReplaceResult{}, false, err
		}
		if reachedDeadline {
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

func commandContextInterrupted(parentCtx, workCtx context.Context, maxDuration time.Duration, err error) bool {
	if err == nil || maxDuration <= 0 || parentCtx.Err() != nil {
		return false
	}
	workErr := workCtx.Err()
	return workErr != nil && errors.Is(err, workErr)
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
