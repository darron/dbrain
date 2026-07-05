package researcheval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/researchtrace"
	"github.com/darron/dbrain/internal/store"
)

func TestRunAssertsQueryPlanPlannerModesAndCitations(t *testing.T) {
	t.Parallel()

	cfg, st := newResearchEvalStore(t)
	seedResearchEvalItem(t, st, "x:eval-alpha", "Alpha Project", "Alpha Project local memory evidence.")
	planner := fakeResearchPlannerBinary(t, `{"concepts":[{"key":"planner-alpha","preferred":"planner alpha","terms":["planner-alpha","alpha project"],"required":true}],"query_variants":[{"query":"alpha planner special","reason":"test planner"}]}`)

	report, err := Run(context.Background(), cfg, st, Options{Cases: []Case{
		{
			Name:                       "planner assisted query plan",
			Question:                   "Alpha Project",
			Limit:                      5,
			PlannerModel:               "cli/test/planner",
			PlannerBinary:              planner,
			ExpectPlanner:              "model_assisted",
			ExpectQueryTerms:           []string{"alpha", "project"},
			ExpectQueryVariants:        []string{"alpha planner special"},
			ExpectConcepts:             []string{"planner-alpha"},
			MinEvidence:                1,
			ExpectCitationSourceKeys:   []string{"x:eval-alpha"},
			ExpectAnswerStatus:         "ok",
			MinRetrievalSignals:        1,
			ExpectAnySourceKeys:        []string{"x:eval-alpha"},
			ForbidCitationSourceKeys:   []string{"x:not-returned"},
			ExpectPlannerErrorContains: "",
		},
		{
			Name:                "planner disabled baseline",
			Question:            "Alpha Project",
			Limit:               5,
			DisablePlanner:      true,
			ExpectPlanner:       "deterministic",
			ExpectQueryTerms:    []string{"alpha", "project"},
			ForbidQueryVariants: []string{"alpha planner special"},
			MinEvidence:         1,
			ExpectAnySourceKeys: []string{"x:eval-alpha"},
		},
	}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Passed != 2 || report.Failed != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Cases[0].PlannerModel != "cli/test/planner" {
		t.Fatalf("expected planner model in result: %+v", report.Cases[0])
	}
	if len(report.Cases[0].TopEvidence) == 0 || len(report.Cases[0].TopEvidence[0].Signals) == 0 {
		t.Fatalf("expected top retrieval signals in result: %+v", report.Cases[0])
	}
}

func TestRunFailsCitationAssertionWhenCoverageRegresses(t *testing.T) {
	t.Parallel()

	cfg, st := newResearchEvalStore(t)
	seedResearchEvalItem(t, st, "x:eval-alpha", "Alpha Project", "Alpha Project local memory evidence.")

	report, err := Run(context.Background(), cfg, st, Options{Cases: []Case{{
		Name:                     "missing citation",
		Question:                 "Alpha Project",
		Limit:                    5,
		DisablePlanner:           true,
		MinEvidence:              1,
		ExpectCitationSourceKeys: []string{"x:missing-citation"},
		ExpectAnswerStatus:       "ok",
	}}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Passed != 0 || report.Failed != 1 {
		t.Fatalf("expected failing report: %+v", report)
	}
	if !strings.Contains(strings.Join(report.Cases[0].Failures, "\n"), "missing expected citation source_key x:missing-citation") {
		t.Fatalf("expected citation failure, got %+v", report.Cases[0].Failures)
	}
}

func TestTraceProposalGeneratesCasesFromSavedTrace(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	traceDir := writeResearchEvalTrace(t, cfg, "trace-proposal", []ask.Evidence{{
		SourceKey: "x:trace-alpha",
		Kind:      "item",
		Title:     "Trace Alpha",
		Retrieval: &ask.RetrievalInfo{Signals: []ask.RetrievalSignal{{Name: "title_match", Weight: 20}}},
	}}, &brainresearch.SynthesisResult{
		SchemaVersion: brainresearch.SynthesisSchemaVersion,
		Question:      "Alpha Project",
		Answer:        "Alpha answer [x:trace-alpha].",
		AnswerStatus:  "ok",
		Citations:     []brainresearch.Citation{{SourceKey: "x:trace-alpha", Title: "Trace Alpha"}},
		PromptVersion: brainresearch.SynthesisPromptVersion,
		Model:         "ollama/test",
	})

	proposal, err := ProposeFromTrace(traceDir, ProposalOptions{})
	if err != nil {
		t.Fatalf("ProposeFromTrace: %v", err)
	}
	if proposal.SourceType != "trace" || len(proposal.Cases) == 0 {
		t.Fatalf("unexpected proposal: %+v", proposal)
	}
	first := proposal.Cases[0]
	if first.ExpectAnswerStatus != "ok" || !containsFold(first.ExpectCitationSourceKeys, "x:trace-alpha") {
		t.Fatalf("expected answer and citation assertions from trace: %+v", first)
	}
	if len(first.ExpectAnswerText) != 0 {
		t.Fatalf("answer text assertions should be omitted by default: %+v", first)
	}

	withAnswer, err := ProposeFromTrace(traceDir, ProposalOptions{IncludeAnswerText: true})
	if err != nil {
		t.Fatalf("ProposeFromTrace with answer text: %v", err)
	}
	if len(withAnswer.Cases[0].ExpectAnswerText) == 0 {
		t.Fatalf("expected opt-in answer text assertion: %+v", withAnswer.Cases[0])
	}
}

func TestTraceProposalReadsLegacyTraceWithoutSchemaVersion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	traceDir := filepath.Join(root, "legacy-trace")
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy trace: %v", err)
	}
	legacy := map[string]interface{}{
		"run_id":      "legacy-trace",
		"surface":     "cli",
		"question":    "Legacy Trace",
		"stop_reason": "enough_evidence",
		"pack": map[string]interface{}{
			"schema_version": brainresearch.SchemaVersion,
			"question":       "Legacy Trace",
			"query_plan": map[string]interface{}{
				"text_query":     "legacy trace",
				"query_terms":    []string{"legacy", "trace"},
				"planner":        "deterministic",
				"query_family":   "entity_topic_overview",
				"query_variants": []map[string]interface{}{{"query": "legacy trace", "reason": "deterministic"}},
			},
			"coverage": map[string]interface{}{"evidence_count": 1},
			"evidence": []map[string]interface{}{{
				"source_key": "x:legacy-trace",
				"kind":       "item",
				"title":      "Legacy Trace",
			}},
		},
		"synthesis": map[string]interface{}{
			"schema_version": brainresearch.SynthesisSchemaVersion,
			"question":       "Legacy Trace",
			"answer":         "Legacy answer [x:legacy-trace].",
			"answer_status":  "ok",
			"citations":      []map[string]interface{}{{"source_key": "x:legacy-trace", "title": "Legacy Trace"}},
		},
	}
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy trace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(traceDir, "run.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write legacy trace: %v", err)
	}

	proposal, err := ProposeFromTrace(traceDir, ProposalOptions{})
	if err != nil {
		t.Fatalf("ProposeFromTrace legacy: %v", err)
	}
	if len(proposal.Cases) != 1 || proposal.Cases[0].ExpectQueryFamily != "entity_topic_overview" || !containsFold(proposal.Cases[0].ExpectCitationSourceKeys, "x:legacy-trace") {
		t.Fatalf("unexpected legacy trace proposal: %+v", proposal)
	}
}

func TestTranscriptProposalOmitsAnswerTextUnlessRequested(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "transcript.md")
	content := `# dbrain chat transcript

## Turn 1

Status: ` + "`ready`" + `

### Question

What does my brain know about Alpha Project?

### Answer

Alpha answer should not become an assertion by default [x:alpha].

### Citations

- ` + "`x:alpha`" + ` - Alpha

### Research Pack

Query plan:
- text: ` + "`alpha project`" + `
- terms: alpha, project
- planner: deterministic

### Evidence

- ` + "`x:alpha`" + ` - Alpha
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	proposal, err := ProposeFromTranscript(path, ProposalOptions{})
	if err != nil {
		t.Fatalf("ProposeFromTranscript: %v", err)
	}
	if len(proposal.Cases) != 1 {
		t.Fatalf("expected one case: %+v", proposal)
	}
	if len(proposal.Cases[0].ExpectAnswerText) != 0 {
		t.Fatalf("answer text should be omitted by default: %+v", proposal.Cases[0])
	}
	if proposal.Cases[0].ExpectAnswerStatus != "ok" || !containsFold(proposal.Cases[0].ExpectCitationSourceKeys, "x:alpha") {
		t.Fatalf("expected conservative status/citation assertions: %+v", proposal.Cases[0])
	}

	withAnswer, err := ProposeFromTranscript(path, ProposalOptions{IncludeAnswerText: true})
	if err != nil {
		t.Fatalf("ProposeFromTranscript with answer text: %v", err)
	}
	if len(withAnswer.Cases[0].ExpectAnswerText) == 0 {
		t.Fatalf("expected opt-in answer text assertion: %+v", withAnswer.Cases[0])
	}
}

func TestDiffTraceReportsEvidenceKeyChanges(t *testing.T) {
	t.Parallel()

	cfg, st := newResearchEvalStore(t)
	seedResearchEvalItem(t, st, "x:eval-new-alpha", "Alpha Project", "Alpha Project current evidence.")
	traceDir := writeResearchEvalTrace(t, cfg, "trace-diff", []ask.Evidence{{
		SourceKey: "x:eval-old-alpha",
		Kind:      "item",
		Title:     "Old Alpha",
	}}, nil)

	diff, err := DiffTrace(context.Background(), cfg, st, traceDir)
	if err != nil {
		t.Fatalf("DiffTrace: %v", err)
	}
	if !containsFold(diff.Removed, "x:eval-old-alpha") {
		t.Fatalf("expected old key to be removed: %+v", diff)
	}
	if !containsFold(diff.Added, "x:eval-new-alpha") {
		t.Fatalf("expected new key to be added: %+v", diff)
	}
	if !strings.Contains(diff.ProposalCommand, "dbrain eval research propose --from-trace") {
		t.Fatalf("expected proposal command hint: %+v", diff)
	}
}

func TestRunnerEvalPreservesAnchoredEvidenceThroughRetry(t *testing.T) {
	t.Parallel()

	cfg, st := newResearchEvalStore(t)
	seedResearchEvalKristofFixture(t, st)

	report, err := Run(context.Background(), cfg, st, Options{Cases: []Case{{
		Name:           "runner preserves anchored evidence through retry",
		Question:       "Can you synthesize essays from @Kristof_Poland?",
		RawQuestion:    "Can you synthesize essays from @Kristof_Poland?",
		Limit:          4,
		DisablePlanner: true,
		RunWithRunner:  true,
		// Runner retries happen before StopAfterJudge, so these assertions cover the post-merge pack without calling synthesis.
		StopAfterJudge:      true,
		ExpectJudgeVerdict:  "weak_evidence",
		ExpectSourceKeys:    []string{"x:kristof-1", "x:kristof-2"},
		ForbidSourceKeys:    []string{"src:generic-synthesis"},
		ExpectAnySourceKeys: []string{"x:kristof-1", "x:kristof-2"},
	}}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Passed != 1 || report.Failed != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Cases[0].AnswerStatus != "" || report.Cases[0].CitationSourceKeys != nil {
		t.Fatalf("stop-after-judge runner eval should not synthesize: %+v", report.Cases[0])
	}
}

func TestTraceDiffUsesRawQuestionFromChatContinuity(t *testing.T) {
	t.Parallel()

	composed := "Prior evidence titles:\n- Generic synthesis archive\n\nCurrent question: Synthesize those"
	trace := researchtrace.ResearchTrace{
		Question: composed,
		ChatContinuity: &researchtrace.ChatContinuity{
			OriginalQuestion:  "Synthesize those",
			RetrievalQuestion: composed,
			ContinuityAnchors: []brainresearch.ProtectedAnchor{researchEvalKristofAnchor()},
		},
		Pack: &brainresearch.Pack{
			Question: composed,
			QueryPlan: brainresearch.QueryPlan{
				TextQuery:         composed,
				QueryVariants:     []brainresearch.QueryVariant{{Query: composed, Reason: "trace"}},
				ProtectedAnchors:  []brainresearch.ProtectedAnchor{researchEvalKristofAnchor()},
				Planner:           "deterministic",
				Limit:             5,
				MaxCharsPerDoc:    700,
				IncludeTopicBrief: false,
			},
		},
	}

	opts := OptionsFromTrace(trace)
	if opts.Question != composed {
		t.Fatalf("expected trace replay question to remain composed retrieval text, got %q", opts.Question)
	}
	if opts.RawQuestion != "Synthesize those" {
		t.Fatalf("expected raw question from chat continuity, got %q", opts.RawQuestion)
	}
	if len(opts.ContinuityAnchors) != 1 || opts.ContinuityAnchors[0].ResolvedID != "x-author:kristof_poland" {
		t.Fatalf("expected typed continuity anchor from trace, got %#v", opts.ContinuityAnchors)
	}
}

func TestRunnerEvalUsesTypedContinuityAnchorForPronounFollowUp(t *testing.T) {
	t.Parallel()

	cfg, st := newResearchEvalStore(t)
	seedResearchEvalKristofFixture(t, st)
	composed := "Prior evidence titles:\n- Generic synthesis archive\n\nCurrent question: Synthesize those"

	report, err := Run(context.Background(), cfg, st, Options{Cases: []Case{{
		Name:              "runner pronoun follow-up uses typed continuity anchor",
		Question:          composed,
		RawQuestion:       "Synthesize those",
		ContinuityAnchors: []brainresearch.ProtectedAnchor{researchEvalKristofAnchor()},
		Limit:             4,
		DisablePlanner:    true,
		RunWithRunner:     true,
		StopAfterJudge:    true,
		ExpectSourceKeys:  []string{"x:kristof-1", "x:kristof-2"},
		ForbidSourceKeys:  []string{"x:other-author", "src:generic-synthesis"},
	}}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Passed != 1 || report.Failed != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestRunnerEvalReplacesStaleAnchorOnTopicShift(t *testing.T) {
	t.Parallel()

	cfg, st := newResearchEvalStore(t)
	seedResearchEvalKristofFixture(t, st)

	report, err := Run(context.Background(), cfg, st, Options{Cases: []Case{{
		Name:              "runner current handle replaces stale continuity anchor",
		Question:          "Current question: Synthesize @Other_Author",
		RawQuestion:       "Synthesize @Other_Author",
		ContinuityAnchors: []brainresearch.ProtectedAnchor{researchEvalKristofAnchor()},
		Limit:             4,
		DisablePlanner:    true,
		RunWithRunner:     true,
		StopAfterJudge:    true,
		ExpectSourceKeys:  []string{"x:other-author"},
		ForbidSourceKeys:  []string{"x:kristof-1", "x:kristof-2"},
	}}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Passed != 1 || report.Failed != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func newResearchEvalStore(t *testing.T) (config.Config, *store.Store) {
	t.Helper()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return cfg, st
}

func seedResearchEvalItem(t *testing.T, st *store.Store, sourceKey string, title string, text string) {
	t.Helper()

	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	_, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    sourceKey,
		SourceType:   "x_bookmark",
		ExternalID:   strings.TrimPrefix(sourceKey, "x:"),
		CanonicalURL: "https://x.example/" + strings.TrimPrefix(sourceKey, "x:"),
		Title:        title,
		Text:         text,
		SummaryText:  text,
		ContentHash:  sourceKey + "-hash",
		NotePath:     "items/x/" + strings.TrimPrefix(sourceKey, "x:") + ".md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
}

func seedResearchEvalKristofFixture(t *testing.T, st *store.Store) {
	t.Helper()

	ctx := context.Background()
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	items := []model.Item{
		{
			SourceKey:    "x:kristof-1",
			SourceType:   "x_bookmark",
			ExternalID:   "kristof-1",
			CanonicalURL: "https://x.com/Kristof_Poland/status/kristof-1",
			Title:        "Kristof Poland economics note",
			AuthorHandle: "Kristof_Poland",
			AuthorName:   "Krzysztof Szczawinski",
			Text:         "Direct Kristof Poland note about economics and planning.",
			SummaryText:  "Direct Kristof Poland note about economics and planning.",
			ContentHash:  "kristof-1-hash",
			NotePath:     "items/x/kristof-1.md",
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		},
		{
			SourceKey:    "x:kristof-2",
			SourceType:   "x_bookmark",
			ExternalID:   "kristof-2",
			CanonicalURL: "https://x.com/Kristof_Poland/status/kristof-2",
			Title:        "Kristof Poland political note",
			AuthorHandle: "Kristof_Poland",
			AuthorName:   "Krzysztof Szczawinski",
			Text:         "Second Kristof Poland note about politics and incentives.",
			SummaryText:  "Second Kristof Poland note about politics and incentives.",
			ContentHash:  "kristof-2-hash",
			NotePath:     "items/x/kristof-2.md",
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		},
		{
			SourceKey:    "x:other-author",
			SourceType:   "x_bookmark",
			ExternalID:   "other-author",
			CanonicalURL: "https://x.com/Other_Author/status/other-author",
			Title:        "Other Author synthesis note",
			AuthorHandle: "Other_Author",
			AuthorName:   "Other Author",
			Text:         "Other Author note about essays and synthesis.",
			SummaryText:  "Other Author note about essays and synthesis.",
			ContentHash:  "other-author-hash",
			NotePath:     "items/x/other-author.md",
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		},
	}
	for _, item := range items {
		if _, err := st.UpsertItem(ctx, item); err != nil {
			t.Fatalf("upsert fixture item %s: %v", item.SourceKey, err)
		}
	}
	source, err := st.UpsertSource(ctx, model.SourceCandidate{
		SourceKey:     "src:generic-synthesis",
		OriginalURL:   "https://example.com/generic-synthesis",
		CanonicalURL:  "https://example.com/generic-synthesis",
		NormalizedURL: "https://example.com/generic-synthesis",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/generic-synthesis.md",
	})
	if err != nil {
		t.Fatalf("upsert generic source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, source.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/generic-synthesis",
		FinalURL:     "https://example.com/generic-synthesis",
		Title:        "Generic synthesis archive",
		Content:      "Generic essays and synthesis guidance for second-brain workflows.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "test",
		ToolVersion:  "test",
	}, "generic-synthesis-hash"); err != nil {
		t.Fatalf("save generic extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(ctx, source.SourceID, model.SummaryResult{
		Text:          "Generic essays and synthesis guidance for second-brain workflows.",
		Status:        "ok",
		Model:         "test/model",
		PromptVersion: "test",
		Tool:          "test",
		ToolVersion:   "test",
		FetchedAt:     now,
	}); err != nil {
		t.Fatalf("save generic summary: %v", err)
	}
}

func researchEvalKristofAnchor() brainresearch.ProtectedAnchor {
	return brainresearch.ProtectedAnchor{
		Kind:        "handle",
		Relation:    "authored_by",
		Raw:         "@Kristof_Poland",
		Canonical:   "kristof_poland",
		ResolvedID:  "x-author:kristof_poland",
		Source:      "chat_continuity",
		Confidence:  "exact",
		ExactTerms:  []string{"@Kristof_Poland", "Kristof_Poland", "kristof_poland", "x-author:kristof_poland"},
		PhraseTerms: []string{"kristof poland"},
	}
}

func fakeResearchPlannerBinary(t *testing.T, planJSON string) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "summarize")
	if runtime.GOOS == "windows" {
		binary += ".bat"
	}
	summaryJSON, err := json.Marshal(planJSON)
	if err != nil {
		t.Fatalf("marshal planner summary: %v", err)
	}
	payload := `{"input":{"model":"cli/test/planner"},"extracted":{"content":"planner"},"summary":` + string(summaryJSON) + `}`
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ] || [ \"$1\" = \"version\" ]; then echo summarize-test; exit 0; fi\n" +
		"printf '%s\\n' " + shellSingleQuote(payload) + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake planner: %v", err)
	}
	return binary
}

func shellSingleQuote(value string) string {
	value = strings.ReplaceAll(value, "'", "'\"'\"'")
	return "'" + value + "'"
}

func writeResearchEvalTrace(t *testing.T, cfg config.Config, runID string, evidence []ask.Evidence, synthesis *brainresearch.SynthesisResult) string {
	t.Helper()

	pack := brainresearch.Pack{
		SchemaVersion: brainresearch.SchemaVersion,
		Question:      "Alpha Project",
		QueryPlan: brainresearch.QueryPlan{
			TextQuery:         "alpha project",
			QueryTerms:        []string{"alpha", "project"},
			QueryVariants:     []brainresearch.QueryVariant{{Query: "alpha project", Reason: "terms"}},
			Concepts:          []brainresearch.QueryConcept{{Key: "alpha", Terms: []string{"alpha"}, Required: true}},
			Planner:           "deterministic",
			Limit:             5,
			MaxCharsPerDoc:    700,
			IncludeTopicBrief: false,
		},
		Coverage: brainresearch.Coverage{EvidenceCount: len(evidence), DisplayedLimit: 5},
		Evidence: evidence,
	}
	result, err := researchtrace.Write(cfg, researchtrace.ResearchTrace{
		SchemaVersion: researchtrace.SchemaVersion,
		RunID:         runID,
		Surface:       "test",
		Question:      "Alpha Project",
		StartedAt:     time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC),
		CompletedAt:   time.Date(2026, 5, 23, 12, 0, 1, 0, time.UTC),
		Pack:          &pack,
		Synthesis:     synthesis,
		StopReason:    "enough_evidence",
	}, researchtrace.ArtifactContents{}, researchtrace.WriteOptions{Retention: researchtrace.RetentionOptions{KeepAll: true}})
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	return result.Directory
}
