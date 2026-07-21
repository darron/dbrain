package semanticbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/store"
)

const maxVerifyPageSize = 5_000

type VerifyOptions struct {
	ProfileID string
	Limit     int
	Resume    string
	Progress  func(VerifyProgress) error
}

type VerifyProgress struct {
	Progress
	Resume  string `json:"resume,omitempty"`
	HasMore bool   `json:"has_more"`
}

type VerifyStore interface {
	ListRetrievalVectors(context.Context, string, store.VectorPage) ([]store.RetrievalVectorRow, error)
	BlockCorruptRetrievalEmbedding(context.Context, *store.RetrievalEmbeddingCorruptionError) error
}

func RunVerify(ctx context.Context, st VerifyStore, opts VerifyOptions) (VerifyProgress, error) {
	progress := VerifyProgress{Progress: Progress{Stage: "verify", Snapshots: make([]Progress, 0)}}
	if strings.TrimSpace(opts.ProfileID) == "" {
		return progress, fmt.Errorf("semantic verify profile is required")
	}
	if opts.Limit <= 0 || opts.Limit > maxVerifyPageSize {
		return progress, fmt.Errorf("semantic verify limit must be between 1 and %d", maxVerifyPageSize)
	}
	if err := ctx.Err(); err != nil {
		return progress, err
	}
	rows, err := st.ListRetrievalVectors(ctx, opts.ProfileID, store.VectorPage{AfterChunkID: opts.Resume, Limit: opts.Limit})
	if err != nil {
		return progress, err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return progress, err
		}
		progress.Scanned++
		progress.Resume = row.ChunkID
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
