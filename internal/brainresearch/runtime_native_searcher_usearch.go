//go:build usearch && cgo

package brainresearch

import (
	"context"
	"fmt"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/store"
)

func runtimeSemanticSearcher(ctx context.Context, st *store.Store, cfg config.Config, profile embedding.Profile, snapshot semanticreadiness.Snapshot, _ int) (semanticindex.Searcher, error) {
	if snapshot.ActiveGenerationID == "" {
		return semanticindex.NewExact(st), nil
	}
	databaseID, err := st.RetrievalDatabaseID(ctx)
	if err != nil {
		return nil, err
	}
	root, err := semanticindex.OpenUSearchRoot(
		cfg.CacheDir,
		databaseID,
		snapshot.ProfileID,
		snapshot.ActiveGenerationID,
		semanticindex.USearchRootExpectations{
			Index: semanticindex.USearchOptions{
				Dimensions:      profile.Dimensions,
				Connectivity:    16,
				ExpansionAdd:    128,
				ExpansionSearch: 256,
			},
			SnapshotRevision: snapshot.ActiveSnapshotRevision,
			PurgeEpoch:       snapshot.ProfilePurgeEpoch,
			BackendVersion:   snapshot.ActiveGenerationBackendVersion,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("open native semantic root: %w", err)
	}
	return semanticindex.NewUSearchCandidateSearcher(root, st), nil
}
