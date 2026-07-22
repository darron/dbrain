package brainresearch

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/researchhybrid"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/store"
)

const (
	DefaultInspectionLimit    = 5
	DefaultInspectionMaxChars = 2000
)

type InspectionOptions struct {
	Limit    int
	MaxChars int
}

type InspectionResult struct {
	Applied             bool              `json:"applied"`
	Limit               int               `json:"limit,omitempty"`
	InspectedSourceKeys []string          `json:"inspected_source_keys,omitempty"`
	OriginalOrder       []string          `json:"original_order,omitempty"`
	FinalOrder          []string          `json:"final_order,omitempty"`
	Reordered           bool              `json:"reordered,omitempty"`
	Rows                []InspectionRow   `json:"rows,omitempty"`
	Errors              []InspectionError `json:"errors,omitempty"`
}

type InspectionRow struct {
	SourceKey          string   `json:"source_key"`
	HydrationStatus    string   `json:"hydration_status"`
	MatchedConcepts    []string `json:"matched_concepts,omitempty"`
	MissingConcepts    []string `json:"missing_concepts,omitempty"`
	RawMatchedConcepts []string `json:"raw_matched_concepts,omitempty"`
	EvidenceRole       string   `json:"evidence_role,omitempty"`
	DirectEvidence     bool     `json:"direct_evidence,omitempty"`
	RawSupport         bool     `json:"raw_support,omitempty"`
	RankingClass       string   `json:"ranking_class,omitempty"`
	ScoreBefore        int      `json:"score_before,omitempty"`
	ScoreAfter         int      `json:"score_after,omitempty"`
	RankBefore         int      `json:"rank_before,omitempty"`
	RankAfter          int      `json:"rank_after,omitempty"`
}

type InspectionError struct {
	SourceKey string `json:"source_key"`
	Error     string `json:"error"`
}

type inspectedEvidence struct {
	row      ask.Evidence
	metadata InspectionRow
	order    int
}

