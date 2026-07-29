package semanticbuild

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticsegment"
	"github.com/darron/dbrain/internal/store"
)

type SegmentPayloadBuilder interface {
	Build(context.Context, []store.RetrievalEmbeddingRow) (func(io.Writer) error, error)
}

type FlushStore interface {
	RetrievalDatabaseID(context.Context) (string, error)
	NextRetrievalFlushWindow(context.Context, string, int) (store.RetrievalFlushWindow, error)
	RetrievalIndexGenerationSegments(context.Context, string) ([]store.RetrievalIndexSegmentRow, error)
	CompleteRetrievalIndexGeneration(context.Context, store.CompleteRetrievalIndexGenerationInput) error
}

type FlushOptions struct {
	Profile                                           embedding.Profile
	Backend, BackendVersion, DistanceMetric, CacheDir string
	Limit                                             int
}

type FlushResult struct {
	GenerationID, SegmentHash string
	Indexed, Flushed, L0Ready int
	SnapshotRevision          int64
}

// Flush creates exactly one immutable segment from the oldest revision-prefix
// of the L0 tail. The caller supplies an opaque backend payload builder; this
// package never selects or links a native backend itself.
func Flush(ctx context.Context, st FlushStore, builder SegmentPayloadBuilder, opts FlushOptions) (FlushResult, error) {
	if st == nil || builder == nil {
		return FlushResult{}, fmt.Errorf("semantic flush store and payload builder are required")
	}
	if err := ctx.Err(); err != nil {
		return FlushResult{}, err
	}
	profileID, err := opts.Profile.ID()
	if err != nil {
		return FlushResult{}, fmt.Errorf("semantic flush profile is invalid: %w", err)
	}
	if strings.TrimSpace(opts.Backend) == "" || strings.TrimSpace(opts.BackendVersion) == "" || strings.TrimSpace(opts.DistanceMetric) == "" || strings.TrimSpace(opts.CacheDir) == "" {
		return FlushResult{}, fmt.Errorf("semantic flush backend metadata and cache directory are required")
	}
	limit := opts.Limit
	if limit == 0 {
		limit = store.RetrievalSegmentTarget
	}
	if limit < 0 || limit > store.RetrievalSegmentHardLimit {
		return FlushResult{}, fmt.Errorf("semantic flush limit must be between 1 and %d", store.RetrievalSegmentHardLimit)
	}
	if limit > store.RetrievalSegmentTarget {
		limit = store.RetrievalSegmentTarget
	}
	databaseID, err := st.RetrievalDatabaseID(ctx)
	if err != nil {
		return FlushResult{}, err
	}
	window, err := st.NextRetrievalFlushWindow(ctx, profileID, limit)
	if err != nil {
		return FlushResult{}, err
	}
	if err := validateFlushWindow(profileID, opts.Profile.Dimensions, window, limit); err != nil {
		return FlushResult{}, err
	}
	activationMode := store.RetrievalGenerationAdvanceSnapshot
	if window.SnapshotRevision == window.Profile.ActiveSnapshotRevision {
		activationMode = store.RetrievalGenerationRewriteSnapshot
	}
	payload, err := builder.Build(ctx, append([]store.RetrievalEmbeddingRow(nil), window.Rows...))
	if err != nil {
		return FlushResult{}, fmt.Errorf("build semantic segment payload: %w", err)
	}
	segment, err := semanticsegment.PublishSegment(opts.CacheDir, semanticsegment.SegmentInput{
		DatabaseID: databaseID, ProfileID: profileID, Backend: opts.Backend, BackendVersion: opts.BackendVersion,
		DistanceMetric: opts.DistanceMetric, Dimensions: opts.Profile.Dimensions, Members: segmentMembers(window.Rows), Payload: payload,
	})
	if err != nil {
		return FlushResult{}, fmt.Errorf("publish semantic segment: %w", err)
	}
	generationID, err := newGenerationID()
	if err != nil {
		return FlushResult{}, err
	}
	segments, err := flushRootSegments(ctx, st, window.Profile.ActiveGenerationID, segment)
	if err != nil {
		return FlushResult{}, err
	}
	root, err := semanticsegment.PublishRoot(opts.CacheDir, semanticsegment.RootInput{
		DatabaseID: databaseID, ProfileID: profileID, GenerationID: generationID,
		SnapshotRevision: window.SnapshotRevision, PurgeEpoch: window.Profile.PurgeEpoch, Segments: segments,
	})
	if err != nil {
		return FlushResult{}, fmt.Errorf("publish semantic root: %w", err)
	}
	if _, err := semanticsegment.OpenRoot(opts.CacheDir, databaseID, profileID, generationID); err != nil {
		return FlushResult{}, fmt.Errorf("reopen published semantic root: %w", err)
	}
	segmentRows, err := flushSegmentRows(ctx, st, window.Profile.ActiveGenerationID, segment)
	if err != nil {
		return FlushResult{}, err
	}
	indexed := 0
	for _, row := range segmentRows {
		indexed += row.IndexedChunkCount
	}
	now := time.Now().UTC()
	completion := store.CompleteRetrievalIndexGenerationInput{
		Generation: store.RetrievalIndexGenerationRow{
			GenerationID: generationID, ProfileID: profileID, Backend: opts.Backend, BackendVersion: opts.BackendVersion,
			Dimensions: opts.Profile.Dimensions, DistanceMetric: opts.DistanceMetric, IndexedChunkCount: indexed,
			SourceManifestHash: root.Manifest.DescriptorSHA256, BuildStatus: store.RetrievalGenerationCompleted,
			RelativeCachePath: root.RelativePath, BuildStartedAt: now, BuildCompletedAt: now,
		},
		Segments: segmentRows, Members: storeSegmentMembers(window.Rows, segment.Hash), SnapshotRevision: window.SnapshotRevision,
		ExpectedActiveGenerationID: window.Profile.ActiveGenerationID, ExpectedPurgeEpoch: window.Profile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: window.Profile.ActiveSnapshotRevision, ActivationMode: activationMode,
	}
	if err := st.CompleteRetrievalIndexGeneration(ctx, completion); err != nil {
		return FlushResult{}, fmt.Errorf("activate published semantic root: %w", err)
	}
	return FlushResult{
		GenerationID: generationID, SegmentHash: segment.Hash,
		Indexed: indexed, Flushed: len(window.Rows),
		L0Ready:          max(window.Profile.L0ReadyCount-len(window.Rows), 0),
		SnapshotRevision: window.SnapshotRevision,
	}, nil
}

