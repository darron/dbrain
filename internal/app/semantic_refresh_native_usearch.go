//go:build usearch && cgo

package app

import (
	"context"

	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticrefresh"
)

type usearchRefreshLifecycle struct {
	*semanticbuild.USearchSegmentBuilder
}

func newSemanticNativeLifecycle(cfg semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
	builder, err := semanticbuild.NewUSearchSegmentBuilder(
		semanticbuild.USearchSegmentBuilderOptions{Dimensions: cfg.Dimensions},
	)
	if err != nil {
		return nil, err
	}
	return &usearchRefreshLifecycle{USearchSegmentBuilder: builder}, nil
}

func (l *usearchRefreshLifecycle) VerifyRoot(
	ctx context.Context,
	expect semanticrefresh.RootExpectation,
) error {
	root, err := semanticindex.OpenUSearchRoot(
		ctx,
		expect.CacheDir,
		expect.DatabaseID,
		expect.ProfileID,
		expect.GenerationID,
		semanticindex.USearchRootExpectations{
			Index: semanticindex.USearchOptions{
				Dimensions:      expect.Dimensions,
				Connectivity:    16,
				ExpansionAdd:    128,
				ExpansionSearch: 256,
			},
			SnapshotRevision: expect.SnapshotRevision,
			PurgeEpoch:       expect.PurgeEpoch,
			BackendVersion:   expect.BackendVersion,
			DescriptorSHA256: expect.DescriptorSHA256,
		},
	)
	if err != nil {
		return err
	}
	return root.Close()
}