// InspectPack performs one bounded read-only hydration pass over the strongest
// primary evidence rows, then reranks only that inspected window. Rows outside
// the window retain their original order.
func InspectPack(ctx context.Context, cfg config.Config, st *store.Store, pack Pack, opts InspectionOptions) (Pack, InspectionResult) {
	pack.Evidence = append([]ask.Evidence(nil), pack.Evidence...)
	for i := range pack.Evidence {
		pack.Evidence[i] = cloneInspectionEvidence(pack.Evidence[i])
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultInspectionLimit
	}
	maxChars := opts.MaxChars
	if maxChars <= 0 {
		maxChars = max(DefaultInspectionMaxChars, pack.QueryPlan.MaxCharsPerDoc)
	}
	limit = min(limit, len(pack.Evidence))
	result := InspectionResult{Applied: limit > 0, Limit: limit}
	if limit == 0 {
		pack.Inspection = &result
		return pack, result
	}

	inspected := make([]inspectedEvidence, 0, limit)
	for index, original := range pack.Evidence[:limit] {
		result.OriginalOrder = append(result.OriginalOrder, original.SourceKey)
		hydrated, err := ask.InspectEvidence(ctx, cfg, st, original, pack.Question, maxChars)
		if err != nil {
			hydrated = original
			result.Errors = append(result.Errors, InspectionError{SourceKey: original.SourceKey, Error: err.Error()})
		} else {
			hydrated.Retrieval = cloneInspectionRetrieval(original.Retrieval)
		}
		if err != nil {
			hydrated.Retrieval = cloneInspectionRetrieval(original.Retrieval)
		}
		metadata := InspectionRow{SourceKey: original.SourceKey, EvidenceRole: original.EvidenceRole}
		if err == nil {
			metadata = inspectEvidenceRow(hydrated, pack.QueryPlan.Concepts)
		}
		metadata.HydrationStatus = "hydrated"
		if err != nil {
			metadata.HydrationStatus = "hydration_failed"
		}
		metadata.ScoreBefore = evidenceScore(original)
		metadata.ScoreAfter = metadata.ScoreBefore
		metadata.RankBefore = index + 1
		metadata.RankingClass = inspectionRankingClass(metadata)
		if hydrated.Retrieval == nil {
			hydrated.Retrieval = &ask.RetrievalInfo{}
		}
		hydrated.Retrieval.Signals = append(hydrated.Retrieval.Signals,
			retrieval.RetrievalSignal{Name: "inspection_hydrated", Detail: metadata.EvidenceRole, Weight: 0},
			retrieval.RetrievalSignal{Name: "inspection_required_concept_coverage", Detail: inspectionCoverageDetail(metadata), Weight: 0},
		)
		inspected = append(inspected, inspectedEvidence{row: hydrated, metadata: metadata, order: index})
		result.InspectedSourceKeys = append(result.InspectedSourceKeys, hydrated.SourceKey)
	}
	if len(result.Errors) == 0 {
		sort.SliceStable(inspected, func(i, j int) bool {
			leftQuality, leftRaw := inspectionRankingValues(inspected[i].metadata)
			rightQuality, rightRaw := inspectionRankingValues(inspected[j].metadata)
			if leftRaw != rightRaw {
				return leftRaw > rightRaw
			}
			// Raw concept verification is the primary inspection ordering class.
			// Direct/raw evidence quality breaks ties only within a positive raw
			// coverage class; without raw verification retrieval ranking stays primary.
			if leftRaw > 0 && leftQuality != rightQuality {
				return leftQuality > rightQuality
			}
			leftScore, rightScore := evidenceInspectionScore(inspected[i].row), evidenceInspectionScore(inspected[j].row)
			if leftScore != rightScore {
				return leftScore > rightScore
			}
			return inspected[i].order < inspected[j].order
		})
	}
	for i, inspectedRow := range inspected {
		inspectedRow.metadata.RankAfter = i + 1
		pack.Evidence[i] = inspectedRow.row
		result.Rows = append(result.Rows, inspectedRow.metadata)
		result.FinalOrder = append(result.FinalOrder, inspectedRow.row.SourceKey)
	}
	result.Reordered = !equalStrings(result.OriginalOrder, result.FinalOrder)
	pack.Inspection = &result
	return pack, result
}

func inspectEvidenceRow(row ask.Evidence, concepts []QueryConcept) InspectionRow {
	result := InspectionRow{SourceKey: row.SourceKey, EvidenceRole: row.EvidenceRole, DirectEvidence: strings.TrimSpace(row.RelatedTo) == "" && strings.TrimSpace(row.Relationship) == "", RawSupport: hasRawEvidenceSupport(row)}
	text := researchEvidenceText(row)
	rawText := rawInspectionText(row)
	for _, concept := range concepts {
		if !concept.Required || (concept.Role != "" && concept.Role != conceptRoleContent && concept.Role != conceptRoleAnchor) {
			continue
		}
		if conceptMatchesText(concept, text) {
			result.MatchedConcepts = append(result.MatchedConcepts, concept.Key)
		} else {
			result.MissingConcepts = append(result.MissingConcepts, concept.Key)
		}
		if conceptMatchesText(concept, rawText) {
			result.RawMatchedConcepts = append(result.RawMatchedConcepts, concept.Key)
		}
	}
	return result
}

func inspectionRankingClass(row InspectionRow) string {
	quality, rawCoverage := inspectionRankingValues(row)
	return fmt.Sprintf("quality_%d_raw_coverage_%d", quality, rawCoverage)
}

func inspectionRankingValues(row InspectionRow) (int, int) {
	quality := 0
	if row.DirectEvidence {
		quality += 2
	}
	if row.RawSupport {
		quality++
	}
	rawCoverage := 0
	if len(row.RawMatchedConcepts) > 0 {
		rawCoverage = 1
	}
	if len(row.MissingConcepts) == 0 && len(row.RawMatchedConcepts) == len(row.MatchedConcepts) && len(row.MatchedConcepts) > 0 {
		rawCoverage = 2
	}
	return quality, rawCoverage
}

