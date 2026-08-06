//go:build usearch && cgo

package brainresearch

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticlock"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/store"
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

func runtimeSemanticSearcher(ctx context.Context, st *store.Store, cfg config.Config, profile embedding.Profile, snapshot semanticreadiness.Snapshot, _ int) (semanticindex.Searcher, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if snapshot.ActiveGenerationID == "" {
		return semanticindex.NewExact(st), nil
	}
	cacheInfo, err := os.Stat(cfg.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect native semantic cache: %v", errNativeRootArtifactsUnavailable, err)
	}
	if !cacheInfo.IsDir() {
		return nil, fmt.Errorf("%w: native semantic cache is not a directory", errNativeRootArtifactsUnavailable)
	}
	databaseID, err := st.RetrievalDatabaseID(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := semanticlock.NewScope(cfg.CacheDir, databaseID)
	if err != nil {
		return nil, fmt.Errorf("construct native semantic generation lease scope: %w", err)
	}
	admissionCtx, cancelAdmission := context.WithTimeout(ctx, semanticRuntimeAdmissionTimeout)
	defer cancelAdmission()
	generation, err := scope.AcquireGenerationShared(admissionCtx, "owner=runtime-admission\noperation=probe-native-root\n")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: acquire native root generation lease: %v", errSemanticGenerationBusy, err)
		}
		return nil, fmt.Errorf("acquire native root generation lease: %w", err)
	}
	if err := closeRuntimeGenerationLease(generation); err != nil {
		return nil, nativeSemanticLeaseReleaseError{cause: err}
	}
	root, err := openRuntimeUSearchRoot(
		admissionCtx,
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
			DescriptorSHA256: snapshot.ActiveGenerationRootDescriptorSHA256,
		},
	)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: open native semantic root within admission budget: %v", errSemanticGenerationBusy, err)
		}
		return nil, fmt.Errorf("%w: open native semantic root: %w", errNativeRootArtifactsUnavailable, err)
	}
	return semanticindex.NewUSearchCandidateSearcher(root, st), nil
}
