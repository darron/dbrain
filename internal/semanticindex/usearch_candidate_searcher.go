//go:build usearch && cgo

package semanticindex

import (
	"container/heap"
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/store"
)

const nativeCandidateOverfetch = 8

// USearchCandidateStore is the SQLite authority boundary for a native root.
// It validates immutable manifest members against the active root and returns
// only current ready embeddings for exact reranking.
type USearchCandidateStore interface {
	ReadRetrievalNativeCandidates(context.Context, store.RetrievalNativeCandidateRequest) ([]store.RetrievalEmbeddingRow, error)
	ReadRetrievalExactL0(context.Context, store.RetrievalActiveRootReadRequest, int) ([]store.RetrievalEmbeddingRow, error)
}

// USearchCandidateSearcher is a tag-gated Searcher implementation. It searches
// immutable native segments for candidates, validates every member through
// SQLite, and exactly reranks the surviving vectors.
type USearchCandidateSearcher struct {
	root  *USearchRoot
	store USearchCandidateStore
}

func NewUSearchCandidateSearcher(root *USearchRoot, st USearchCandidateStore) *USearchCandidateSearcher {
	return &USearchCandidateSearcher{root: root, store: st}
}

// Close releases the native indexes opened for this immutable root.
func (s *USearchCandidateSearcher) Close() error {
	if s == nil || s.root == nil {
		return nil
	}
	return s.root.Close()
}

