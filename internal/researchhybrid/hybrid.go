package researchhybrid

import (
	"strings"

	"github.com/darron/dbrain/internal/retrieval"
)

const (
	LaneLexical  = "lexical"
	LaneSemantic = "semantic"
	LaneExactTag = "exact_tag"

	StatusUsed     = "used"
	StatusDisabled = "disabled"
)

type Options struct {
	UseSemantic     bool
	DisableSemantic bool
}

func LaneStatuses(opts Options) []retrieval.RetrievalLane {
	lanes := []retrieval.RetrievalLane{{
		Name:   LaneLexical,
		Status: StatusUsed,
	}}
	semantic := retrieval.RetrievalLane{
		Name:   LaneSemantic,
		Status: StatusDisabled,
	}
	switch {
	case opts.DisableSemantic:
		semantic.Reason = "disabled_for_lexical_debugging"
	case opts.UseSemantic:
		semantic.Reason = "not_configured"
	default:
		semantic.Reason = "not_requested"
	}
	return append(lanes, semantic)
}

func Merge(lexical []retrieval.EvidenceDocument, semantic []retrieval.EvidenceDocument, limit int) []retrieval.EvidenceDocument {
	if limit <= 0 {
		limit = len(lexical) + len(semantic)
	}
	out := make([]retrieval.EvidenceDocument, 0, min(limit, len(lexical)+len(semantic)))
	byKey := map[string]int{}
	for _, row := range lexical {
		row = WithLane(row, retrieval.RetrievalLane{Name: LaneLexical, Status: StatusUsed})
		key := strings.TrimSpace(row.SourceKey)
		if key == "" {
			continue
		}
		if _, exists := byKey[key]; exists {
			continue
		}
		byKey[key] = len(out)
		out = append(out, row)
		if len(out) >= limit {
			return out
		}
	}
	for _, row := range semantic {
		row = WithLane(row, retrieval.RetrievalLane{Name: LaneSemantic, Status: StatusUsed})
		key := strings.TrimSpace(row.SourceKey)
		if key == "" {
			continue
		}
		if idx, exists := byKey[key]; exists {
			out[idx] = mergeLanes(out[idx], row)
			continue
		}
		byKey[key] = len(out)
		out = append(out, row)
		if len(out) >= limit {
			return out
		}
	}
	return out
}

func WithLane(row retrieval.EvidenceDocument, lane retrieval.RetrievalLane) retrieval.EvidenceDocument {
	lane.Name = strings.TrimSpace(lane.Name)
	if lane.Name == "" {
		return row
	}
	if row.Retrieval == nil {
		row.Retrieval = &retrieval.RetrievalInfo{}
	}
	for _, current := range row.Retrieval.Lanes {
		if strings.EqualFold(current.Name, lane.Name) {
			return row
		}
	}
	row.Retrieval.Lanes = append(row.Retrieval.Lanes, lane)
	return row
}

func WithLaneAll(rows []retrieval.EvidenceDocument, lane retrieval.RetrievalLane) []retrieval.EvidenceDocument {
	out := make([]retrieval.EvidenceDocument, 0, len(rows))
	for _, row := range rows {
		out = append(out, WithLane(row, lane))
	}
	return out
}

func mergeLanes(primary retrieval.EvidenceDocument, secondary retrieval.EvidenceDocument) retrieval.EvidenceDocument {
	if secondary.Retrieval == nil {
		return primary
	}
	for _, lane := range secondary.Retrieval.Lanes {
		primary = WithLane(primary, lane)
	}
	return primary
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
