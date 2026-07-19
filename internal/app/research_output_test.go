package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
)

func TestWriteResearchPackIncludesCompactSemanticDiagnostics(t *testing.T) {
	t.Parallel()

	pack := brainresearch.Pack{
		Question: "q",
		Mode:     "evidence_only",
		QueryPlan: brainresearch.QueryPlan{
			SemanticMode: semanticconfig.ModeShadow,
			RetrievalLanes: []retrieval.RetrievalLane{
				{Name: "lexical", Status: "used"},
				{Name: "semantic", Status: "disabled", Reason: "provider_unavailable"},
			},
			ShadowComparison:      &brainresearch.ShadowComparison{Status: semanticindex.StateSearched, LexicalCount: 4, HybridCount: 5, Added: []brainresearch.ShadowRankedReference{{SourceKey: "added"}}, Removed: []brainresearch.ShadowRankedReference{}, Reordered: []brainresearch.ShadowRankedReference{{SourceKey: "moved"}}},
			RetryShadowComparison: &brainresearch.ShadowComparison{Status: semanticindex.StateUnavailable, Reason: semanticindex.ReasonProviderUnavailable, Added: []brainresearch.ShadowRankedReference{}, Removed: []brainresearch.ShadowRankedReference{}, Reordered: []brainresearch.ShadowRankedReference{}},
		},
	}

	var out bytes.Buffer
	writeResearchPack(&out, pack)
	for _, want := range []string{
		"Semantic: shadow",
		"lanes lexical=used, semantic=disabled(provider_unavailable)",
		"shadow initial=searched L4/H5 +1/-0/~1",
		"retry=unavailable(provider_unavailable) L0/H0 +0/-0/~0",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q: %s", want, out.String())
		}
	}
}