func (s *USearchCandidateSearcher) Search(ctx context.Context, query []float32, opts SearchOptions) ([]Hit, Status, error) {
	hits := make([]Hit, 0)
	status := Status{State: StateUnavailable, Backend: BackendUSearch}
	if err := ctx.Err(); err != nil {
		status.Reason = ReasonCanceled
		return hits, status, err
	}
	profileID, err := opts.Profile.ID()
	if err != nil || strings.TrimSpace(opts.Profile.Normalization) != embedding.NormalizationL2 {
		status.Reason = ReasonProfileMismatch
		return hits, status, nil
	}
	status.ProfileID = profileID
	if len(query) != opts.Profile.Dimensions {
		status.Reason = ReasonDimensionMismatch
		return hits, status, nil
	}
	if opts.Limit <= 0 || opts.MaxChunks <= 0 || s == nil || s.root == nil || s.store == nil {
		status.Reason = ReasonSearchError
		return hits, status, nil
	}
	if err := embedding.ValidateDenseF32(query, opts.Profile.Dimensions, opts.Profile.Normalization); err != nil {
		status.Reason = ReasonSearchError
		return hits, status, nil
	}
	filters, err := cleanFilters(opts.Filters)
	if err != nil {
		status.Reason = ReasonSearchError
		return hits, status, nil
	}
	root := s.root.Root.Manifest
	if root.ProfileID != profileID {
		status.Reason = ReasonProfileMismatch
		return hits, status, nil
	}
	if root.GenerationID == "" || root.SnapshotRevision <= 0 || root.PurgeEpoch < 0 || len(s.root.Segments) == 0 {
		status.Reason = ReasonIndexCorrupt
		return hits, status, nil
	}
	status.GenerationID = root.GenerationID
	candidates, err := s.root.Candidates(query, nativeCandidatesPerSegment(opts.Limit))
	if err != nil {
		status.Reason = ReasonSearchError
		return hits, status, err
	}
	if len(candidates) == 0 {
		status.State, status.Reason = StateSearched, ReasonNone
		return hits, status, nil
	}
	if len(candidates) > store.MaxRetrievalNativeCandidates {
		candidates = candidates[:store.MaxRetrievalNativeCandidates]
	}
	requested := make(map[string]USearchRootCandidate, len(candidates))
	request := store.RetrievalNativeCandidateRequest{
		ProfileID: profileID, ExpectedActiveGenerationID: root.GenerationID,
		ExpectedPurgeEpoch: root.PurgeEpoch, ExpectedActiveSnapshotRevision: root.SnapshotRevision,
		Candidates: make([]store.RetrievalNativeCandidate, 0, len(candidates)),
	}
	for _, candidate := range candidates {
		if _, exists := requested[candidate.Member.ChunkID]; exists {
			status.Reason = ReasonIndexCorrupt
			return hits, status, nil
		}
		requested[candidate.Member.ChunkID] = candidate
		request.Candidates = append(request.Candidates, store.RetrievalNativeCandidate{
			SegmentHash: candidate.SegmentHash, ChunkID: candidate.Member.ChunkID,
			Revision: candidate.Member.Revision, VectorHash: candidate.Member.VectorHash,
		})
	}
	rows, err := s.store.ReadRetrievalNativeCandidates(ctx, request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			status.Reason = ReasonCanceled
			return hits, status, ctxErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status.Reason = ReasonCanceled
			return hits, status, err
		}
		var corruption *store.RetrievalEmbeddingCorruptionError
		if errors.As(err, &corruption) {
			status.Reason = ReasonIndexCorrupt
			return hits, status, nil
		}
		status.Reason = ReasonSearchError
		return hits, status, err
	}
	l0, err := s.store.ReadRetrievalExactL0(ctx, store.RetrievalActiveRootReadRequest{ProfileID: profileID, ExpectedActiveGenerationID: root.GenerationID, ExpectedPurgeEpoch: root.PurgeEpoch, ExpectedActiveSnapshotRevision: root.SnapshotRevision}, store.RetrievalSegmentHardLimit)
	if err != nil {
		status.Reason = ReasonSearchError
		return hits, status, err
	}
	rows = append(rows, l0...)
	ranked := make(candidateHeap, 0, min(opts.Limit, len(rows)))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			status.Reason = ReasonCanceled
			return hits, status, err
		}
		candidate, exists := requested[row.ChunkID]
		if exists && (row.Revision != candidate.Member.Revision || row.VectorHash != candidate.Member.VectorHash) {
			status.Reason = ReasonIndexCorrupt
			return hits, status, nil
		}
		if row.ProfileID != profileID || strings.TrimSpace(row.Provider) != strings.TrimSpace(opts.Profile.Provider) || strings.TrimSpace(row.Model) != strings.TrimSpace(opts.Profile.Model) ||
			strings.TrimSpace(row.ProjectionVersion) != strings.TrimSpace(opts.Profile.ProjectionVersion) || strings.TrimSpace(row.ChunkerVersion) != strings.TrimSpace(opts.Profile.ChunkerVersion) {
			status.Reason = ReasonProfileMismatch
			return hits, status, nil
		}
		if row.Dimensions != opts.Profile.Dimensions {
			status.Reason = ReasonDimensionMismatch
			return hits, status, nil
		}
		if strings.TrimSpace(row.Representation) != strings.TrimSpace(opts.Profile.Representation) || strings.TrimSpace(row.Normalization) != strings.TrimSpace(opts.Profile.Normalization) {
			status.Reason = ReasonIndexCorrupt
			return hits, status, nil
		}
		vector, err := embedding.DecodeDenseF32(row.VectorBytes, row.Dimensions)
		if err != nil || embedding.ValidateDenseF32(vector, row.Dimensions, embedding.NormalizationL2) != nil {
			status.Reason = ReasonIndexCorrupt
			return hits, status, nil
		}
		status.Scanned++
		if !filters.allows(row) {
			continue
		}
		hit := Hit{ChunkID: row.ChunkID, Distance: cosineDistance(query, vector), SourceType: row.SourceType, SectionOrdinal: row.SectionOrdinal}
		if ranked.Len() < opts.Limit {
			heap.Push(&ranked, hit)
			continue
		}
		if better(hit, ranked[0]) {
			ranked[0] = hit
			heap.Fix(&ranked, 0)
		}
	}
	hits = append(hits, ranked...)
	sort.Slice(hits, func(i, j int) bool { return better(hits[i], hits[j]) })
	for index := range hits {
		hits[index].Rank = index + 1
	}
	status.State, status.Reason = StateSearched, ReasonNone
	return hits, status, nil
}

func nativeCandidatesPerSegment(limit int) int {
	if limit >= store.MaxRetrievalNativeCandidates/nativeCandidateOverfetch {
		return store.MaxRetrievalNativeCandidates
	}
	if candidateCount := limit * nativeCandidateOverfetch; candidateCount > 32 {
		return candidateCount
	}
	return 32
}