func inspectionCoverageDetail(row InspectionRow) string {
	return fmt.Sprintf("matched=%s; missing=%s; raw_support=%t", strings.Join(row.MatchedConcepts, ","), strings.Join(row.MissingConcepts, ","), row.RawSupport)
}

func hasRawEvidenceSupport(row ask.Evidence) bool {
	role := strings.ToLower(strings.TrimSpace(row.EvidenceRole))
	if isSemanticChunkEvidence(row) {
		return retrievalchunk.IsRawSupportRole(role) && strings.TrimSpace(row.Excerpt) != ""
	}
	if retrievalchunk.IsRawSupportRole(role) && strings.TrimSpace(row.Excerpt) != "" {
		return true
	}
	for _, section := range row.ContentSections {
		if retrievalchunk.IsRawSupportRole(section.Role) && strings.TrimSpace(section.Text) != "" {
			return true
		}
	}
	return false
}

func rawInspectionText(row ask.Evidence) string {
	if isSemanticChunkEvidence(row) {
		if retrievalchunk.IsRawSupportRole(row.EvidenceRole) {
			return strings.ToLower(strings.TrimSpace(row.Excerpt))
		}
		return ""
	}
	parts := make([]string, 0, len(row.ContentSections)+1)
	if retrievalchunk.IsRawSupportRole(row.EvidenceRole) && strings.TrimSpace(row.Excerpt) != "" {
		parts = append(parts, row.Excerpt)
	}
	for _, section := range row.ContentSections {
		if retrievalchunk.IsRawSupportRole(section.Role) && strings.TrimSpace(section.Text) != "" {
			parts = append(parts, section.Text)
		}
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

func isSemanticChunkEvidence(row ask.Evidence) bool {
	if row.Chunk == nil || strings.TrimSpace(row.Chunk.ID) == "" || row.Retrieval == nil {
		return false
	}
	for _, lane := range row.Retrieval.Lanes {
		if strings.EqualFold(strings.TrimSpace(lane.Name), researchhybrid.LaneSemantic) {
			return true
		}
	}
	return false
}

func evidenceInspectionScore(row ask.Evidence) float64 {
	if row.Retrieval == nil {
		return 0
	}
	if row.Retrieval.FusedScore != nil {
		return *row.Retrieval.FusedScore
	}
	return float64(row.Retrieval.Score)
}

func evidenceScore(row ask.Evidence) int {
	if row.Retrieval == nil {
		return 0
	}
	return row.Retrieval.Score
}

func cloneInspectionRetrieval(info *ask.RetrievalInfo) *ask.RetrievalInfo {
	if info == nil {
		return nil
	}
	clone := *info
	clone.Lanes = append([]retrieval.RetrievalLane(nil), info.Lanes...)
	clone.Signals = append([]retrieval.RetrievalSignal(nil), info.Signals...)
	clone.MatchedTerms = append([]string(nil), info.MatchedTerms...)
	clone.MissingTerms = append([]string(nil), info.MissingTerms...)
	return &clone
}

func cloneInspectionEvidence(row ask.Evidence) ask.Evidence {
	row.Retrieval = cloneInspectionRetrieval(row.Retrieval)
	if row.Chunk != nil {
		chunk := *row.Chunk
		chunk.ContributingIDs = append([]string(nil), row.Chunk.ContributingIDs...)
		row.Chunk = &chunk
	}
	row.ContentSections = append([]retrieval.ContentSection(nil), row.ContentSections...)
	row.EntityMatches = append([]string(nil), row.EntityMatches...)
	row.Media = append([]retrieval.MediaRef(nil), row.Media...)
	return row
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
