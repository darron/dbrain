package semanticbuild

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/store"
)

const defaultRetryCooldown = 15 * time.Minute
const maxEmbeddingBatchSize = 5_000

var ErrEmbedCircuitOpen = errors.New("semantic embedding circuit opened after three consecutive retryable provider failures")

type EmbedOptions struct {
	Limit         int
	BatchSize     int
	RetryCooldown time.Duration
	UntilIdle     bool
	MaxDuration   time.Duration
	Now           func() time.Time
	Clock         func() time.Time
	Progress      func(Progress) error
}

type EmbedStore interface {
	ListChunksNeedingEmbeddingForProfileAt(context.Context, embedding.Profile, string, int, time.Time) ([]store.RetrievalChunkRow, error)
	CountChunksNeedingEmbeddingForProfileAt(context.Context, embedding.Profile, time.Time) (int, error)
	RetrievalPurgeEpoch(context.Context) (int64, error)
	PutRetrievalEmbeddingBatch(context.Context, store.PutRetrievalEmbeddingBatchInput) (int64, error)
}

func Profile(info embedding.Info) embedding.Profile {
	return embedding.Profile{
		Provider: info.Provider, Model: info.Model, Dimensions: info.Dimensions,
		ProjectionVersion: retrievalchunk.ProjectionVersion,
		ChunkerVersion:    retrievalchunk.Version,
		Representation:    embedding.RepresentationDenseF32,
		Normalization:     embedding.NormalizationL2,
	}
}

func RunEmbed(ctx context.Context, st EmbedStore, provider embedding.Provider, opts EmbedOptions) (Progress, error) {
	progress := Progress{Stage: "embed", Snapshots: make([]Progress, 0)}
	if opts.Limit <= 0 {
		return progress, fmt.Errorf("semantic embed limit must be positive")
	}
	if opts.BatchSize <= 0 {
		return progress, fmt.Errorf("semantic embed batch size must be positive")
	}
	if opts.MaxDuration < 0 {
		return progress, fmt.Errorf("semantic embed max duration must not be negative")
	}
	if provider == nil {
		return progress, fmt.Errorf("semantic embedding provider is required")
	}
	if err := ctx.Err(); err != nil {
		return progress, err
	}
	// Eligibility is frozen for the entire invocation. In particular, a retry
	// written during an until-idle run cannot become eligible again just because
	// a long command crosses its cooldown boundary.
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	var cooperativeDeadline time.Time
	workCtx := ctx
	cancel := func() {}
	if opts.MaxDuration > 0 {
		cooperativeDeadline = clock().Add(opts.MaxDuration)
		workCtx, cancel = context.WithTimeout(ctx, opts.MaxDuration)
	}
	defer cancel()
	cooldown := opts.RetryCooldown
	if cooldown <= 0 {
		cooldown = defaultRetryCooldown
	}
	info := provider.Info()
	profile := Profile(info)
	profileID, err := profile.ID()
	if err != nil {
		return progress, embedding.FatalConfigError(err)
	}
	purgeEpoch, err := st.RetrievalPurgeEpoch(workCtx)
	if err != nil {
		if embedCommandContextInterrupted(ctx, workCtx, opts.MaxDuration, err) {
			return finishEmbedInterrupted(progress), nil
		}
		return progress, err
	}
	total, err := st.CountChunksNeedingEmbeddingForProfileAt(workCtx, profile, now)
	if err != nil {
		if embedCommandContextInterrupted(ctx, workCtx, opts.MaxDuration, err) {
			return finishEmbedInterrupted(progress), nil
		}
		return progress, err
	}
	progress.Remaining = total
	batchSize := min(opts.BatchSize, maxEmbeddingBatchSize)
	consecutiveRetryableFailures := 0
	afterChunkID := ""
	for {
		if err := ctx.Err(); err != nil {
			return progress, err
		}
		if embedOwnDeadlineReached(ctx, workCtx, cooperativeDeadline, clock) {
			return finishEmbedInterrupted(progress), nil
		}
		candidates, err := st.ListChunksNeedingEmbeddingForProfileAt(workCtx, profile, afterChunkID, opts.Limit, now)
		if err != nil {
			if embedCommandContextInterrupted(ctx, workCtx, opts.MaxDuration, err) {
				return finishEmbedInterrupted(progress), nil
			}
			return progress, err
		}
		if len(candidates) > opts.Limit {
			candidates = candidates[:opts.Limit]
		}
		if len(candidates) == 0 {
			if opts.UntilIdle && afterChunkID != "" {
				// Re-probe from the start after reaching the keyset tail. Rows
				// completed by this invocation are no longer eligible, while this
				// catches concurrently-added work whose ID sorts before the tail.
				afterChunkID = ""
				continue
			}
			progress.Remaining = 0
			finalizeEmbedProgress(&progress)
			return progress, nil
		}
		attempts := make(map[string]int, len(candidates))
		for _, candidate := range candidates {
			attempts[candidate.ChunkID] = candidate.AttemptCount
		}
		afterChunkID = candidates[len(candidates)-1].ChunkID
		for start := 0; start < len(candidates); start += batchSize {
			if err := ctx.Err(); err != nil {
				return progress, err
			}
			if embedOwnDeadlineReached(ctx, workCtx, cooperativeDeadline, clock) {
				return finishEmbedInterrupted(progress), nil
			}
			end := min(start+batchSize, len(candidates))
			batch := candidates[start:end]
			beforeScanned := progress.Scanned
			if err := processEmbedBatch(workCtx, st, provider, info, profile, profileID, purgeEpoch, batch, attempts, now, cooldown, &progress, &consecutiveRetryableFailures); err != nil {
				progress.Remaining = max(total-progress.Scanned, 0)
				if progress.Scanned > beforeScanned {
					if snapshotErr := recordEmbedSnapshot(&progress, opts.Progress); snapshotErr != nil {
						return progress, snapshotErr
					}
				}
				if embedCommandContextInterrupted(ctx, workCtx, opts.MaxDuration, err) {
					return finishEmbedInterrupted(progress), nil
				}
				return progress, err
			}
			progress.Remaining = max(total-progress.Scanned, 0)
			if err := recordEmbedSnapshot(&progress, opts.Progress); err != nil {
				return progress, err
			}
		}
		if !opts.UntilIdle {
			finalizeEmbedProgress(&progress)
			return progress, nil
		}
	}
}

