package semanticbuild

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/store"
)

const (
	defaultRetryCooldown = 15 * time.Minute

	MaxEmbeddingBatchSize          = 5_000
	MaxConsecutiveProviderFailures = 3
	DefaultRetryInitialBackoff     = time.Second
	DefaultRetryMaxBackoff         = 30 * time.Second
)

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

type EmbedBatchOptions struct {
	BatchSize           int
	RetryCooldown       time.Duration
	RetryInitialBackoff time.Duration
	RetryMaxBackoff     time.Duration
	Now                 func() time.Time
	Sleep               func(context.Context, time.Duration) error
	BeforeProvider      func(context.Context, int) error

	// The remaining fields bind the existing manual command to this same batch
	// implementation without changing its public Limit/BatchSize semantics.
	afterChunkID                string
	pageLimit                   int
	skipHasMore                 bool
	skipIdleRevision            bool
	persistRetryableImmediately bool
	consecutiveFailures         *int
	afterPhysicalBatch          func(Progress) error
}

type EmbedBatchResult struct {
	Progress
	Revision int64 `json:"revision"`
	HasMore  bool  `json:"has_more"`

	lastChunkID string
}

type EmbedStore interface {
	ListChunksNeedingEmbeddingForProfileAt(context.Context, embedding.Profile, string, int, time.Time) ([]store.RetrievalChunkRow, error)
	CountChunksNeedingEmbeddingForProfileAt(context.Context, embedding.Profile, time.Time) (int, error)
	RetrievalPurgeEpoch(context.Context) (int64, error)
	PutRetrievalEmbeddingBatch(context.Context, store.PutRetrievalEmbeddingBatchInput) (int64, error)
}

type retrievalEmbeddingProfileReader interface {
	RetrievalEmbeddingProfile(context.Context, string) (store.RetrievalEmbeddingProfileRow, error)
}

type embedBatchExecution struct {
	info           embedding.Info
	profile        embedding.Profile
	profileID      string
	purgeEpoch     int64
	now            time.Time
	cooldown       time.Duration
	initialBackoff time.Duration
	maxBackoff     time.Duration
	sleep          func(context.Context, time.Duration) error
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

func RunEmbedBatch(ctx context.Context, st EmbedStore, provider embedding.Provider, opts EmbedBatchOptions) (EmbedBatchResult, error) {
	result := EmbedBatchResult{Progress: Progress{Stage: "embed", Snapshots: make([]Progress, 0)}}
	execution, err := prepareEmbedBatch(ctx, st, provider, opts)
	if err != nil {
		return result, err
	}

	pageLimit := opts.pageLimit
	if pageLimit <= 0 {
		pageLimit = opts.BatchSize
	}
	candidates, err := listEmbedCandidates(ctx, st, execution.profile, opts.afterChunkID, pageLimit, execution.now)
	if err != nil {
		return result, err
	}
	if len(candidates) == 0 {
		if opts.skipIdleRevision {
			return result, nil
		}
		revision, err := currentEmbedRevision(ctx, st, execution.profileID)
		result.Revision = revision
		return result, err
	}
	result.lastChunkID = candidates[len(candidates)-1].ChunkID

	failures := opts.consecutiveFailures
	if failures == nil {
		failures = new(int)
	}
	attempts := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		attempts[candidate.ChunkID] = candidate.AttemptCount
	}

	var processingErr error
	for start := 0; start < len(candidates); start += opts.BatchSize {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		end := min(start+opts.BatchSize, len(candidates))
		batch := candidates[start:end]
		if start == 0 && opts.BeforeProvider != nil {
			if err := opts.BeforeProvider(ctx, len(batch)); err != nil {
				return result, err
			}
		}
		physical := Progress{Stage: "embed", Snapshots: make([]Progress, 0)}
		revision, err := processEmbedBatch(
			ctx, st, provider, execution, batch, attempts, &physical, failures,
			!opts.persistRetryableImmediately,
		)
		mergeEmbedProgress(&result.Progress, physical)
		result.Revision = revision
		if opts.afterPhysicalBatch != nil && physical.Scanned > 0 {
			if callbackErr := opts.afterPhysicalBatch(physical); callbackErr != nil {
				return result, callbackErr
			}
		}
		if err != nil {
			processingErr = err
			break
		}
	}
	if processingErr != nil && !errors.Is(processingErr, ErrEmbedCircuitOpen) {
		return result, processingErr
	}
	if !opts.skipHasMore {
		remaining, err := listEmbedCandidates(ctx, st, execution.profile, "", 1, execution.now)
		if err != nil {
			if processingErr != nil {
				return result, errors.Join(processingErr, err)
			}
			return result, err
		}
		result.HasMore = len(remaining) > 0
		if result.HasMore {
			result.Remaining = 1
		}
	}
	return result, processingErr
}

