package ask

import (
	"context"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

// InspectEvidence reloads one retrieved row from authoritative local storage
// and rebuilds a wider, query-windowed evidence view. It is read-only and does
// not run another search.
func InspectEvidence(ctx context.Context, cfg config.Config, st *store.Store, row Evidence, question string, maxChars int) (Evidence, error) {
	if st == nil {
		return Evidence{}, fmt.Errorf("store is required")
	}
	key := strings.TrimSpace(row.SourceKey)
	if key == "" {
		return Evidence{}, fmt.Errorf("source key is required")
	}
	if maxChars <= 0 {
		maxChars = 2000
	}
	terms := Hints(question).Terms
	result := model.SearchResult{
		SourceKey:    key,
		SourceType:   row.SourceType,
		Title:        row.Title,
		CanonicalURL: row.URL,
		NotePath:     row.NotePath,
		UserTags:     row.UserTags,
		Snippet:      row.Excerpt,
	}
	if row.Kind != "source" && !isSourceDocumentKey(key) {
		if item, err := st.GetItem(ctx, key); err == nil {
			hydrated := evidenceFromItem(cfg, item, result, maxChars, terms).Evidence
			return preserveSemanticSelection(hydrated, row), nil
		}
	}
	source, err := st.GetSourceEvidence(ctx, key)
	if err != nil {
		return Evidence{}, err
	}
	if extracted, extractErr := st.GetSourceExtractedText(ctx, key); extractErr == nil {
		source.ExtractedText = extracted
	}
	hydrated := evidenceFromSource(cfg, source, result, maxChars, terms).Evidence
	return preserveSemanticSelection(hydrated, row), nil
}

func preserveSemanticSelection(hydrated, original Evidence) Evidence {
	if original.Chunk == nil || strings.TrimSpace(original.Chunk.ID) == "" || !hasRetrievalLane(original, "semantic") {
		return hydrated
	}
	hydrated.Excerpt, hydrated.EvidenceRole = original.Excerpt, original.EvidenceRole
	chunk := *original.Chunk
	chunk.ContributingIDs = append([]string(nil), original.Chunk.ContributingIDs...)
	hydrated.Chunk = &chunk
	if original.Retrieval != nil {
		info := *original.Retrieval
		info.Lanes = append([]RetrievalLane(nil), original.Retrieval.Lanes...)
		info.Signals = append([]RetrievalSignal(nil), original.Retrieval.Signals...)
		info.MatchedTerms = append([]string(nil), original.Retrieval.MatchedTerms...)
		info.MissingTerms = append([]string(nil), original.Retrieval.MissingTerms...)
		if info.FusedScore != nil {
			v := *info.FusedScore
			info.FusedScore = &v
		}
		hydrated.Retrieval = &info
	}
	return hydrated
}

func hasRetrievalLane(row Evidence, name string) bool {
	if row.Retrieval == nil {
		return false
	}
	for _, lane := range row.Retrieval.Lanes {
		if strings.EqualFold(strings.TrimSpace(lane.Name), name) {
			return true
		}
	}
	return false
}