func finishEmbedInterrupted(progress Progress) Progress {
	progress.Interrupted = true
	finalizeEmbedProgress(&progress)
	return progress
}

func finalizeEmbedProgress(progress *Progress) {
	if progress.SnapshotCount == 0 {
		progress.Snapshots = make([]Progress, 0)
		progress.LastSnapshot = nil
		return
	}
	snapshot := *progress
	snapshot.Snapshots = make([]Progress, 0)
	snapshot.LastSnapshot = nil
	progress.Snapshots = []Progress{snapshot}
	progress.LastSnapshot = &snapshot
}

func embedOwnDeadlineReached(parentCtx, workCtx context.Context, deadline time.Time, clock func() time.Time) bool {
	if parentCtx.Err() != nil || deadline.IsZero() {
		return false
	}
	return workCtx.Err() != nil || !clock().Before(deadline)
}

func embedCommandContextInterrupted(parentCtx, workCtx context.Context, maxDuration time.Duration, err error) bool {
	return commandContextInterrupted(parentCtx, workCtx, maxDuration, err)
}

func processEmbedBatch(ctx context.Context, st EmbedStore, provider embedding.Provider, info embedding.Info, profile embedding.Profile, profileID string, purgeEpoch int64, batch []store.RetrievalChunkRow, attempts map[string]int, now time.Time, cooldown time.Duration, progress *Progress, consecutiveRetryableFailures *int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	texts := make([]string, len(batch))
	for i := range batch {
		texts[i] = batch[i].Text
		attempts[batch[i].ChunkID]++
	}
	req := embedding.Request{Texts: texts, Purpose: embedding.PurposeDocument}
	response, embedErr := provider.Embed(ctx, req)
	if embedErr != nil {
		// A provider may complete with its own failure at the same instant the
		// command deadline expires. Preserve that independent evidence; only an
		// error attributable to the command-owned context is graceful upstream.
		if ctx.Err() != nil && !errors.Is(embedErr, ctx.Err()) {
			return embedErr
		}
		if embedding.IsFatalConfig(embedErr) || (!embedding.IsRetryable(embedErr) && !embedding.IsBlocked(embedErr)) {
			return embedErr
		}
		if embedding.IsBlocked(embedErr) && len(batch) > 1 {
			*consecutiveRetryableFailures = 0
			middle := len(batch) / 2
			if err := processEmbedBatch(ctx, st, provider, info, profile, profileID, purgeEpoch, batch[:middle], attempts, now, cooldown, progress, consecutiveRetryableFailures); err != nil {
				return err
			}
			return processEmbedBatch(ctx, st, provider, info, profile, profileID, purgeEpoch, batch[middle:], attempts, now, cooldown, progress, consecutiveRetryableFailures)
		}
		status := store.RetrievalEmbeddingError
		if embedding.IsBlocked(embedErr) {
			status = store.RetrievalEmbeddingBlocked
		}
		rows := make([]store.RetrievalEmbeddingRow, 0, len(batch))
		for _, candidate := range batch {
			row := embeddingRow(candidate, profileID, info, status, attempts[candidate.ChunkID], nil, now)
			row.LastError = embedErr.Error()
			if status == store.RetrievalEmbeddingError {
				row.NextAttemptAt = now.Add(cooldown)
			}
			rows = append(rows, row)
		}
		if _, err := st.PutRetrievalEmbeddingBatch(ctx, store.PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: rows, ExpectedPurgeEpoch: purgeEpoch}); err != nil {
			return err
		}
		for range batch {
			progress.Scanned++
			if status == store.RetrievalEmbeddingBlocked {
				progress.Blocked++
			} else {
				progress.Failed++
			}
		}
		if embedding.IsRetryable(embedErr) {
			*consecutiveRetryableFailures++
			if *consecutiveRetryableFailures >= 3 {
				return ErrEmbedCircuitOpen
			}
		} else {
			*consecutiveRetryableFailures = 0
		}
		return nil
	}
	*consecutiveRetryableFailures = 0
	if err := embedding.ValidateResponse(info, req, response); err != nil {
		return embedding.FatalConfigError(err)
	}
	for i, vector := range response.Vectors {
		if err := embedding.ValidateDenseF32(vector, info.Dimensions, embedding.NormalizationL2); err != nil {
			return embedding.FatalConfigError(fmt.Errorf("invalid L2 embedding vector %d: %w", i, err))
		}
	}
	rows := make([]store.RetrievalEmbeddingRow, 0, len(batch))
	for i, candidate := range batch {
		row := embeddingRow(candidate, profileID, info, store.RetrievalEmbeddingReady, attempts[candidate.ChunkID], embedding.EncodeDenseF32(response.Vectors[i]), now)
		row.EmbeddedAt = now
		rows = append(rows, row)
	}
	if _, err := st.PutRetrievalEmbeddingBatch(ctx, store.PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: rows, ExpectedPurgeEpoch: purgeEpoch}); err != nil {
		return err
	}
	for range batch {
		progress.Scanned++
		progress.Generated++
	}
	return nil
}

func recordEmbedSnapshot(progress *Progress, callback func(Progress) error) error {
	progress.SnapshotCount++
	progress.SnapshotsTruncated = progress.SnapshotCount > 1
	snapshot := *progress
	snapshot.Snapshots = make([]Progress, 0)
	snapshot.LastSnapshot = nil
	progress.Snapshots = []Progress{snapshot}
	progress.LastSnapshot = &snapshot
	if callback != nil {
		return callback(snapshot)
	}
	return nil
}

func embeddingRow(candidate store.RetrievalChunkRow, profileID string, info embedding.Info, status store.RetrievalEmbeddingStatus, attempts int, vector []byte, now time.Time) store.RetrievalEmbeddingRow {
	return store.RetrievalEmbeddingRow{
		ChunkID: candidate.ChunkID, ProfileID: profileID,
		Provider: info.Provider, Model: info.Model, Dimensions: info.Dimensions,
		Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2,
		VectorBytes: vector, ChunkTextHash: candidate.ChunkTextHash,
		Status: status, AttemptCount: attempts,
	}
}
