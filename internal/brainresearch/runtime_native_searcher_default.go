//go:build !usearch || !cgo

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

func runtimeSemanticSearcher(_ context.Context, st *store.Store, _ config.Config, _ embedding.Profile, snapshot semanticreadiness.Snapshot, _ int) (semanticindex.Searcher, error) {
	if snapshot.ActiveGenerationID != "" {
		return nil, fmt.Errorf("optional native semantic backend is unavailable")
	}
	return semanticindex.NewExact(st), nil
}
