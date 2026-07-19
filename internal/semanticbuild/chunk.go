package semanticbuild

import (
	"context"
	"fmt"

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
	Created            int    `json:"created"`
	Updated            int    `json:"updated"`
	Deleted            int    `json:"deleted"`
	NextAfterSourceKey string `json:"next_after_source_key"`
	HasMore            bool   `json:"has_more"`
}

type ChunkOptions struct {
	Limit          int
	AfterSourceKey string
	Progress       func(ChunkProgress) error
}

type ChunkStore interface {
	ListRetrievalParents(context.Context, string, int) ([]retrievalchunk.Parent, error)
	ReplaceRetrievalChunks(context.Context, string, string, []retrievalchunk.Chunk) (store.ChunkReplaceResult, error)
}

func RunChunk(ctx context.Context, st ChunkStore, opts ChunkOptions) (ChunkProgress, error) {
	progress := ChunkProgress{Progress: Progress{Stage: "chunk", Snapshots: make([]Progress, 0)}}
	if opts.Limit <= 0 {
		return progress, fmt.Errorf("semantic chunk limit must be positive")
	}
	parents, err := st.ListRetrievalParents(ctx, opts.AfterSourceKey, opts.Limit+1)
	if err != nil {
		return progress, err
	}
	selected := make([]retrievalchunk.Parent, 0, len(parents))
	selectedKeys := 0
	lastKey := ""
	for _, parent := range parents {
		if parent.SourceKey != lastKey {
			selectedKeys++
			lastKey = parent.SourceKey
		}
		if selectedKeys > opts.Limit {
			progress.HasMore = true
			break
		}
		selected = append(selected, parent)
	}
	progress.Remaining = len(selected)
	for start := 0; start < len(selected); {
		groupKey := selected[start].SourceKey
		end := start + 1
		for end < len(selected) && selected[end].SourceKey == groupKey {
			end++
		}
		for _, parent := range selected[start:end] {
			if err := ctx.Err(); err != nil {
				return progress, err
			}
			progress.Scanned++
			chunks, buildErr := retrievalchunk.Build(parent, retrievalchunk.DefaultOptions())
			if buildErr != nil {
				progress.Blocked++
				progress.Remaining--
				continue
			}
			result, err := st.ReplaceRetrievalChunks(ctx, parent.Kind, parent.SourceKey, chunks)
			if err != nil {
				progress.Failed++
				return progress, err
			}
			progress.Remaining--
			progress.Created += result.Created
			progress.Updated += result.Updated
			progress.Deleted += result.Deleted
			if len(chunks) == 0 {
				progress.Blocked++
			} else if result.Created == 0 && result.Updated == 0 && result.Deleted == 0 {
				progress.Current++
			} else {
				progress.Generated++
			}
		}
		progress.NextAfterSourceKey = groupKey
		if err := recordChunkSnapshot(&progress, opts.Progress); err != nil {
			return progress, err
		}
		start = end
	}
	return progress, nil
}

func recordChunkSnapshot(progress *ChunkProgress, callback func(ChunkProgress) error) error {
	snapshot := progress.Progress
	snapshot.Snapshots = make([]Progress, 0)
	progress.Snapshots = append(progress.Snapshots, snapshot)
	if callback != nil {
		return callback(*progress)
	}
	return nil
}
