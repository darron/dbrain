//go:build usearch && cgo

package semanticindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
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
	DescriptorSHA256 string
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

func OpenUSearchRoot(ctx context.Context, cacheDir, databaseID, profileID, generationID string, expect USearchRootExpectations) (*USearchRoot, error) {
	loaded, err := openUSearchRoot(ctx, cacheDir, databaseID, profileID, generationID, expect, func(path string) (io.ReadCloser, error) {
		return os.Open(path)
	})
	if err != nil {
		return nil, err
	}
	return loaded, nil
}

func openUSearchRoot(
	ctx context.Context,
	cacheDir, databaseID, profileID, generationID string,
	expect USearchRootExpectations,
	openPayload func(string) (io.ReadCloser, error),
) (*USearchRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if openPayload == nil {
		return nil, fmt.Errorf("usearch payload opener is nil")
	}
	root, err := semanticsegment.OpenRootManifest(ctx, cacheDir, databaseID, profileID, generationID)
	if err != nil {
		return nil, fmt.Errorf("open usearch root: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root.Manifest.DescriptorSHA256 != expect.DescriptorSHA256 {
		return nil, fmt.Errorf("usearch root descriptor hash mismatch: cache=%q expected=%q", root.Manifest.DescriptorSHA256, expect.DescriptorSHA256)
	}
	if root.Manifest.SnapshotRevision != expect.SnapshotRevision {
		return nil, fmt.Errorf("usearch root snapshot revision mismatch: cache=%d expected=%d", root.Manifest.SnapshotRevision, expect.SnapshotRevision)
	}
	if root.Manifest.PurgeEpoch != expect.PurgeEpoch {
		return nil, fmt.Errorf("usearch root purge epoch mismatch: cache=%d expected=%d", root.Manifest.PurgeEpoch, expect.PurgeEpoch)
	}
	loaded := &USearchRoot{Root: root, Segments: make([]USearchRootSegment, 0, len(root.Manifest.Segments))}
	fail := func(err error) (*USearchRoot, error) { _ = loaded.Close(); return loaded, err }
	for _, reference := range root.Manifest.Segments {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		segment, err := semanticsegment.OpenSegmentManifest(ctx, cacheDir, databaseID, profileID, reference.Hash)
		if err != nil {
			return fail(fmt.Errorf("open usearch root segment %s: %w", reference.Hash, err))
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
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
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		payloadFile, err := openPayload(filepath.Join(cacheDir, filepath.FromSlash(reference.RelativePath), semanticsegment.PayloadFileName))
		if err != nil {
			return fail(fmt.Errorf("open usearch root payload %s: %w", reference.Hash, err))
		}
		payload, readErr := readVerifiedUSearchPayload(ctx, payloadFile, segment.Manifest.PayloadSHA256)
		closeErr := payloadFile.Close()
		if readErr != nil {
			return fail(fmt.Errorf("read usearch root payload %s: %w", reference.Hash, readErr))
		}
		if closeErr != nil {
			return fail(fmt.Errorf("close usearch root payload %s: %w", reference.Hash, closeErr))
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		index, err := NewUSearch(expect.Index)
		if err != nil {
			return fail(err)
		}
		if err := loadVerifiedUSearchPayload(ctx, index, payload); err != nil {
			_ = index.Close()
			return fail(fmt.Errorf("import usearch root segment %s: %w", reference.Hash, err))
		}
		loaded.Segments = append(loaded.Segments, USearchRootSegment{SegmentHash: reference.Hash, Manifest: segment.Manifest, Index: index})
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return loaded, nil
}

func readVerifiedUSearchPayload(ctx context.Context, reader io.Reader, expectedSHA256 string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hash := sha256.New()
	payload, err := io.ReadAll(io.TeeReader(&contextReader{ctx: ctx, reader: reader}, hash))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedSHA256 {
		return nil, fmt.Errorf("payload checksum mismatch")
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("usearch payload is empty")
	}
	return payload, nil
}

// loadVerifiedUSearchPayload cannot preempt USearch's native LoadBuffer call.
// It rejects cancellation immediately before entering C and immediately after
// the call returns.
func loadVerifiedUSearchPayload(ctx context.Context, index *USearch, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := index.available(); err != nil {
		return err
	}
	if len(payload) == 0 {
		return fmt.Errorf("usearch payload is empty")
	}
	loadErr := index.index.LoadBuffer(payload, uint(len(payload)))
	if err := ctx.Err(); err != nil {
		return err
	}
	if loadErr != nil {
		return fmt.Errorf("load usearch payload: %w", loadErr)
	}
	dimensions, err := index.index.Dimensions()
	if err != nil {
		return fmt.Errorf("read imported usearch dimensions: %w", err)
	}
	if int(dimensions) != index.dimensions {
		return fmt.Errorf("usearch imported dimensions %d want %d", dimensions, index.dimensions)
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	count, err := r.reader.Read(buffer)
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return count, ctxErr
	}
	return count, err
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