func prepareEmbedBatch(ctx context.Context, st EmbedStore, provider embedding.Provider, opts EmbedBatchOptions) (embedBatchExecution, error) {
	var execution embedBatchExecution
	if opts.BatchSize <= 0 {
		return execution, fmt.Errorf("semantic embed batch size must be positive")
	}
	if opts.BatchSize > MaxEmbeddingBatchSize {
		return execution, fmt.Errorf("semantic embed batch size must not exceed %d", MaxEmbeddingBatchSize)
	}
	if opts.RetryCooldown < 0 {
		return execution, fmt.Errorf("semantic embed retry cooldown must not be negative")
	}
	if opts.RetryInitialBackoff < 0 {
		return execution, fmt.Errorf("semantic embed retry initial backoff must not be negative")
	}
	if opts.RetryMaxBackoff < 0 {
		return execution, fmt.Errorf("semantic embed retry max backoff must not be negative")
	}
	if opts.RetryMaxBackoff > DefaultRetryMaxBackoff {
		return execution, fmt.Errorf("semantic embed retry max backoff must not exceed %s", DefaultRetryMaxBackoff)
	}
	if st == nil {
		return execution, fmt.Errorf("semantic embed store is required")
	}
	if provider == nil {
		return execution, fmt.Errorf("semantic embedding provider is required")
	}
	if err := ctx.Err(); err != nil {
		return execution, err
	}

	execution.now = time.Now().UTC()
	if opts.Now != nil {
		execution.now = opts.Now().UTC()
	}
	execution.cooldown = opts.RetryCooldown
	if execution.cooldown == 0 {
		execution.cooldown = defaultRetryCooldown
	}
	execution.initialBackoff = opts.RetryInitialBackoff
	if execution.initialBackoff == 0 {
		execution.initialBackoff = DefaultRetryInitialBackoff
	}
	execution.maxBackoff = opts.RetryMaxBackoff
	if execution.maxBackoff == 0 {
		execution.maxBackoff = DefaultRetryMaxBackoff
	}
	execution.sleep = opts.Sleep
	if execution.sleep == nil {
		execution.sleep = sleepContext
	}
	execution.info = provider.Info()
	execution.profile = Profile(execution.info)
	profileID, err := execution.profile.ID()
	if err != nil {
		return embedBatchExecution{}, embedding.FatalConfigError(err)
	}
	execution.profileID = profileID
	execution.purgeEpoch, err = st.RetrievalPurgeEpoch(ctx)
	if err != nil {
		return embedBatchExecution{}, err
	}
	return execution, nil
}

