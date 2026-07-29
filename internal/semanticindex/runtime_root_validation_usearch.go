//go:build usearch && cgo

package semanticindex

import "context"

// ValidateUSearchRuntimeRoot opens the exact native root that runtime search
// would use, then closes every loaded index before returning.
func ValidateUSearchRuntimeRoot(
	ctx context.Context,
	cacheDir, databaseID, profileID, generationID string,
	dimensions int,
	snapshotRevision, purgeEpoch int64,
	backendVersion, descriptorSHA256 string,
) error {
	root, err := OpenUSearchRoot(ctx, cacheDir, databaseID, profileID, generationID, USearchRootExpectations{
		Index: USearchOptions{
			Dimensions:      dimensions,
			Connectivity:    16,
			ExpansionAdd:    128,
			ExpansionSearch: 256,
		},
		SnapshotRevision: snapshotRevision,
		PurgeEpoch:       purgeEpoch,
		BackendVersion:   backendVersion,
		DescriptorSHA256: descriptorSHA256,
	})
	if err != nil {
		return err
	}
	return root.Close()
}
