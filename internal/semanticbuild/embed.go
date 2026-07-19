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

type EmbedOptions struct {
	Limit         int
	BatchSize     int
	RetryCooldown time.Duration
	Now           func() time.Time
	Progress      func(Progress) error
}

type EmbedStore interface {
	ListReadyEmbeddings(context.Context, string, int) ([]store.RetrievalEmbeddingRow, error)
	BlockCorruptRetrievalEmbedding(context.Context, *store.RetrievalEmbeddingCorruptionError) error
	ListChunksNeedingEmbeddingAt(context.Context, string, string, int, time.Time) ([]store.RetrievalChunkRow, error)
	CountChunksNeedingEmbeddingAt(context.Context, string, time.Time) (int, error)
	PutRetrievalEmbedding(context.Context, store.RetrievalEmbeddingRow) error
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
	if provider == nil {
		return progress, fmt.Errorf("semantic embedding provider is required")
	}
	if err := ctx.Err(); err != nil {
		return progress, err
	}
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
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
	if err := quarantineCorruptReady(ctx, st, profileID, &progress); err != nil {
		return progress, err
	}
	total, err := st.CountChunksNeedingEmbeddingAt(ctx, profileID, now)
	if err != nil {
		return progress, err
	}
	candidates, err := st.ListChunksNeedingEmbeddingAt(ctx, profileID, "", opts.Limit, now)
	if err != nil {
		return progress, err
	}
	if len(candidates) > opts.Limit {
		candidates = candidates[:opts.Limit]
	}
	progress.Remaining = total
	attempts := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		attempts[candidate.ChunkID] = candidate.AttemptCount
	}
	for start := 0; start < len(candidates); start += opts.BatchSize {
		if err := ctx.Err(); err != nil {
			return progress, err
		}
		end := min(start+opts.BatchSize, len(candidates))
		batch := candidates[start:end]
		if err := processEmbedBatch(ctx, st, provider, info, profileID, batch, attempts, now, cooldown, &progress); err != nil {
			return progress, err
		}
		progress.Remaining = max(total-progress.Scanned, 0)
		if err := recordEmbedSnapshot(&progress, opts.Progress); err != nil {
			return progress, err
		}
	}
	return progress, nil
}

func quarantineCorruptReady(ctx context.Context, st EmbedStore, profileID string, progress *Progress) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := st.ListReadyEmbeddings(ctx, profileID, 0)
		if err == nil {
			return nil
		}
		var corruption *store.RetrievalEmbeddingCorruptionError
		if !errors.As(err, &corruption) {
			return err
		}
		if err := st.BlockCorruptRetrievalEmbedding(ctx, corruption); err != nil {
			if errors.Is(err, store.ErrRetrievalEmbeddingNoLongerCorrupt) {
				continue
			}
			return err
		}
		progress.Quarantined++
	}
}

func processEmbedBatch(ctx context.Context, st EmbedStore, provider embedding.Provider, info embedding.Info, profileID string, batch []store.RetrievalChunkRow, attempts map[string]int, now time.Time, cooldown time.Duration, progress *Progress) error {
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
		if err := ctx.Err(); err != nil {
			return err
		}
		if embedding.IsFatalConfig(embedErr) || (!embedding.IsRetryable(embedErr) && !embedding.IsBlocked(embedErr)) {
			return embedErr
		}
		if embedding.IsBlocked(embedErr) && len(batch) > 1 {
			middle := len(batch) / 2
			if err := processEmbedBatch(ctx, st, provider, info, profileID, batch[:middle], attempts, now, cooldown, progress); err != nil {
				return err
			}
			return processEmbedBatch(ctx, st, provider, info, profileID, batch[middle:], attempts, now, cooldown, progress)
		}
		status := store.RetrievalEmbeddingError
		if embedding.IsBlocked(embedErr) {
			status = store.RetrievalEmbeddingBlocked
		}
		for _, candidate := range batch {
			row := embeddingRow(candidate, profileID, info, status, attempts[candidate.ChunkID], nil, now)
			row.LastError = embedErr.Error()
			if status == store.RetrievalEmbeddingError {
				row.NextAttemptAt = now.Add(cooldown)
			}
			if err := st.PutRetrievalEmbedding(ctx, row); err != nil {
				return err
			}
			progress.Scanned++
			if status == store.RetrievalEmbeddingBlocked {
				progress.Blocked++
			} else {
				progress.Failed++
			}
		}
		return nil
	}
	if err := embedding.ValidateResponse(info, req, response); err != nil {
		return embedding.FatalConfigError(err)
	}
	for i, vector := range response.Vectors {
		if err := embedding.ValidateDenseF32(vector, info.Dimensions, embedding.NormalizationL2); err != nil {
			return embedding.FatalConfigError(fmt.Errorf("invalid L2 embedding vector %d: %w", i, err))
		}
	}
	for i, candidate := range batch {
		row := embeddingRow(candidate, profileID, info, store.RetrievalEmbeddingReady, attempts[candidate.ChunkID], embedding.EncodeDenseF32(response.Vectors[i]), now)
		row.EmbeddedAt = now
		if err := st.PutRetrievalEmbedding(ctx, row); err != nil {
			return err
		}
		progress.Scanned++
		progress.Generated++
	}
	return nil
}

func recordEmbedSnapshot(progress *Progress, callback func(Progress) error) error {
	snapshot := *progress
	snapshot.Snapshots = make([]Progress, 0)
	progress.Snapshots = append(progress.Snapshots, snapshot)
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
