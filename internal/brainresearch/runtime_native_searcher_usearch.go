//go:build usearch && cgo

package brainresearch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/researchsemantic"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticlock"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/semanticruntime"
)

var closeRuntimeGenerationLease = (*semanticlock.Lease).Close
var openRuntimeUSearchRoot = semanticindex.OpenUSearchRoot

type nativeSemanticLeaseReleaseError struct {
	cause error
}

func (e nativeSemanticLeaseReleaseError) Error() string {
	return "release native root generation lease: " + e.cause.Error()
}

func (e nativeSemanticLeaseReleaseError) Unwrap() error {
	return e.cause
}

func (nativeSemanticLeaseReleaseError) semanticLeaseReleaseFailure() {}

type runtimeNativeLazySearcher struct {
	runtime *Runtime
	spec    runtimeRootSpec
}

func runtimeSemanticSearcher(ctx context.Context, runtime *Runtime, profile embedding.Profile, snapshot semanticreadiness.Snapshot, exactMaxChunks int) (semanticindex.Searcher, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if snapshot.ActiveGenerationID == "" {
		return semanticindex.NewExact(runtime.st), nil
	}
	cacheDir := runtime.cfg.CacheDir
	if strings.TrimSpace(cacheDir) == "" {
		resolved, err := config.Load(runtime.cfg.RootDir)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve native semantic cache", errNativeRootArtifactsUnavailable)
		}
		cacheDir = resolved.CacheDir
	}
	canonicalCacheDir, err := canonicalRuntimeCacheDir(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("%w: canonicalize native semantic cache", errNativeRootArtifactsUnavailable)
	}
	return &runtimeNativeLazySearcher{runtime: runtime, spec: runtimeRootSpec{
		cacheDir: canonicalCacheDir, profile: profile, exactMaxChunks: exactMaxChunks,
	}}, nil
}

func (s *runtimeNativeLazySearcher) Search(ctx context.Context, query []float32, options semanticindex.SearchOptions) ([]semanticindex.Hit, semanticindex.Status, error) {
	empty := make([]semanticindex.Hit, 0)
	status := semanticindex.Status{State: semanticindex.StateUnavailable, Backend: semanticindex.BackendUSearch}
	if err := ctx.Err(); err != nil {
		status.Reason = semanticindex.ReasonCanceled
		return empty, status, err
	}
	if s == nil || s.runtime == nil || s.runtime.rootCache == nil {
		status.Reason = semanticindex.ReasonNativeRootArtifactsUnavailable
		return empty, status, nil
	}
	key, err := currentRuntimeRootKey(ctx, s.runtime, s.spec)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			status.Reason = semanticindex.ReasonCanceled
			return empty, status, ctxErr
		}
		status.Reason = semanticindex.ReasonRuntimeReadinessUnavailable
		return empty, status, nil
	}
	status.ProfileID = key.ProfileID
	s.runtime.registerRootSpec(key, s.spec)

	var retain semanticruntime.RetainLoadGuard
	if generation := researchsemantic.GenerationLeaseFromContext(ctx); generation != nil {
		if retainer, ok := generation.(interface {
			Retain() (*semanticlock.Lease, error)
		}); ok {
			retain = func() (func() error, error) {
				retained, err := retainer.Retain()
				if err != nil {
					return nil, err
				}
				return func() error {
					if err := closeRuntimeGenerationLease(retained); err != nil {
						return nativeSemanticLeaseReleaseError{cause: err}
					}
					return nil
				}, nil
			}
		}
	}
	lease, err := s.runtime.rootCache.Acquire(ctx, key, retain)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			status.Reason = semanticindex.ReasonCanceled
			return empty, status, ctxErr
		}
		var releaseFailure semanticLeaseReleaseFailure
		if errors.As(err, &releaseFailure) {
			status.Reason = semanticindex.ReasonSearchError
			return empty, status, err
		}
		var timeout *semanticruntime.LoadWaitTimeoutError
		switch {
		case errors.As(err, &timeout):
			status.Reason = semanticindex.ReasonRootLoadTimeout
		case errors.Is(err, errRuntimeReadinessUnavailable):
			status.Reason = semanticindex.ReasonRuntimeReadinessUnavailable
		default:
			status.Reason = semanticindex.ReasonNativeRootArtifactsUnavailable
		}
		return empty, status, nil
	}
	hits, searched, searchErr := lease.Search(ctx, query, options)
	closeErr := lease.Close()
	if searchErr != nil || closeErr != nil {
		return hits, searched, errors.Join(searchErr, closeErr)
	}
	return hits, searched, nil
}

