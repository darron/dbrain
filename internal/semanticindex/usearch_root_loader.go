//go:build usearch && cgo

package semanticindex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/darron/dbrain/internal/semanticsegment"
)

// USearchRoot is a verified, closeable native view of one immutable root. It
// is intentionally not a serving searcher; callers still need authoritative
// SQLite validation and exact reranking before exposing candidates.
type USearchRoot struct {
	Root     semanticsegment.Root
	Segments []USearchRootSegment
}

type USearchRootSegment struct {
	SegmentHash string
	Manifest    semanticsegment.Manifest
	Index       *USearch
}

type USearchRootExpectations struct {
	Index            USearchOptions
	SnapshotRevision int64
	PurgeEpoch       int64
	BackendVersion   string
}

// USearchRootCandidate resolves one approximate native ordinal through the
// immutable member manifest. It is only a candidate identity: callers must
// still validate it against current SQLite state and exactly rerank the vector
// before returning evidence.
type USearchRootCandidate struct {
	SegmentHash         string
	Member              semanticsegment.Member
	ApproximateDistance float32
}

func OpenUSearchRoot(cacheDir, databaseID, profileID, generationID string, expect USearchRootExpectations) (*USearchRoot, error) {
	root, err := semanticsegment.OpenRoot(cacheDir, databaseID, profileID, generationID)
	if err != nil {
		return nil, fmt.Errorf("open usearch root: %w", err)
	}
	if root.Manifest.SnapshotRevision != expect.SnapshotRevision {
		return nil, fmt.Errorf("usearch root snapshot revision mismatch: cache=%d expected=%d", root.Manifest.SnapshotRevision, expect.SnapshotRevision)
	}
	if root.Manifest.PurgeEpoch != expect.PurgeEpoch {
		return nil, fmt.Errorf("usearch root purge epoch mismatch: cache=%d expected=%d", root.Manifest.PurgeEpoch, expect.PurgeEpoch)
	}
	loaded := &USearchRoot{Root: root, Segments: make([]USearchRootSegment, 0, len(root.Manifest.Segments))}
	fail := func(err error) (*USearchRoot, error) { _ = loaded.Close(); return nil, err }
	for _, reference := range root.Manifest.Segments {
		segment, err := semanticsegment.OpenSegment(cacheDir, databaseID, profileID, reference.Hash)
		if err != nil {
			return fail(fmt.Errorf("open usearch root segment %s: %w", reference.Hash, err))
		}
		if segment.Manifest.Backend != BackendUSearch {
			return fail(fmt.Errorf("usearch root segment %s backend mismatch: cache=%q expected=%q", reference.Hash, segment.Manifest.Backend, BackendUSearch))
		}
		if segment.Manifest.BackendVersion != expect.BackendVersion {
			return fail(fmt.Errorf("usearch root segment %s backend version mismatch: cache=%q expected=%q", reference.Hash, segment.Manifest.BackendVersion, expect.BackendVersion))
		}
		if segment.Manifest.DistanceMetric != "cosine" {
			return fail(fmt.Errorf("usearch root segment %s distance metric mismatch: cache=%q expected=%q", reference.Hash, segment.Manifest.DistanceMetric, "cosine"))
		}
		if segment.Manifest.Dimensions != expect.Index.Dimensions {
			return fail(fmt.Errorf("usearch root segment %s dimensions mismatch: cache=%d expected=%d", reference.Hash, segment.Manifest.Dimensions, expect.Index.Dimensions))
		}
		payload, err := os.ReadFile(filepath.Join(cacheDir, filepath.FromSlash(reference.RelativePath), semanticsegment.PayloadFileName))
		if err != nil {
			return fail(fmt.Errorf("read usearch root payload %s: %w", reference.Hash, err))
		}
		index, err := NewUSearch(expect.Index)
		if err != nil {
			return fail(err)
		}
		if err := index.Import(bytes.NewReader(payload)); err != nil {
			_ = index.Close()
			return fail(fmt.Errorf("import usearch root segment %s: %w", reference.Hash, err))
		}
		loaded.Segments = append(loaded.Segments, USearchRootSegment{SegmentHash: reference.Hash, Manifest: segment.Manifest, Index: index})
	}
	return loaded, nil
}

// Candidates searches each verified segment independently and converts every
// native ordinal into its immutable member provenance. The per-segment limit
// deliberately overfetches before later SQLite validation and exact reranking;
// native ordering is never exposed as semantic evidence ordering.
func (r *USearchRoot) Candidates(query []float32, limitPerSegment int) ([]USearchRootCandidate, error) {
	if r == nil {
		return nil, fmt.Errorf("usearch root is nil")
	}
	if limitPerSegment <= 0 {
		return []USearchRootCandidate{}, nil
	}
	candidates := make([]USearchRootCandidate, 0, len(r.Segments)*limitPerSegment)
	for _, segment := range r.Segments {
		if segment.SegmentHash == "" {
			return nil, fmt.Errorf("usearch root segment hash is empty")
		}
		hits, err := segment.Index.Search(query, limitPerSegment)
		if err != nil {
			return nil, fmt.Errorf("search usearch root segment %s: %w", segment.SegmentHash, err)
		}
		for _, hit := range hits {
			if hit.Ordinal >= uint64(len(segment.Manifest.Members)) {
				return nil, fmt.Errorf("usearch root segment %s ordinal %d is outside immutable manifest", segment.SegmentHash, hit.Ordinal)
			}
			member := segment.Manifest.Members[hit.Ordinal]
			if member.Ordinal != hit.Ordinal {
				return nil, fmt.Errorf("usearch root segment %s ordinal %d does not match immutable manifest", segment.SegmentHash, hit.Ordinal)
			}
			candidates = append(candidates, USearchRootCandidate{SegmentHash: segment.SegmentHash, Member: member, ApproximateDistance: hit.Distance})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ApproximateDistance != candidates[j].ApproximateDistance {
			return candidates[i].ApproximateDistance < candidates[j].ApproximateDistance
		}
		if candidates[i].SegmentHash != candidates[j].SegmentHash {
			return candidates[i].SegmentHash < candidates[j].SegmentHash
		}
		return candidates[i].Member.Ordinal < candidates[j].Member.Ordinal
	})
	return candidates, nil
}

func (r *USearchRoot) Close() error {
	if r == nil {
		return nil
	}
	var first error
	for i := range r.Segments {
		if r.Segments[i].Index != nil {
			if err := r.Segments[i].Index.Close(); err != nil && first == nil {
				first = err
			}
			r.Segments[i].Index = nil
		}
	}
	return first
}
