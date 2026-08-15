//go:build !usearch || !cgo

package brainresearch

import (
	"context"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/semanticruntime"
)

func runtimeSemanticSearcher(_ context.Context, runtime *Runtime, _ embedding.Profile, snapshot semanticreadiness.Snapshot, _ int) (semanticindex.Searcher, error) {
	if snapshot.ActiveGenerationID != "" {
		return nil, errNativeBackendUnavailable
	}
	return semanticindex.NewExact(runtime.st), nil
}

func runtimeLoadSemanticRoot(_ context.Context, runtime *Runtime, key semanticruntime.RootKey) (semanticruntime.LoadedSearcher, error) {
	// Keep the shared runtime/spec seams compiled and linted in CGO-free builds;
	// the default searcher never invokes this loader for a native root.
	runtime.registerRootSpec(key, runtimeRootSpec{})
	spec, _ := runtime.rootSpec(key)
	_, _, _ = spec.cacheDir, spec.profile, spec.exactMaxChunks
	_ = errRuntimeReadinessUnavailable
	return semanticruntime.LoadedSearcher{}, errNativeBackendUnavailable
}
