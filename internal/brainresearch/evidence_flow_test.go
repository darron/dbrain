package brainresearch

import (
	"reflect"
	"testing"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/retrieval"
)

func TestBuildEvidenceFlowSeparatesLifecycleStagesAndRowProvenance(t *testing.T) {
	t.Parallel()

	pack := Pack{
		Evidence: []ask.Evidence{
			{SourceKey: "src:kept", Chunk: &retrieval.EvidenceChunk{ParentSourceKey: "src:parent", Index: 2, Role: "raw_extract"}},
			{SourceKey: "src:excluded"},
			{SourceKey: "src:dropped"},
		},
		ExactTagEvidence: []ask.Evidence{{SourceKey: "src:kept"}},
		Inspection: &InspectionResult{
			Applied:             true,
			InspectedSourceKeys: []string{"src:kept", "src:excluded"},
			Rows: []InspectionRow{
				{SourceKey: "src:kept", HydrationStatus: "hydrated", RawSupport: true, RankBefore: 2, RankAfter: 1},
				{SourceKey: "src:excluded", HydrationStatus: "hydration_failed", RankBefore: 1, RankAfter: 2},
			},
			Errors: []InspectionError{{SourceKey: "src:excluded", Error: "missing"}},
		},
	}
	prepared := PreparedSynthesis{
		Relevance: &SynthesisRelevanceSelection{
			Applied:            true,
			SelectedSourceKeys: []string{"src:kept", "src:dropped"},
			ExcludedSourceKeys: []string{"src:excluded"},
		},
		Citations: []Citation{{SourceKey: "src:kept"}},
		Truncation: TruncationMetadata{
			DroppedSourceKeys:         []string{"topic_brief", "src:dropped", "src:kept"},
			PartiallyTrimmedSourceKey: "src:kept",
		},
	}
	synthesis := SynthesisResult{Citations: []Citation{{SourceKey: "src:kept"}}}

	flow := BuildEvidenceFlow(&pack, &prepared, &synthesis, true)

	if flow.SchemaVersion != EvidenceFlowSchemaVersion || flow.InspectionStatus != "partial" || !flow.Retried {
		t.Fatalf("unexpected lifecycle metadata: %#v", flow)
	}
	if !reflect.DeepEqual(flow.RetrievedSourceKeys, []string{"src:dropped", "src:excluded", "src:kept"}) ||
		!reflect.DeepEqual(flow.RelevanceAdmittedSourceKeys, []string{"src:dropped", "src:kept"}) ||
		!reflect.DeepEqual(flow.RelevanceExcludedSourceKeys, []string{"src:excluded"}) ||
		!reflect.DeepEqual(flow.PromptAdmittedSourceKeys, []string{"src:kept"}) ||
		!reflect.DeepEqual(flow.BudgetDroppedSourceKeys, []string{"src:dropped"}) ||
		!reflect.DeepEqual(flow.AnswerCitedSourceKeys, []string{"src:kept"}) {
		t.Fatalf("unexpected evidence stages: %#v", flow)
	}
	if len(flow.RetrievedRows) != 4 || flow.RetrievedRows[0].Lane != "primary" || flow.RetrievedRows[0].ChunkIndex != 2 || flow.RetrievedRows[3].Lane != "exact_tag" {
		t.Fatalf("row provenance was not preserved: %#v", flow.RetrievedRows)
	}
	if len(flow.InvariantErrors) != 0 {
		t.Fatalf("unexpected invariant errors: %#v", flow.InvariantErrors)
	}
}

func TestValidateEvidenceFlowRejectsInvalidAnswerCitationAndStageOverlap(t *testing.T) {
	t.Parallel()

	flow := EvidenceFlow{
		PreparationStatus:           "ran",
		SynthesisStatus:             "ran",
		RetrievedSourceKeys:         []string{"src:one"},
		RelevanceAdmittedSourceKeys: []string{"src:one"},
		RelevanceExcludedSourceKeys: []string{"src:one"},
		PromptAdmittedSourceKeys:    []string{"src:one"},
		BudgetDroppedSourceKeys:     []string{"src:one"},
		AnswerCitedSourceKeys:       []string{"src:invented"},
	}

	errors := ValidateEvidenceFlow(flow)
	if len(errors) != 3 {
		t.Fatalf("expected three invariant failures, got %#v", errors)
	}
}
