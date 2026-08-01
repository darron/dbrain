//go:build usearch && cgo

package semanticindex

import (
	"context"
	"errors"
	"strings"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/store"
)

const nativeCandidateOverfetch = 8

// USearchCandidateStore is the SQLite authority boundary for a native root.
// It validates immutable manifest members against the active root and returns
// only current ready embeddings for exact reranking.
type USearchCandidateStore interface {
	BeginRetrievalNativeReadSnapshot(context.Context) (store.RetrievalNativeReadSession, error)
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
	readSession, err := s.store.BeginRetrievalNativeReadSnapshot(ctx)
	if err != nil {
		status.Reason = ReasonSearchError
		return hits, status, err
	}
	defer func() { _ = readSession.Close() }()
	l0, err := readSession.ReadRetrievalExactL0(ctx, store.RetrievalActiveRootReadRequest{ProfileID: profileID, ExpectedActiveGenerationID: root.GenerationID, ExpectedPurgeEpoch: root.PurgeEpoch, ExpectedActiveSnapshotRevision: root.SnapshotRevision}, store.RetrievalSegmentHardLimit)
	if err != nil {
		status.Reason = ReasonSearchError
		return hits, status, err
	}
	rows := append([]store.RetrievalEmbeddingRow(nil), l0...)
	requested := make(map[string]USearchRootCandidate)
	validated := make(map[string]USearchRootCandidate)
	for _, budget := range nativeCandidateStages(opts.Limit) {
		candidates, err := s.root.Candidates(ctx, query, nativeCandidatesPerSegment(budget, len(s.root.Segments)))
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				status.Reason = ReasonCanceled
				return []Hit{}, status, ctxErr
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				status.Reason = ReasonCanceled
				return []Hit{}, status, err
			}
			status.Reason = ReasonSearchError
			return hits, status, err
		}
		if len(candidates) > budget {
			candidates = candidates[:budget]
		}
		newCandidates := make([]store.RetrievalNativeCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			previous, exists := requested[candidate.Member.ChunkID]
			if exists && (previous.SegmentHash != candidate.SegmentHash || previous.Member.Revision != candidate.Member.Revision || previous.Member.VectorHash != candidate.Member.VectorHash) {
				status.Reason = ReasonIndexCorrupt
				return hits, status, nil
			}
			if exists {
				continue
			}
			requested[candidate.Member.ChunkID] = candidate
			newCandidates = append(newCandidates, store.RetrievalNativeCandidate{SegmentHash: candidate.SegmentHash, ChunkID: candidate.Member.ChunkID, Revision: candidate.Member.Revision, VectorHash: candidate.Member.VectorHash})
		}
		for start := 0; start < len(newCandidates); start += store.MaxRetrievalNativeCandidates {
			end := min(start+store.MaxRetrievalNativeCandidates, len(newCandidates))
			batch, err := readSession.ReadRetrievalNativeCandidates(ctx, store.RetrievalNativeCandidateRequest{ProfileID: profileID, ExpectedActiveGenerationID: root.GenerationID, ExpectedPurgeEpoch: root.PurgeEpoch, ExpectedActiveSnapshotRevision: root.SnapshotRevision, Candidates: newCandidates[start:end]})
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					status.Reason = ReasonCanceled
					return []Hit{}, status, ctxErr
				}
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					status.Reason = ReasonCanceled
					return []Hit{}, status, err
				}
				var corruption *store.RetrievalEmbeddingCorruptionError
				if errors.As(err, &corruption) {
					status.Reason = ReasonIndexCorrupt
					return hits, status, nil
				}
				status.Reason = ReasonSearchError
				return hits, status, err
			}
			for _, row := range batch {
				candidate, exists := requested[row.ChunkID]
				if !exists || row.Revision != candidate.Member.Revision || row.VectorHash != candidate.Member.VectorHash {
					status.Reason = ReasonIndexCorrupt
					return hits, status, nil
				}
				validated[row.ChunkID] = candidate
			}
			rows = append(rows, batch...)
		}
		hits, status.Scanned, status.Reason, err = rankNativeRows(ctx, query, opts, filters, profileID, rows, validated)
		if err != nil || status.Reason != ReasonNone {
			return hits, status, err
		}
		if len(hits) >= opts.Limit {
			break
		}
	}
	for index := range hits {
		hits[index].Rank = index + 1
	}
	status.State, status.Reason = StateSearched, ReasonNone
	return hits, status, nil
}

func rankNativeRows(ctx context.Context, query []float32, opts SearchOptions, filters cleanFilterSet, profileID string, rows []store.RetrievalEmbeddingRow, validated map[string]USearchRootCandidate) ([]Hit, int, StatusReason, error) {
	ranked := make([]Hit, 0, len(rows))
	scanned := 0
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, scanned, ReasonCanceled, err
		}
		candidate, exists := validated[row.ChunkID]
		if exists && (row.Revision != candidate.Member.Revision || row.VectorHash != candidate.Member.VectorHash) {
			return nil, scanned, ReasonIndexCorrupt, nil
		}
		if row.ProfileID != profileID || strings.TrimSpace(row.Provider) != strings.TrimSpace(opts.Profile.Provider) || strings.TrimSpace(row.Model) != strings.TrimSpace(opts.Profile.Model) ||
			strings.TrimSpace(row.ProjectionVersion) != strings.TrimSpace(opts.Profile.ProjectionVersion) || strings.TrimSpace(row.ChunkerVersion) != strings.TrimSpace(opts.Profile.ChunkerVersion) {
			return nil, scanned, ReasonProfileMismatch, nil
		}
		if row.Dimensions != opts.Profile.Dimensions {
			return nil, scanned, ReasonDimensionMismatch, nil
		}
		if strings.TrimSpace(row.Representation) != strings.TrimSpace(opts.Profile.Representation) || strings.TrimSpace(row.Normalization) != strings.TrimSpace(opts.Profile.Normalization) {
			return nil, scanned, ReasonIndexCorrupt, nil
		}
		vector, err := embedding.DecodeDenseF32(row.VectorBytes, row.Dimensions)
		if err != nil || embedding.ValidateDenseF32(vector, row.Dimensions, embedding.NormalizationL2) != nil {
			return nil, scanned, ReasonIndexCorrupt, nil
		}
		scanned++
		if !filters.allows(row) {
			continue
		}
		ranked = append(ranked, Hit{ChunkID: row.ChunkID, Distance: cosineDistance(query, vector), ParentKind: row.ParentKind, ParentSourceKey: row.ParentSourceKey, SourceType: row.SourceType, SectionOrdinal: row.SectionOrdinal})
	}
	return parentDiverseHits(ranked, opts.Limit, filters.parentKeys), scanned, ReasonNone, nil
}

func nativeCandidateStages(_ int) []int { return []int{200, 500, 2000} }

func nativeCandidatesPerSegment(total, segments int) int {
	if segments <= 0 {
		return total
	}
	return (total + segments - 1) / segments
}
