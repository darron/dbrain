package app

import (
	"fmt"
	"io"
	"strings"

	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticindex"
)

func writeSemanticStatus(dst io.Writer, status semanticbuild.Status) error {
	if _, err := fmt.Fprintf(dst, "Status: %s\n", status.Status); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Mode: %s\nProfile: %s\n", status.Mode, status.ProfileID); err != nil {
		return err
	}
	if err := writeSemanticBackendCapability(dst, status.BackendCapability); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Searchable: %t\n", status.Searchable); err != nil {
		return err
	}
	if status.Reason != "" {
		if _, err := fmt.Fprintf(dst, "Reason: %s\n", status.Reason); err != nil {
			return err
		}
	}
	if status.Store.Available {
		_, err := fmt.Fprintf(dst, "Parents: expected=%d current=%d empty=%d pending=%d blocked=%d error=%d\nDirty parents: %d\nEstimated not-ready chunks: %d\nChunks: %d\nReady embeddings: %d\nPending embeddings: %d\nBlocked embeddings: %d\nFailed embeddings: %d\nRetries: due=%d scheduled=%d\nIndex: active=%s l0=%d tombstones=%d building=%d stale=%d error=%d\n",
			status.Store.ExpectedParents, status.Store.CurrentParents, status.Store.EmptyParents,
			status.Store.PendingParents, status.Store.BlockedParents, status.Store.ErrorParents,
			status.Store.DirtyParents, status.Store.EstimatedNotReadyChunks, status.Store.ChunkCount,
			status.Store.ReadyEmbeddings, status.Store.PendingEmbeddings, status.Store.BlockedEmbeddings,
			status.Store.ErrorEmbeddings, status.Store.DueRetries, status.Store.ScheduledRetries,
			status.Store.ActiveGenerationID, status.Store.L0ReadyCount, status.Store.ActiveTombstones,
			status.Store.BuildingGenerations, status.Store.StaleGenerations, status.Store.ErrorGenerations)
		return err
	}
	return nil
}

func writeSemanticBackendCapability(dst io.Writer, capability semanticindex.Capability) error {
	if _, err := fmt.Fprintf(dst, "Backend: state=%s", capability.State); err != nil {
		return err
	}
	if capability.Backend != "" {
		if _, err := fmt.Fprintf(dst, " backend=%s", capability.Backend); err != nil {
			return err
		}
	}
	if capability.Version != "" {
		if _, err := fmt.Fprintf(dst, " version=%s", capability.Version); err != nil {
			return err
		}
	}
	if capability.State == semanticindex.CapabilitySupportedBroken {
		_, reason := capability.Admit(capability.Backend, capability.Version)
		reason = strings.TrimPrefix(reason, "native_backend_broken")
		reason = strings.TrimSpace(strings.TrimPrefix(reason, ":"))
		if reason != "" {
			if _, err := fmt.Fprintf(dst, " reason=%s", reason); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(dst)
	return err
}

func writeSemanticProgressSnapshot(dst io.Writer, progress semanticbuild.Progress) error {
	_, err := fmt.Fprintf(dst, "Progress: stage=%s interrupted=%t scanned=%d current=%d generated=%d quarantined=%d blocked=%d failed=%d remaining=%d\n",
		progress.Stage, progress.Interrupted, progress.Scanned, progress.Current, progress.Generated,
		progress.Quarantined, progress.Blocked, progress.Failed, progress.Remaining)
	return err
}

func writeSemanticProgress(dst io.Writer, progress semanticbuild.Progress) error {
	_, err := fmt.Fprintf(dst, "Stage: %s\nInterrupted: %t\nScanned: %d\nCurrent: %d\nGenerated: %d\nQuarantined: %d\nBlocked: %d\nFailed: %d\nRemaining: %d\n",
		progress.Stage, progress.Interrupted, progress.Scanned, progress.Current, progress.Generated,
		progress.Quarantined, progress.Blocked, progress.Failed, progress.Remaining)
	return err
}

func writeSemanticVerifyProgress(dst io.Writer, progress semanticbuild.VerifyProgress) error {
	if err := writeSemanticProgress(dst, progress.Progress); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Counters repaired: %t\n", progress.CountersRepaired); err != nil {
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