func listEmbedCandidates(ctx context.Context, st EmbedStore, profile embedding.Profile, afterChunkID string, limit int, now time.Time) ([]store.RetrievalChunkRow, error) {
	candidates, err := st.ListChunksNeedingEmbeddingForProfileAt(ctx, profile, afterChunkID, limit, now)
	if err != nil {
		return nil, err
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

func currentEmbedRevision(ctx context.Context, st EmbedStore, profileID string) (int64, error) {
	reader, ok := st.(retrievalEmbeddingProfileReader)
	if !ok {
		return 0, fmt.Errorf("semantic embed store cannot read the current embedding profile revision")
	}
	profile, err := reader.RetrievalEmbeddingProfile(ctx, profileID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrRetrievalEmbeddingProfileNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return profile.LatestRevision, nil
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

	info := provider.Info()
	profile := Profile(info)
	if _, err := profile.ID(); err != nil {
		return progress, embedding.FatalConfigError(err)
	}
	total, err := st.CountChunksNeedingEmbeddingForProfileAt(workCtx, profile, now)
	if err != nil {
		if embedCommandContextInterrupted(ctx, workCtx, opts.MaxDuration, err) {
			return finishEmbedInterrupted(progress), nil
		}
		return progress, err
	}
	progress.Remaining = total
	cooldown := opts.RetryCooldown
	if cooldown <= 0 {
		cooldown = defaultRetryCooldown
	}
	batchSize := min(opts.BatchSize, MaxEmbeddingBatchSize)
	consecutiveRetryableFailures := 0
	afterChunkID := ""

	for {
		if err := ctx.Err(); err != nil {
			return progress, err
		}
		if embedOwnDeadlineReached(ctx, workCtx, cooperativeDeadline, clock) {
			return finishEmbedInterrupted(progress), nil
		}
		batchResult, err := RunEmbedBatch(workCtx, st, provider, EmbedBatchOptions{
			BatchSize:                   batchSize,
			RetryCooldown:               cooldown,
			Now:                         func() time.Time { return now },
			afterChunkID:                afterChunkID,
			pageLimit:                   opts.Limit,
			skipHasMore:                 true,
			skipIdleRevision:            true,
			persistRetryableImmediately: true,
			consecutiveFailures:         &consecutiveRetryableFailures,
			afterPhysicalBatch: func(physical Progress) error {
				mergeEmbedProgress(&progress, physical)
				progress.Remaining = max(total-progress.Scanned, 0)
				return recordEmbedSnapshot(&progress, opts.Progress)
			},
		})
		if batchResult.lastChunkID != "" {
			afterChunkID = batchResult.lastChunkID
		}
		if err != nil {
			if embedCommandContextInterrupted(ctx, workCtx, opts.MaxDuration, err) {
				return finishEmbedInterrupted(progress), nil
			}
			return progress, err
		}
		if batchResult.lastChunkID == "" {
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
		if !opts.UntilIdle {
			finalizeEmbedProgress(&progress)
			return progress, nil
		}
	}
}

func processEmbedBatch(
	ctx context.Context,
	st EmbedStore,
	provider embedding.Provider,
	execution embedBatchExecution,
	batch []store.RetrievalChunkRow,
	attempts map[string]int,
	progress *Progress,
	consecutiveRetryableFailures *int,
	retrySameBatch bool,
) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	texts := make([]string, len(batch))
	for i := range batch {
		texts[i] = batch[i].Text
	}
	req := embedding.Request{Texts: texts, Purpose: embedding.PurposeDocument}

	for {
		for _, candidate := range batch {
			attempts[candidate.ChunkID]++
		}
		response, embedErr := provider.Embed(ctx, req)
		if embedErr == nil {
			*consecutiveRetryableFailures = 0
			return persistReadyEmbedBatch(ctx, st, execution, batch, attempts, req, response, progress)
		}

		// A provider may complete with its own failure at the same instant the
		// command deadline expires. Preserve that independent evidence; only an
		// error attributable to the command-owned context is graceful upstream.
		if ctx.Err() != nil && !errors.Is(embedErr, ctx.Err()) {
			return 0, embedErr
		}
		if embedding.IsFatalConfig(embedErr) || (!embedding.IsRetryable(embedErr) && !embedding.IsBlocked(embedErr)) {
			return 0, embedErr
		}
		if embedding.IsBlocked(embedErr) && len(batch) > 1 {
			*consecutiveRetryableFailures = 0
			middle := len(batch) / 2
			revision, err := processEmbedBatch(
				ctx, st, provider, execution, batch[:middle], attempts, progress,
				consecutiveRetryableFailures, retrySameBatch,
			)
			if err != nil {
				return revision, err
			}
			nextRevision, err := processEmbedBatch(
				ctx, st, provider, execution, batch[middle:], attempts, progress,
				consecutiveRetryableFailures, retrySameBatch,
			)
			if nextRevision > 0 {
				revision = nextRevision
			}
			return revision, err
		}
		if embedding.IsRetryable(embedErr) {
			*consecutiveRetryableFailures++
			if retrySameBatch && *consecutiveRetryableFailures < MaxConsecutiveProviderFailures {
				delay := embedRetryDelay(execution.initialBackoff, execution.maxBackoff, *consecutiveRetryableFailures)
				if err := execution.sleep(ctx, delay); err != nil {
					return 0, err
				}
				continue
			}
			revision, err := persistFailedEmbedBatch(
				ctx, st, execution, batch, attempts, store.RetrievalEmbeddingError, embedErr, progress,
			)
			if err != nil {
				return 0, err
			}
			if *consecutiveRetryableFailures >= MaxConsecutiveProviderFailures {
				return revision, ErrEmbedCircuitOpen
			}
			return revision, nil
		}

		*consecutiveRetryableFailures = 0
		return persistFailedEmbedBatch(
			ctx, st, execution, batch, attempts, store.RetrievalEmbeddingBlocked, embedErr, progress,
		)
	}
}

func persistReadyEmbedBatch(
	ctx context.Context,
	st EmbedStore,
	execution embedBatchExecution,
	batch []store.RetrievalChunkRow,
	attempts map[string]int,
	req embedding.Request,
	response embedding.Response,
	progress *Progress,
) (int64, error) {
	if err := embedding.ValidateResponse(execution.info, req, response); err != nil {
		return 0, embedding.FatalConfigError(err)
	}
	for i, vector := range response.Vectors {
		if err := embedding.ValidateDenseF32(vector, execution.info.Dimensions, embedding.NormalizationL2); err != nil {
			return 0, embedding.FatalConfigError(fmt.Errorf("invalid L2 embedding vector %d: %w", i, err))
		}
	}
	rows := make([]store.RetrievalEmbeddingRow, 0, len(batch))
	for i, candidate := range batch {
		row := embeddingRow(
			candidate, execution.profileID, execution.info, store.RetrievalEmbeddingReady,
			attempts[candidate.ChunkID], embedding.EncodeDenseF32(response.Vectors[i]), execution.now,
		)
		row.EmbeddedAt = execution.now
		rows = append(rows, row)
	}
	revision, err := st.PutRetrievalEmbeddingBatch(ctx, store.PutRetrievalEmbeddingBatchInput{
		Profile: execution.profile, Rows: rows, ExpectedPurgeEpoch: execution.purgeEpoch,
	})
	if err != nil {
		return 0, err
	}
	for range batch {
		progress.Scanned++
		progress.Generated++
	}
	return revision, nil
}

func persistFailedEmbedBatch(
	ctx context.Context,
	st EmbedStore,
	execution embedBatchExecution,
	batch []store.RetrievalChunkRow,
	attempts map[string]int,
	status store.RetrievalEmbeddingStatus,
	embedErr error,
	progress *Progress,
) (int64, error) {
	rows := make([]store.RetrievalEmbeddingRow, 0, len(batch))
	for _, candidate := range batch {
		row := embeddingRow(candidate, execution.profileID, execution.info, status, attempts[candidate.ChunkID], nil, execution.now)
		row.LastError = embedErr.Error()
		if status == store.RetrievalEmbeddingError {
			row.NextAttemptAt = execution.now.Add(execution.cooldown)
		}
		rows = append(rows, row)
	}
	revision, err := st.PutRetrievalEmbeddingBatch(ctx, store.PutRetrievalEmbeddingBatchInput{
		Profile: execution.profile, Rows: rows, ExpectedPurgeEpoch: execution.purgeEpoch,
	})
	if err != nil {
		return 0, err
	}
	for range batch {
		progress.Scanned++
		if status == store.RetrievalEmbeddingBlocked {
			progress.Blocked++
		} else {
			progress.Failed++
		}
	}
	return revision, nil
}

func embedRetryDelay(initial, maximum time.Duration, consecutiveFailures int) time.Duration {
	delay := min(initial, maximum)
	for failure := 1; failure < consecutiveFailures; failure++ {
		if delay >= maximum || delay > maximum-delay {
			return maximum
		}
		delay *= 2
	}
	return min(delay, maximum)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func mergeEmbedProgress(target *Progress, delta Progress) {
	target.Scanned += delta.Scanned
	target.Generated += delta.Generated
	target.Blocked += delta.Blocked
	target.Failed += delta.Failed
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
