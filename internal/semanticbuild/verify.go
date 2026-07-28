package semanticbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/store"
)

const maxVerifyPageSize = 5_000

type VerifyOptions struct {
	Profile        embedding.Profile
	Limit          int
	Resume         string
	RepairCounters bool
	Progress       func(VerifyProgress) error
}

type VerifyProgress struct {
	Progress
	Resume           string `json:"resume,omitempty"`
	HasMore          bool   `json:"has_more"`
	CountersRepaired bool   `json:"counters_repaired"`
}

type VerifyStore interface {
	RepairRetrievalRuntimeReadinessCounters(context.Context) error
	RetrievalEmbeddingVerificationState(context.Context, string) (store.RetrievalEmbeddingVerificationState, error)
	ListRetrievalVectors(context.Context, string, store.VectorPage) ([]store.RetrievalVectorRow, error)
	BlockCorruptRetrievalEmbedding(context.Context, *store.RetrievalEmbeddingCorruptionError) error
}

func RunVerify(ctx context.Context, st VerifyStore, opts VerifyOptions) (VerifyProgress, error) {
	progress := VerifyProgress{Progress: Progress{Stage: "verify", Snapshots: make([]Progress, 0)}}
	profileID, err := opts.Profile.ID()
	if err != nil {
		return progress, fmt.Errorf("semantic verify profile is invalid: %w", err)
	}
	if opts.Limit <= 0 || opts.Limit > maxVerifyPageSize {
		return progress, fmt.Errorf("semantic verify limit must be between 1 and %d", maxVerifyPageSize)
	}
	if err := ctx.Err(); err != nil {
		return progress, err
	}
	if opts.RepairCounters {
		if err := st.RepairRetrievalRuntimeReadinessCounters(ctx); err != nil {
			return progress, err
		}
		progress.CountersRepaired = true
	}
	state, err := st.RetrievalEmbeddingVerificationState(ctx, profileID)
	if err != nil {
		if errors.Is(err, store.ErrRetrievalEmbeddingProfileNotFound) {
			return progress, nil
		}
		return progress, err
	}
	if err := validateVerificationState(profileID, opts.Profile, state); err != nil {
		return progress, err
	}
	rows, err := st.ListRetrievalVectors(ctx, profileID, store.VectorPage{AfterChunkID: opts.Resume, Limit: opts.Limit})
	if err != nil {
		return progress, err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return progress, err
		}
		progress.Scanned++
		progress.Resume = row.ChunkID
		if err := validateVerificationRow(profileID, opts.Profile, state.LatestRevision, row); err != nil {
			return progress, err
		}
		if reason := retrievalVectorCorruptionReason(row); reason != "" {
			corruption := &store.RetrievalEmbeddingCorruptionError{ChunkID: row.ChunkID, ProfileID: row.ProfileID, Reason: reason}
			if err := st.BlockCorruptRetrievalEmbedding(ctx, corruption); err != nil {
				if errors.Is(err, store.ErrRetrievalEmbeddingNoLongerCorrupt) {
					progress.Current++
				} else {
					return progress, err
				}
			} else {
				progress.Quarantined++
			}
		} else {
			progress.Current++
		}
	}
	progress.HasMore = len(rows) == opts.Limit
	if len(rows) > 0 {
		progress.SnapshotCount = 1
		snapshot := progress.Progress
		snapshot.Snapshots = make([]Progress, 0)
		progress.Snapshots = []Progress{snapshot}
		progress.LastSnapshot = &snapshot
		if opts.Progress != nil {
			if err := opts.Progress(progress); err != nil {
				return progress, err
			}
		}
	}
	return progress, nil
}

