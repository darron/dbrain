package semanticindex

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/store"
)

const (
	maxFilterValues          = 1000
	maxFilterLength          = 512
	maxSemanticHitsPerParent = 3
)

type ReadyStore interface {
	ListReadyEmbeddings(context.Context, string, int) ([]store.RetrievalEmbeddingRow, error)
}

type Exact struct {
	store ReadyStore
}

func NewExact(st ReadyStore) *Exact {
	return &Exact{store: st}
}

func (e *Exact) Search(ctx context.Context, query []float32, opts SearchOptions) ([]Hit, Status, error) {
	hits := make([]Hit, 0)
	status := Status{State: StateUnavailable, Backend: BackendExact}
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
	if opts.Limit <= 0 || opts.MaxChunks <= 0 || e == nil || e.store == nil {
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
	rows, err := e.store.ListReadyEmbeddings(ctx, status.ProfileID, opts.MaxChunks+1)
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
	if len(rows) > opts.MaxChunks {
		status.Reason = ReasonTooLarge
		return hits, status, nil
	}
	candidates := make([]Hit, 0, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			status.Reason = ReasonCanceled
			return hits, status, err
		}
		status.Scanned++
		if row.ProfileID != status.ProfileID {
			status.Reason = ReasonProfileMismatch
			return hits, status, nil
		}
		if strings.TrimSpace(row.Provider) != strings.TrimSpace(opts.Profile.Provider) || strings.TrimSpace(row.Model) != strings.TrimSpace(opts.Profile.Model) {
			status.Reason = ReasonProfileMismatch
			return hits, status, nil
		}
		if strings.TrimSpace(row.ProjectionVersion) != strings.TrimSpace(opts.Profile.ProjectionVersion) || strings.TrimSpace(row.ChunkerVersion) != strings.TrimSpace(opts.Profile.ChunkerVersion) {
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
		if !filters.allows(row) {
			continue
		}
		distance := cosineDistance(query, vector)
		candidates = append(candidates, Hit{ChunkID: row.ChunkID, Distance: distance, ParentKind: row.ParentKind, ParentSourceKey: row.ParentSourceKey, SourceType: row.SourceType, SectionOrdinal: row.SectionOrdinal})
	}
	hits = parentDiverseHits(candidates, opts.Limit, filters.parentKeys)
	for i := range hits {
		hits[i].Rank = i + 1
	}
	status.State, status.Reason = StateSearched, ReasonNone
	return hits, status, nil
}

func cosineDistance(a, b []float32) float64 {
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return 1 - dot
}

func better(a, b Hit) bool {
	if a.Distance != b.Distance {
		return a.Distance < b.Distance
	}
	return a.ChunkID < b.ChunkID
}

// parentDiverseHits preserves exact distance order while preventing one
// unpinned parent from exhausting a semantic candidate window. Explicitly
// allowed parent keys are protected anchors and remain uncapped.
func parentDiverseHits(candidates []Hit, limit int, protectedParentKeys map[string]struct{}) []Hit {
	if limit <= 0 || len(candidates) == 0 {
		return []Hit{}
	}
	sorted := append([]Hit(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool { return better(sorted[i], sorted[j]) })
	counts := make(map[string]int, len(sorted))
	hits := make([]Hit, 0, min(limit, len(sorted)))
	for _, candidate := range sorted {
		parent := semanticParentIdentity(candidate)
		_, protected := protectedParentKeys[strings.TrimSpace(candidate.ParentSourceKey)]
		if !protected && counts[parent] >= maxSemanticHitsPerParent {
			continue
		}
		counts[parent]++
		hits = append(hits, candidate)
		if len(hits) == limit {
			break
		}
	}
	return hits
}

func semanticParentIdentity(hit Hit) string {
	kind, key := strings.TrimSpace(hit.ParentKind), strings.TrimSpace(hit.ParentSourceKey)
	if key == "" {
		key = hit.ChunkID
	}
	return kind + "\x00" + key
}

type cleanFilterSet struct {
	parentKeys    map[string]struct{}
	parentKinds   map[string]struct{}
	evidenceRoles map[string]struct{}
	sourceTypes   map[string]struct{}
}

func cleanFilters(filters Filters) (cleanFilterSet, error) {
	parentKeys, err := cleanFilterValues(filters.AllowedParentKeys, false)
	if err != nil {
		return cleanFilterSet{}, fmt.Errorf("allowed parent keys: %w", err)
	}
	parentKinds, err := cleanFilterValues(filters.AllowedParentKinds, true)
	if err != nil {
		return cleanFilterSet{}, fmt.Errorf("allowed parent kinds: %w", err)
	}
	evidenceRoles, err := cleanFilterValues(filters.AllowedEvidenceRoles, true)
	if err != nil {
		return cleanFilterSet{}, fmt.Errorf("allowed evidence roles: %w", err)
	}
	sourceTypes, err := cleanFilterValues(filters.AllowedSourceTypes, true)
	if err != nil {
		return cleanFilterSet{}, fmt.Errorf("allowed source types: %w", err)
	}
	return cleanFilterSet{parentKeys: parentKeys, parentKinds: parentKinds, evidenceRoles: evidenceRoles, sourceTypes: sourceTypes}, nil
}

func cleanFilterValues(values []string, fold bool) (map[string]struct{}, error) {
	if len(values) > maxFilterValues {
		return nil, fmt.Errorf("filter has %d values; maximum is %d", len(values), maxFilterValues)
	}
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > maxFilterLength {
			return nil, fmt.Errorf("filter value exceeds %d bytes", maxFilterLength)
		}
		if fold {
			value = strings.ToLower(value)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func (f cleanFilterSet) allows(row store.RetrievalEmbeddingRow) bool {
	return allowsValue(f.parentKeys, strings.TrimSpace(row.ParentSourceKey)) &&
		allowsValue(f.parentKinds, strings.ToLower(strings.TrimSpace(row.ParentKind))) &&
		allowsValue(f.evidenceRoles, strings.ToLower(strings.TrimSpace(row.EvidenceRole))) &&
		allowsSourceType(f.sourceTypes, row.SourceType)
}

func allowsSourceType(allowed map[string]struct{}, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if _, ok := allowed[value]; ok {
		return true
	}
	if family, _, ok := strings.Cut(value, "_"); ok {
		_, ok = allowed[family]
		return ok
	}
	return false
}

func allowsValue(allowed map[string]struct{}, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	_, ok := allowed[value]
	return ok
}