func validateFlushWindow(profileID string, dimensions int, window store.RetrievalFlushWindow, limit int) error {
	if window.Profile.ProfileID != profileID || len(window.Rows) != store.RetrievalSegmentTarget || len(window.Rows) > limit || len(window.Rows) > store.RetrievalSegmentTarget {
		return fmt.Errorf("semantic flush window is empty or does not match requested profile")
	}
	if window.SnapshotRevision < window.Profile.ActiveSnapshotRevision {
		return fmt.Errorf("semantic flush window snapshot revision predates active snapshot")
	}
	previous := int64(0)
	for _, row := range window.Rows {
		// One atomic embedding batch intentionally shares one revision across
		// all changed rows, so the ordered flush prefix is nondecreasing.
		if row.ProfileID != profileID || row.Dimensions != dimensions || row.Revision <= 0 || row.Revision < previous || strings.TrimSpace(row.VectorHash) == "" {
			return fmt.Errorf("semantic flush window has invalid member provenance")
		}
		previous = row.Revision
	}
	if previous != window.SnapshotRevision {
		return fmt.Errorf("semantic flush window snapshot does not match final member revision")
	}
	return nil
}

func segmentMembers(rows []store.RetrievalEmbeddingRow) []semanticsegment.Member {
	members := make([]semanticsegment.Member, 0, len(rows))
	for ordinal, row := range rows {
		members = append(members, semanticsegment.Member{Ordinal: uint64(ordinal), ChunkID: row.ChunkID, Revision: row.Revision, VectorHash: row.VectorHash})
	}
	return members
}

func storeSegmentMembers(rows []store.RetrievalEmbeddingRow, segmentHash string) []store.RetrievalIndexSegmentMember {
	members := make([]store.RetrievalIndexSegmentMember, 0, len(rows))
	for ordinal, row := range rows {
		members = append(members, store.RetrievalIndexSegmentMember{SegmentHash: segmentHash, Ordinal: uint64(ordinal), ChunkID: row.ChunkID, Revision: row.Revision, VectorHash: row.VectorHash})
	}
	return members
}

func flushRootSegments(ctx context.Context, st FlushStore, activeGenerationID string, newSegment semanticsegment.Segment) ([]semanticsegment.RootSegment, error) {
	segments := []semanticsegment.RootSegment{{Hash: newSegment.Hash, RelativePath: newSegment.RelativePath}}
	if activeGenerationID != "" {
		existing, err := st.RetrievalIndexGenerationSegments(ctx, activeGenerationID)
		if err != nil {
			return nil, err
		}
		for _, row := range existing {
			segments = append(segments, semanticsegment.RootSegment{Hash: row.SegmentHash, RelativePath: row.RelativeCachePath})
		}
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].Hash < segments[j].Hash })
	for index := 1; index < len(segments); index++ {
		if segments[index-1].Hash == segments[index].Hash {
			return nil, fmt.Errorf("semantic root already references segment %s", segments[index].Hash)
		}
	}
	return segments, nil
}

func flushSegmentRows(ctx context.Context, st FlushStore, activeGenerationID string, newSegment semanticsegment.Segment) ([]store.RetrievalIndexSegmentRow, error) {
	rows := []store.RetrievalIndexSegmentRow{{
		SegmentHash: newSegment.Hash, ProfileID: newSegment.Manifest.ProfileID, Backend: newSegment.Manifest.Backend,
		BackendVersion: newSegment.Manifest.BackendVersion, Dimensions: newSegment.Manifest.Dimensions,
		DistanceMetric: newSegment.Manifest.DistanceMetric, IndexedChunkCount: len(newSegment.Manifest.Members),
		RelativeCachePath: newSegment.RelativePath, MembershipHash: newSegment.Manifest.MembersSHA256,
		PayloadHash: newSegment.Manifest.PayloadSHA256, ManifestHash: newSegment.Manifest.DescriptorSHA256,
	}}
	if activeGenerationID != "" {
		existing, err := st.RetrievalIndexGenerationSegments(ctx, activeGenerationID)
		if err != nil {
			return nil, err
		}
		rows = append(rows, existing...)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].SegmentHash < rows[j].SegmentHash })
	return rows, nil
}

func newGenerationID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate semantic generation ID: %w", err)
	}
	return "semantic-root-v1:" + hex.EncodeToString(bytes[:]), nil
}
