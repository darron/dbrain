package researchhybrid

import (
	"testing"

	"github.com/darron/dbrain/internal/retrieval"
)

func TestLaneStatusesKeepSemanticDisabledUntilConfigured(t *testing.T) {
	t.Parallel()

	lanes := LaneStatuses(Options{UseSemantic: true})
	if len(lanes) != 2 {
		t.Fatalf("expected lexical and semantic lane statuses, got %#v", lanes)
	}
	if lanes[0].Name != LaneLexical || lanes[0].Status != StatusUsed {
		t.Fatalf("expected lexical lane used, got %#v", lanes[0])
	}
	if lanes[1].Name != LaneSemantic || lanes[1].Status != StatusDisabled || lanes[1].Reason != "not_configured" {
		t.Fatalf("expected semantic lane not_configured, got %#v", lanes[1])
	}

	lanes = LaneStatuses(Options{DisableSemantic: true})
	if lanes[1].Reason != "disabled_for_lexical_debugging" {
		t.Fatalf("expected explicit disable reason, got %#v", lanes[1])
	}
}

func TestMergeKeepsLexicalOrderBeforeVectorOnlyNearMatches(t *testing.T) {
	t.Parallel()

	lexical := []retrieval.EvidenceDocument{
		{
			SourceKey: "src:exact",
			Title:     "Exact lexical evidence",
			Retrieval: &retrieval.RetrievalInfo{Score: 40, Signals: []retrieval.RetrievalSignal{{
				Name:   "exact_phrase_title",
				Detail: "exact",
				Weight: 15,
			}}},
		},
	}
	semantic := []retrieval.EvidenceDocument{
		{
			SourceKey: "src:near",
			Title:     "Semantic near match",
			Retrieval: &retrieval.RetrievalInfo{Score: 90, Signals: []retrieval.RetrievalSignal{{
				Name:   "vector_similarity",
				Detail: "near meaning",
				Weight: 90,
			}}},
		},
	}

	merged := Merge(lexical, semantic, 2)
	if got := []string{merged[0].SourceKey, merged[1].SourceKey}; got[0] != "src:exact" || got[1] != "src:near" {
		t.Fatalf("expected lexical evidence to remain first, got %#v", got)
	}
	if len(merged[0].Retrieval.Lanes) != 1 || merged[0].Retrieval.Lanes[0].Name != LaneLexical {
		t.Fatalf("expected lexical lane provenance, got %#v", merged[0].Retrieval)
	}
	if len(merged[1].Retrieval.Lanes) != 1 || merged[1].Retrieval.Lanes[0].Name != LaneSemantic {
		t.Fatalf("expected semantic lane provenance, got %#v", merged[1].Retrieval)
	}
}

func TestMergeDeduplicatesAndPreservesBothLaneProvenance(t *testing.T) {
	t.Parallel()

	merged := Merge(
		[]retrieval.EvidenceDocument{{SourceKey: "src:shared", Retrieval: &retrieval.RetrievalInfo{Score: 20}}},
		[]retrieval.EvidenceDocument{{SourceKey: "src:shared", Retrieval: &retrieval.RetrievalInfo{Score: 70}}},
		5,
	)
	if len(merged) != 1 {
		t.Fatalf("expected duplicate source to merge, got %#v", merged)
	}
	var names []string
	for _, lane := range merged[0].Retrieval.Lanes {
		names = append(names, lane.Name)
	}
	if len(names) != 2 || names[0] != LaneLexical || names[1] != LaneSemantic {
		t.Fatalf("expected lexical and semantic provenance, got %#v", names)
	}
}