func runtimeLoadSemanticRoot(ctx context.Context, runtime *Runtime, key semanticruntime.RootKey) (semanticruntime.LoadedSearcher, error) {
	spec, ok := runtime.rootSpec(key)
	if !ok {
		return semanticruntime.LoadedSearcher{}, fmt.Errorf("%w: native root load specification is unavailable", errNativeRootArtifactsUnavailable)
	}
	cacheInfo, err := os.Stat(key.CacheDir)
	if err != nil || !cacheInfo.IsDir() {
		return semanticruntime.LoadedSearcher{}, fmt.Errorf("%w: native semantic cache is unavailable", errNativeRootArtifactsUnavailable)
	}
	root, err := openRuntimeUSearchRoot(ctx, key.CacheDir, key.DatabaseID, key.ProfileID, key.GenerationID, semanticindex.USearchRootExpectations{
		Index: semanticindex.USearchOptions{
			Dimensions: spec.profile.Dimensions, Connectivity: 16, ExpansionAdd: 128, ExpansionSearch: 256,
		},
		SnapshotRevision: key.SnapshotRevision,
		PurgeEpoch:       key.PurgeEpoch,
		BackendVersion:   key.BackendVersion,
		DescriptorSHA256: key.DescriptorSHA256,
	})
	if err != nil {
		return semanticruntime.LoadedSearcher{}, fmt.Errorf("%w: open native semantic root: %w", errNativeRootArtifactsUnavailable, err)
	}
	current, readinessErr := currentRuntimeRootKey(ctx, runtime, spec)
	if readinessErr != nil {
		return semanticruntime.LoadedSearcher{}, errors.Join(fmt.Errorf("%w: validate native root readiness", errRuntimeReadinessUnavailable), root.Close())
	}
	if current != key {
		return semanticruntime.LoadedSearcher{}, errors.Join(fmt.Errorf("%w: loaded native root no longer matches authoritative readiness", errNativeRootArtifactsUnavailable), root.Close())
	}
	searcher := semanticindex.NewUSearchCandidateSearcher(root, runtime.st)
	return semanticruntime.LoadedSearcher{Searcher: searcher, Close: searcher.Close}, nil
}

func currentRuntimeRootKey(ctx context.Context, runtime *Runtime, spec runtimeRootSpec) (semanticruntime.RootKey, error) {
	readinessCtx, cancelReadiness := context.WithTimeout(ctx, semanticRuntimeAdmissionTimeout)
	defer cancelReadiness()
	snapshot, err := runtime.deps.readiness(readinessCtx, runtime.st, spec.profile, spec.exactMaxChunks, time.Now().UTC())
	if err != nil {
		return semanticruntime.RootKey{}, fmt.Errorf("%w: read authoritative semantic readiness: %v", errRuntimeReadinessUnavailable, err)
	}
	snapshot.Configured, snapshot.Enabled = true, true
	snapshot.ExactMaxChunks = spec.exactMaxChunks
	if snapshot.Now.IsZero() {
		snapshot.Now = time.Now().UTC()
	}
	profileID, err := spec.profile.ID()
	if err != nil || snapshot.ProfileID != profileID || !semanticreadiness.Evaluate(snapshot).Searchable ||
		snapshot.ActiveGenerationID == "" || !snapshot.ActiveGenerationValid ||
		snapshot.ActiveGenerationBackend != semanticindex.BackendUSearch ||
		snapshot.ActiveGenerationBackendVersion != semanticindex.USearchVersion ||
		snapshot.ActiveGenerationDimensions != spec.profile.Dimensions ||
		snapshot.ActiveGenerationDistanceMetric != "cosine" ||
		strings.TrimSpace(snapshot.ActiveGenerationRootDescriptorSHA256) == "" {
		return semanticruntime.RootKey{}, fmt.Errorf("%w: semantic readiness does not describe a stable searchable native root", errRuntimeReadinessUnavailable)
	}
	databaseID, err := runtime.st.RetrievalDatabaseID(readinessCtx)
	if err != nil {
		return semanticruntime.RootKey{}, fmt.Errorf("%w: read semantic database identity: %v", errRuntimeReadinessUnavailable, err)
	}
	return semanticruntime.RootKey{
		CacheDir: spec.cacheDir, DatabaseID: databaseID, ProfileID: profileID,
		GenerationID:     snapshot.ActiveGenerationID,
		SnapshotRevision: snapshot.ActiveSnapshotRevision,
		PurgeEpoch:       snapshot.ProfilePurgeEpoch,
		BackendVersion:   snapshot.ActiveGenerationBackendVersion,
		DescriptorSHA256: snapshot.ActiveGenerationRootDescriptorSHA256,
	}, nil
}

func canonicalRuntimeCacheDir(cacheDir string) (string, error) {
	if strings.TrimSpace(cacheDir) == "" {
		return "", errors.New("semantic cache directory is empty")
	}
	absolute, err := filepath.Abs(cacheDir)
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	return filepath.Clean(absolute), nil
}