func validateVerificationState(profileID string, profile embedding.Profile, state store.RetrievalEmbeddingVerificationState) error {
	storedProfileID, err := state.Profile.ID()
	if err != nil {
		return fmt.Errorf("semantic verify stored profile %s is invalid: %w", state.ProfileID, err)
	}
	if state.ProfileID != profileID || storedProfileID != profileID || state.Profile != profile {
		return fmt.Errorf("semantic verify profile definition does not match requested profile %s", profileID)
	}
	if state.PurgeEpoch != state.GlobalPurgeEpoch {
		return fmt.Errorf("semantic verify profile %s purge epoch %d does not match database epoch %d", profileID, state.PurgeEpoch, state.GlobalPurgeEpoch)
	}
	if state.LatestRevision < 0 || state.ActiveSnapshotRevision < 0 || state.ActiveSnapshotRevision > state.LatestRevision {
		return fmt.Errorf("semantic verify profile %s has invalid revision bounds latest=%d snapshot=%d", profileID, state.LatestRevision, state.ActiveSnapshotRevision)
	}
	if state.ActiveIndexedCount < 0 || state.L0ReadyCount < 0 || state.ActiveTombstoneCount < 0 {
		return fmt.Errorf("semantic verify profile %s has negative aggregate counts", profileID)
	}
	if state.ActiveTombstoneCount > state.ActiveIndexedCount {
		return fmt.Errorf("semantic verify profile %s tombstone count %d exceeds active indexed count %d", profileID, state.ActiveTombstoneCount, state.ActiveIndexedCount)
	}
	if state.ActiveGenerationID == "" {
		if state.ActiveSnapshotRevision != 0 || state.ActiveIndexedCount != 0 || state.ActiveTombstoneCount != 0 {
			return fmt.Errorf("semantic verify profile %s has root aggregates without an active generation", profileID)
		}
		return nil
	}
	if !state.GenerationActive || state.GenerationStatus != store.RetrievalGenerationCompleted {
		return fmt.Errorf("semantic verify profile %s active generation %s is not active and completed", profileID, state.ActiveGenerationID)
	}
	if state.GenerationBackend != semanticindex.BackendUSearch ||
		state.GenerationBackendVersion != semanticindex.USearchVersion ||
		state.GenerationDistanceMetric != "cosine" {
		return fmt.Errorf("semantic verify profile %s active generation %s has unsupported backend provenance %q/%q/%q", profileID, state.ActiveGenerationID, state.GenerationBackend, state.GenerationBackendVersion, state.GenerationDistanceMetric)
	}
	if state.GenerationDimensions != profile.Dimensions {
		return fmt.Errorf("semantic verify profile %s active generation %s dimensions %d do not match %d", profileID, state.ActiveGenerationID, state.GenerationDimensions, profile.Dimensions)
	}
	if state.GenerationIndexedChunkCount != state.ActiveIndexedCount {
		return fmt.Errorf("semantic verify profile %s active indexed count %d does not match generation %s indexed count %d", profileID, state.ActiveIndexedCount, state.ActiveGenerationID, state.GenerationIndexedChunkCount)
	}
	return nil
}

func validateVerificationRow(profileID string, profile embedding.Profile, latestRevision int64, row store.RetrievalVectorRow) error {
	if row.ProfileID != profileID || row.Provider != profile.Provider || row.Model != profile.Model ||
		row.Dimensions != profile.Dimensions || row.ProjectionVersion != profile.ProjectionVersion ||
		row.ChunkerVersion != profile.ChunkerVersion || row.Representation != profile.Representation ||
		row.Normalization != profile.Normalization {
		return fmt.Errorf("semantic verify row %s provenance does not match profile %s", row.ChunkID, profileID)
	}
	if row.Revision <= 0 || row.Revision > latestRevision {
		return fmt.Errorf("semantic verify row %s revision %d is outside profile range 1..%d", row.ChunkID, row.Revision, latestRevision)
	}
	return nil
}

func retrievalVectorCorruptionReason(row store.RetrievalVectorRow) string {
	if err := (embedding.Info{Provider: row.Provider, Model: row.Model, Dimensions: row.Dimensions}).Validate(); err != nil {
		return err.Error()
	}
	if row.ChunkTextHash != row.CurrentChunkTextHash {
		return fmt.Sprintf("chunk text hash %q does not match current hash %q", row.ChunkTextHash, row.CurrentChunkTextHash)
	}
	if err := embedding.ValidateEncodedVector(row.VectorBytes, row.Dimensions, row.Representation, row.Normalization); err != nil {
		return err.Error()
	}
	if got := vectorHash(row.VectorBytes); got != row.VectorHash {
		return fmt.Sprintf("vector hash %q does not match stored bytes hash %q", row.VectorHash, got)
	}
	return ""
}

func vectorHash(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
