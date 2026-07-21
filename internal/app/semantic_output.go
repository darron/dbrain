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
	_, err := fmt.Fprintf(dst, "Progress: stage=%s scanned=%d current=%d generated=%d quarantined=%d blocked=%d failed=%d remaining=%d\n",
		progress.Stage, progress.Scanned, progress.Current, progress.Generated,
		progress.Quarantined, progress.Blocked, progress.Failed, progress.Remaining)
	return err
}

func writeSemanticProgress(dst io.Writer, progress semanticbuild.Progress) error {
	_, err := fmt.Fprintf(dst, "Stage: %s\nScanned: %d\nCurrent: %d\nGenerated: %d\nQuarantined: %d\nBlocked: %d\nFailed: %d\nRemaining: %d\n",
		progress.Stage, progress.Scanned, progress.Current, progress.Generated,
		progress.Quarantined, progress.Blocked, progress.Failed, progress.Remaining)
	return err
}

func writeSemanticVerifyProgress(dst io.Writer, progress semanticbuild.VerifyProgress) error {
	if err := writeSemanticProgress(dst, progress.Progress); err != nil {
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
