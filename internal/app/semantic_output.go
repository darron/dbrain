package app

import (
	"fmt"
	"io"

	"github.com/darron/dbrain/internal/semanticbuild"
)

func writeSemanticStatus(dst io.Writer, status semanticbuild.Status) error {
	if _, err := fmt.Fprintf(dst, "Status: %s\n", status.Status); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Mode: %s\nProfile: %s\n", status.Mode, status.ProfileID); err != nil {
		return err
	}
	if status.Reason != "" {
		if _, err := fmt.Fprintf(dst, "Reason: %s\n", status.Reason); err != nil {
			return err
		}
	}
	if status.Store.Available {
		_, err := fmt.Fprintf(dst, "Chunks: %d\nReady embeddings: %d\nEmbedding candidates: %d\nBlocked embeddings: %d\nFailed embeddings: %d\n",
			status.Store.ChunkCount, status.Store.ReadyEmbeddings, status.Store.EmbeddingCandidates,
			status.Store.BlockedEmbeddings, status.Store.FailedEmbeddings)
		return err
	}
	return nil
}

func writeSemanticProgressSnapshot(dst io.Writer, progress semanticbuild.Progress) error {
	_, err := fmt.Fprintf(dst, "Progress: stage=%s scanned=%d current=%d generated=%d blocked=%d failed=%d remaining=%d\n",
		progress.Stage, progress.Scanned, progress.Current, progress.Generated,
		progress.Blocked, progress.Failed, progress.Remaining)
	return err
}

func writeSemanticProgress(dst io.Writer, progress semanticbuild.Progress) error {
	_, err := fmt.Fprintf(dst, "Stage: %s\nScanned: %d\nCurrent: %d\nGenerated: %d\nBlocked: %d\nFailed: %d\nRemaining: %d\n",
		progress.Stage, progress.Scanned, progress.Current, progress.Generated,
		progress.Blocked, progress.Failed, progress.Remaining)
	return err
}

func writeSemanticChunkProgress(dst io.Writer, progress semanticbuild.ChunkProgress) error {
	if err := writeSemanticProgress(dst, progress.Progress); err != nil {
		return err
	}
	_, err := fmt.Fprintf(dst, "Created: %d\nUpdated: %d\nDeleted: %d\nNext after source key: %s\nHas more: %t\n", progress.Created, progress.Updated, progress.Deleted, progress.NextAfterSourceKey, progress.HasMore)
	return err
}
