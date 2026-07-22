package brainresearch

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/semanticreadiness"
)

func TestWithSemanticReadinessDiagnosticsKeepsOnlyCatchingUpAggregates(t *testing.T) {
	oldest := time.Date(2026, time.July, 21, 15, 4, 5, 0, time.FixedZone("offset", -6*60*60))
	diagnostics := SemanticReadinessDiagnostics{
		OmittedParentCount:      7,
		EstimatedNotReadyChunks: 19,
		OldestDebtAt:            &oldest,
	}
	b := &Builder{semanticReadiness: semanticreadiness.Decision{State: semanticreadiness.StateCatchingUp}}
	b.WithSemanticReadinessDiagnostics(diagnostics)
	if b.semanticReadinessDiagnostics == nil || b.semanticReadinessDiagnostics.OmittedParentCount != 7 || b.semanticReadinessDiagnostics.EstimatedNotReadyChunks != 19 {
		t.Fatalf("catching-up diagnostics = %#v", b.semanticReadinessDiagnostics)
	}
	if got := b.semanticReadinessDiagnostics.OldestDebtAt; got == nil || !got.Equal(oldest) {
		t.Fatalf("oldest debt = %v, want %v", got, oldest)
	}

	ready := &Builder{semanticReadiness: semanticreadiness.Decision{State: semanticreadiness.StateReady}}
	ready.WithSemanticReadinessDiagnostics(diagnostics)
	if ready.semanticReadinessDiagnostics != nil {
		t.Fatalf("ready builder retained catch-up diagnostics: %#v", ready.semanticReadinessDiagnostics)
	}
}

func TestQueryPlanSemanticReadinessDiagnosticsJSONIsContentFreeAndOptional(t *testing.T) {
	oldest := time.Date(2026, time.July, 21, 21, 4, 5, 0, time.UTC)
	plan := QueryPlan{SemanticReadinessDiagnostics: &SemanticReadinessDiagnostics{
		OmittedParentCount: 2, EstimatedNotReadyChunks: 11, OldestDebtAt: &oldest,
	}}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	got := string(encoded)
	for _, want := range []string{
		`"semantic_readiness_diagnostics"`, `"omitted_parent_count":2`, `"estimated_not_ready_chunks":11`, `"oldest_debt_at":"2026-07-21T21:04:05Z"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("query plan JSON missing %s: %s", want, got)
		}
	}
	empty, err := json.Marshal(QueryPlan{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "semantic_readiness_diagnostics") {
		t.Fatalf("empty query plan retained diagnostics: %s", empty)
	}
}
