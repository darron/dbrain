package semanticbuild

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/store"
)

type Progress struct {
	Stage       string     `json:"stage"`
	Scanned     int        `json:"scanned"`
	Current     int        `json:"current"`
	Generated   int        `json:"generated"`
	Quarantined int        `json:"quarantined"`
	Blocked     int        `json:"blocked"`
	Failed      int        `json:"failed"`
	Remaining   int        `json:"remaining"`
	Snapshots   []Progress `json:"snapshots"`
}

type ChunkProgress struct {
	Progress
	Created int `json:"created"`
	Updated int `json:"updated"`
	Deleted int `json:"deleted"`
	// Deprecated: the durable dirty ledger resumes automatically.
	NextAfterSourceKey string `json:"next_after_source_key,omitempty"`
	HasMore            bool   `json:"has_more"`
}

type ChunkOptions struct {
	Limit int
	// Deprecated: nonblank source cursors are rejected because the durable dirty
	// ledger provides the resume position.
	AfterSourceKey string
	Progress       func(ChunkProgress) error
}

type ChunkStore interface {
	ProjectionWorkRevision(context.Context) (int64, error)
	ListDirtyRetrievalParents(context.Context, int64, int) ([]store.RetrievalParentWork, error)
	ApplyRetrievalProjection(context.Context, store.ApplyRetrievalProjectionInput) (store.ChunkReplaceResult, error)
}

func RunChunk(ctx context.Context, st ChunkStore, opts ChunkOptions) (ChunkProgress, error) {
	progress := ChunkProgress{Progress: Progress{Stage: "chunk", Snapshots: make([]Progress, 0)}}
	if opts.Limit <= 0 {
		return progress, fmt.Errorf("semantic chunk limit must be positive")
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
	for _, selectedWork := range selected {
		if err := ctx.Err(); err != nil {
			return progress, err
		}
		parent := selectedWork.Parent
		progress.Scanned++
		projection, buildErr := retrievalchunk.BuildProjection(parent, retrievalchunk.DefaultOptions())
		if buildErr != nil {
			progress.Blocked++
			progress.Remaining--
			continue
		}
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
	}
	return progress, nil
}

func recordChunkSnapshot(progress *ChunkProgress, callback func(ChunkProgress) error) error {
	snapshot := progress.Progress
	snapshot.Snapshots = make([]Progress, 0)
	progress.Snapshots = []Progress{snapshot}
	if callback != nil {
		return callback(*progress)
	}
	return nil
}
