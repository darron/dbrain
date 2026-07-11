package brainresearch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/retrieval"
)

func TestPreparedSynthesisOmitsUnusedRelevanceSelection(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(PreparedSynthesis{SchemaVersion: SynthesisSchemaVersion})
	if err != nil {
		t.Fatalf("marshal prepared synthesis: %v", err)
	}
	if strings.Contains(string(encoded), "relevance_selection") {
		t.Fatalf("expected unused additive field to be omitted, got %s", encoded)
	}
	encoded, err = json.Marshal(PreparedSynthesis{
		SchemaVersion: SynthesisSchemaVersion,
		Relevance:     &SynthesisRelevanceSelection{Applied: true, Reason: "required_short_phrase"},
	})
	if err != nil {
		t.Fatalf("marshal prepared synthesis with selection: %v", err)
	}
	if !strings.Contains(string(encoded), `"relevance_selection":{"applied":true,"reason":"required_short_phrase"}`) {
		t.Fatalf("expected applied relevance selection, got %s", encoded)
	}
}

func TestPrepareSynthesisBudgetsEvidenceDeterministically(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	pack := Pack{
		SchemaVersion: SchemaVersion,
		Question:      "What do I know about agent memory?",
		QueryPlan: QueryPlan{
			TextQuery:  "agent memory",
			QueryTerms: []string{"agent", "memory"},
			TagQueries: []string{"agent-memory"},
		},
		Coverage: Coverage{EvidenceCount: 3, RecallNote: "some exact tag evidence"},
		Evidence: []ask.Evidence{
			{SourceKey: "src:one", Kind: "source", Title: "One", URL: "https://example.com/one", NotePath: "sources/web/one.md", Summary: strings.Repeat("primary ", 80)},
			{SourceKey: "src:two", Kind: "source", Title: "Two", URL: "https://example.com/two", NotePath: "sources/web/two.md", Summary: strings.Repeat("secondary ", 80)},
			{SourceKey: "src:related", Kind: "source", Title: "Related", NotePath: "sources/web/related.md", Summary: strings.Repeat("related ", 80), Relationship: "linked source", RelatedTo: "x:one"},
		},
		ExactTagEvidence: []ask.Evidence{
			{SourceKey: "x:tag", Kind: "item", Title: "Tagged", NotePath: "items/x/tag.md", Summary: "exact tag evidence"},
		},
	}

	prepared, err := PrepareSynthesis(cfg, SynthesisOptions{Pack: pack, Model: "cli/test/research", MaxEvidenceChars: 700})
	if err != nil {
		t.Fatalf("PrepareSynthesis: %v", err)
	}
	if prepared.Status != "ok" {
		t.Fatalf("expected ok status, got %q", prepared.Status)
	}
	if !hasString(prepared.Warnings, "evidence_truncated") {
		t.Fatalf("expected evidence_truncated warning, got %#v", prepared.Warnings)
	}
	if prepared.Truncation.EvidenceBudgetChars != 700 || prepared.Truncation.EvidenceCharsUsed == 0 {
		t.Fatalf("unexpected truncation metadata: %+v", prepared.Truncation)
	}
	if len(prepared.Truncation.DroppedSourceKeys) == 0 && prepared.Truncation.PartiallyTrimmedSourceKey == "" {
		t.Fatalf("expected dropped or trimmed evidence: %+v", prepared.Truncation)
	}
	if !strings.Contains(prepared.Input, "## Query Plan") || !strings.Contains(prepared.Input, "source_key: src:one") {
		t.Fatalf("unexpected synthesis input:\n%s", prepared.Input)
	}
	for _, forbidden := range []string{"note_path:", "sources/web/one.md", "items/x/tag.md"} {
		if strings.Contains(prepared.Input, forbidden) {
			t.Fatalf("synthesis input exposed local note path marker %q:\n%s", forbidden, prepared.Input)
		}
	}
	if len(prepared.Citations) == 0 || prepared.Citations[0].SourceKey != "src:one" {
		t.Fatalf("expected citations from included evidence, got %+v", prepared.Citations)
	}
}

func TestPrepareSynthesisLabelsEvidenceContentSections(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	pack := Pack{
		SchemaVersion: SchemaVersion,
		Question:      "What did the long source say about GFANZ?",
		QueryPlan:     QueryPlan{TextQuery: "gfanz", QueryTerms: []string{"gfanz"}},
		Coverage:      Coverage{EvidenceCount: 1},
		Evidence: []ask.Evidence{{
			SourceKey:    "src:long",
			Kind:         "source",
			Title:        "Long source",
			Summary:      "Derived summary without the rare term.",
			Excerpt:      "Raw extract window says Mark Carney discussed GFANZ.",
			EvidenceRole: "raw_extract_window",
			Chunk:        &retrieval.EvidenceChunk{ParentSourceKey: "src:long", Index: 3, Role: "raw_extract_window", Hash: "abc123", Heading: "GFANZ section"},
			ContentSections: []retrieval.ContentSection{
				{Name: "summary_text", Role: "derived", Text: "Derived summary without the rare term.", Chars: 38},
				{Name: "extracted_text_window", Role: "raw", Text: "Raw extract window says Mark Carney discussed GFANZ.", Chars: 52},
			},
		}},
	}

	prepared, err := PrepareSynthesis(cfg, SynthesisOptions{Pack: pack, Model: "cli/test/research", MaxEvidenceChars: 2000})
	if err != nil {
		t.Fatalf("PrepareSynthesis: %v", err)
	}
	for _, want := range []string{"evidence_role: raw_extract_window", "chunk:", "role: raw_extract_window", "content_sections:", "name: summary_text", "role: derived", "name: extracted_text_window", "role: raw", "GFANZ"} {
		if !strings.Contains(prepared.Input, want) {
			t.Fatalf("expected synthesis input to contain %q:\n%s", want, prepared.Input)
		}
	}
}

func TestPrepareSynthesisKeepsAtLeastOneAnchoredRowInContext(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	pack := anchorSynthesisPack([]ask.Evidence{
		{SourceKey: "x:kristof-anchor", Kind: "item", Title: "Kristof row", Author: "Krzysztof Szczawinski @Kristof_Poland", Summary: "Anchored row with direct evidence."},
		{SourceKey: "src:other", Kind: "source", Title: "Other row", Summary: "Other context."},
	})

	prepared, err := PrepareSynthesis(cfg, SynthesisOptions{Pack: pack, Model: "cli/test/research", MaxEvidenceChars: 2000})
	if err != nil {
		t.Fatalf("PrepareSynthesis: %v", err)
	}
	if !hasCitation(prepared.Citations, "x:kristof-anchor") {
		t.Fatalf("expected anchored row citation to survive, citations=%+v input=\n%s", prepared.Citations, prepared.Input)
	}
	if hasString(prepared.Warnings, "anchor_evidence_truncated") {
		t.Fatalf("did not expect anchor truncation warning, got %+v", prepared.Warnings)
	}
	if len(prepared.AnchorContext.Anchors) != 1 || !hasString(prepared.AnchorContext.Anchors[0].CitationSourceKeys, "x:kristof-anchor") {
		t.Fatalf("expected anchor context to report cited anchored row, got %+v", prepared.AnchorContext)
	}
}

func TestPrepareSynthesisWarnsWhenAnchorRowsDropFromTokenBudget(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	pack := anchorSynthesisPack([]ask.Evidence{
		{SourceKey: "src:large", Kind: "source", Title: "Large row", Summary: strings.Repeat("large context ", 80)},
		{SourceKey: "x:kristof-anchor", Kind: "item", Title: "Kristof row", Author: "Krzysztof Szczawinski @Kristof_Poland", Summary: "Anchored row that should be dropped by budget."},
	})

	prepared, err := PrepareSynthesis(cfg, SynthesisOptions{Pack: pack, Model: "cli/test/research", MaxEvidenceChars: 320})
	if err != nil {
		t.Fatalf("PrepareSynthesis: %v", err)
	}
	if !hasString(prepared.Warnings, "anchor_evidence_truncated") {
		t.Fatalf("expected anchor_evidence_truncated warning, got %+v context=%+v truncation=%+v", prepared.Warnings, prepared.AnchorContext, prepared.Truncation)
	}
	if hasCitation(prepared.Citations, "x:kristof-anchor") {
		t.Fatalf("expected anchored row to be absent from citations under tiny budget, citations=%+v", prepared.Citations)
	}
	if len(prepared.AnchorContext.Anchors) != 1 ||
		!hasString(prepared.AnchorContext.Anchors[0].SupportedSourceKeys, "x:kristof-anchor") ||
		!hasString(prepared.AnchorContext.Anchors[0].DroppedSourceKeys, "x:kristof-anchor") ||
		!hasString(prepared.AnchorContext.Anchors[0].Reasons, "token_budget") {
		t.Fatalf("expected dropped anchored row in anchor context, got %+v", prepared.AnchorContext)
	}
}

func TestSynthesisPromptFramesSelectiveCorpusAndAccuracy(t *testing.T) {
	if SynthesisPromptVersion != "brain-research-synthesis-v4" {
		t.Fatalf("unexpected synthesis prompt version: %q", SynthesisPromptVersion)
	}
	for _, want := range []string{
		"intentionally selective",
		"Do not criticize the corpus for not being unbiased",
		"Accuracy matters more than appearing objective",
		"separate supported facts, source claims, opinions, and uncertainty",
		"Do not include local note paths, filesystem paths, or a separate Sources section",
		"Do not mention, summarize, cite, or add a note or section about unrelated candidates",
	} {
		if !strings.Contains(synthesisPrompt, want) {
			t.Fatalf("synthesis prompt missing %q:\n%s", want, synthesisPrompt)
		}
	}
	for _, forbidden := range []string{"source keys and note paths", "Include a short Sources section"} {
		if strings.Contains(synthesisPrompt, forbidden) {
			t.Fatalf("synthesis prompt retained local path instruction %q:\n%s", forbidden, synthesisPrompt)
		}
	}
}

func TestPrepareSynthesisFiltersDistractorsForRequiredShortPhrase(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	t.Setenv("DBRAIN_SUMMARY_MODEL", "cli/test/research")
	matched := func(key string) ask.Evidence {
		return ask.Evidence{SourceKey: key, Kind: "source", Title: key, Summary: "Relevant Anthropic J-space evidence.", Retrieval: &ask.RetrievalInfo{Signals: []ask.RetrievalSignal{{Name: "all_required_research_concepts_matched", Weight: 24}}}}
	}
	pack := Pack{
		SchemaVersion: SchemaVersion,
		Question:      "anthropic j space",
		QueryPlan: QueryPlan{Concepts: []QueryConcept{
			{Key: "anthropic", Preferred: "anthropic", Terms: []string{"anthropic"}, Required: true},
			{Key: "j_space", Preferred: "j space", Terms: []string{"j space", "j-space", "jspace"}, Required: true},
		}},
		Evidence: []ask.Evidence{
			matched("src:j-space"),
			matched("x:j-space"),
			{SourceKey: "src:cursor", Kind: "source", Title: "SpaceX buys Cursor", Summary: "Unrelated candidate."},
		},
		ExactTagEvidence: []ask.Evidence{
			{SourceKey: "x:exact-relevant", Kind: "item", Title: "Anthropic J-space follow-up", Summary: "Relevant follow-up evidence.", Retrieval: &ask.RetrievalInfo{Signals: []ask.RetrievalSignal{{Name: "exact_user_tag_example"}}}},
			{SourceKey: "x:exact-unrelated", Kind: "item", Title: "Anthropic advertising", Summary: "Unrelated candidate.", Retrieval: &ask.RetrievalInfo{Signals: []ask.RetrievalSignal{{Name: "exact_user_tag_example"}}}},
		},
	}
	prepared, err := PrepareSynthesis(cfg, SynthesisOptions{Pack: pack, Model: "cli/test/research"})
	if err != nil {
		t.Fatalf("PrepareSynthesis: %v", err)
	}
	if !prepared.Relevance.Applied || prepared.Relevance.Reason != "required_short_phrase" {
		t.Fatalf("expected relevance selection, got %+v", prepared.Relevance)
	}
	if hasCitation(prepared.Citations, "src:cursor") || hasCitation(prepared.Citations, "x:exact-unrelated") || !hasCitation(prepared.Citations, "src:j-space") || !hasCitation(prepared.Citations, "x:j-space") || !hasCitation(prepared.Citations, "x:exact-relevant") {
		t.Fatalf("unexpected prepared citations: %+v", prepared.Citations)
	}
	if !hasString(prepared.Relevance.ExcludedSourceKeys, "src:cursor") || !hasString(prepared.Relevance.ExcludedSourceKeys, "x:exact-unrelated") || strings.Contains(prepared.Input, "SpaceX buys Cursor") {
		t.Fatalf("expected distractor excluded from synthesis input: relevance=%+v input=%s", prepared.Relevance, prepared.Input)
	}
}

func TestPrepareSynthesisFiltersGeneralConjunctiveDistractorsAndTopicBrief(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	t.Setenv("DBRAIN_SUMMARY_MODEL", "cli/test/research")
	pack := Pack{
		SchemaVersion: SchemaVersion,
		Question:      "Claude Code harness engineering",
		QueryPlan: QueryPlan{
			QueryFamily: queryFamilySoftwareProject,
			Concepts: []QueryConcept{
				{Key: "claude", Preferred: "claude", Terms: []string{"claude"}, Required: true, Role: conceptRoleContent},
				{Key: "harness", Preferred: "harness", Terms: []string{"harness"}, Required: true, Role: conceptRoleContent},
				{Key: "engineering", Preferred: "engineering", Terms: []string{"engineering"}, Required: true, Role: conceptRoleContent},
			},
		},
		Evidence: []ask.Evidence{
			{SourceKey: "src:one", Kind: "source", Title: "Claude Code harness engineering guide"},
			{SourceKey: "src:two", Kind: "source", Summary: "Practical harness engineering for Claude agents."},
			{SourceKey: "src:raw", Kind: "source", Title: "Archived project note", ContentSections: []retrieval.ContentSection{{Name: "extracted_text_window", Role: "raw", Text: "Claude harness engineering implementation details."}}},
			{SourceKey: "src:ads", Kind: "source", Title: "Advertising market news"},
		},
		ExactTagEvidence: []ask.Evidence{
			{SourceKey: "src:two", Kind: "source", UserTags: "harness-engineering,claude"},
			{SourceKey: "src:other", Kind: "source", Title: "General AI topics"},
		},
		TopicBrief: &TopicBrief{Summary: "Broad Claude material including advertising."},
	}
	prepared, err := PrepareSynthesis(cfg, SynthesisOptions{Pack: pack, Model: "cli/test/research"})
	if err != nil {
		t.Fatalf("PrepareSynthesis: %v", err)
	}
	if prepared.Relevance == nil || prepared.Relevance.Reason != "required_concept_intersection" || !prepared.Relevance.TopicBriefExcluded {
		t.Fatalf("expected general relevance selection with topic-brief exclusion, got %+v", prepared.Relevance)
	}
	if !reflect.DeepEqual(prepared.Relevance.SelectedSourceKeys, []string{"src:one", "src:raw", "src:two"}) {
		t.Fatalf("expected unique selected keys, got %+v", prepared.Relevance.SelectedSourceKeys)
	}
	if !containsString(prepared.Warnings, "uncited_topic_brief_excluded") {
		t.Fatalf("expected topic-brief exclusion warning when relevance applies, got %+v", prepared.Warnings)
	}
	for _, forbidden := range []string{"src:ads", "src:other", "Broad Claude material", "## Topic Brief"} {
		if strings.Contains(prepared.Input, forbidden) || hasCitation(prepared.Citations, forbidden) {
			t.Fatalf("expected %q excluded from synthesis input: %s", forbidden, prepared.Input)
		}
	}
	if pack.TopicBrief == nil {
		t.Fatal("PrepareSynthesis must not mutate the original pack")
	}
}

func TestIsCompoundSelectionQuestion(t *testing.T) {
	t.Parallel()

	for _, question := range []string{
		"What happened? What changed?",
		"Explain alpha and also beta",
		"Explain alpha; then beta",
		"Explain alpha as well as beta",
		"Explain alpha plus beta",
		"Explain alpha, additionally explain beta",
		"Explain alpha, what about beta",
	} {
		if !isCompoundSelectionQuestion(question) {
			t.Errorf("expected compound question detection for %q", question)
		}
	}
	if isCompoundSelectionQuestion("What does my brain know about alpha beta?") {
		t.Fatal("simple conjunctive topic question must not be treated as compound")
	}
}

func TestPrepareSynthesisGeneralSelectionFailsOpenForDistributedFamiliesAndDependencies(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	t.Setenv("DBRAIN_SUMMARY_MODEL", "cli/test/research")
	base := Pack{
		SchemaVersion: SchemaVersion,
		Question:      "compare alpha beta",
		QueryPlan: QueryPlan{Concepts: []QueryConcept{
			{Key: "alpha", Terms: []string{"alpha"}, Required: true, Role: conceptRoleContent},
			{Key: "beta", Terms: []string{"beta"}, Required: true, Role: conceptRoleContent},
		}},
		Evidence: []ask.Evidence{
			{SourceKey: "src:one", Title: "alpha beta", Summary: "alpha beta evidence"},
			{SourceKey: "src:two", Title: "alpha beta", Summary: "alpha beta evidence"},
			{SourceKey: "src:other", Title: "gamma", Summary: "gamma evidence"},
		},
	}
	comparison := base
	comparison.QueryPlan.QueryFamily = queryFamilyComparison
	prepared, err := PrepareSynthesis(cfg, SynthesisOptions{Pack: comparison, Model: "cli/test/research"})
	if err != nil {
		t.Fatalf("PrepareSynthesis comparison: %v", err)
	}
	if prepared.Relevance != nil || !hasCitation(prepared.Citations, "src:other") {
		t.Fatalf("comparison selection must fail open, got relevance=%+v citations=%+v", prepared.Relevance, prepared.Citations)
	}

	dependent := base
	dependent.QueryPlan.QueryFamily = queryFamilyEntityTopicOverview
	dependent.Evidence[2].RelatedTo = "src:one"
	prepared, err = PrepareSynthesis(cfg, SynthesisOptions{Pack: dependent, Model: "cli/test/research"})
	if err != nil {
		t.Fatalf("PrepareSynthesis dependent: %v", err)
	}
	if prepared.Relevance != nil || !hasCitation(prepared.Citations, "src:other") {
		t.Fatalf("selection with cross-boundary dependency must fail open, got relevance=%+v citations=%+v", prepared.Relevance, prepared.Citations)
	}
}

func TestPrepareSynthesisGeneralSelectionFailsOpenForPartialConceptEvidence(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	t.Setenv("DBRAIN_SUMMARY_MODEL", "cli/test/research")
	pack := Pack{
		SchemaVersion: SchemaVersion,
		Question:      "What does my brain know about alpha beta?",
		QueryPlan: QueryPlan{
			QueryFamily: queryFamilyEntityTopicOverview,
			Concepts: []QueryConcept{
				{Key: "alpha", Terms: []string{"alpha"}, Required: true, Role: conceptRoleContent},
				{Key: "beta", Terms: []string{"beta"}, Required: true, Role: conceptRoleContent},
			},
		},
		TopicBrief: &TopicBrief{Summary: "Uncited aggregate material."},
		Evidence: []ask.Evidence{
			{SourceKey: "src:one", Summary: "alpha beta direct evidence"},
			{SourceKey: "src:two", Summary: "alpha beta corroboration"},
			{SourceKey: "src:partial", Summary: "alpha-only evidence that may carry distributed context"},
			{SourceKey: "src:other", Summary: "unrelated gamma evidence"},
		},
	}

	prepared, err := PrepareSynthesis(cfg, SynthesisOptions{Pack: pack, Model: "cli/test/research"})
	if err != nil {
		t.Fatalf("PrepareSynthesis: %v", err)
	}
	if prepared.Relevance != nil || !hasCitation(prepared.Citations, "src:partial") || !hasCitation(prepared.Citations, "src:other") {
		t.Fatalf("partial concept evidence must fail open, got relevance=%+v citations=%+v", prepared.Relevance, prepared.Citations)
	}
	if strings.Contains(prepared.Input, "Uncited aggregate material") || !containsString(prepared.Warnings, "uncited_topic_brief_excluded") {
		t.Fatalf("topic brief must be excluded even when row filtering fails open: input=%q warnings=%+v", prepared.Input, prepared.Warnings)
	}
}

func TestSynthesizeRunsConfiguredSummaryPath(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	fakeSummarize := installResearchFakeSummarize(t, root)
	t.Setenv("DBRAIN_TEST_EXPECT_INPUT_DIR", cfg.TempDir)
	t.Setenv("DBRAIN_SUMMARY_MODEL", "")
	t.Setenv("SUMMARIZE_MODEL", "")

	pack := Pack{
		SchemaVersion: SchemaVersion,
		Question:      "What validates manifests?",
		QueryPlan:     QueryPlan{TextQuery: "validates manifests", QueryTerms: []string{"validates", "manifests"}},
		Coverage:      Coverage{EvidenceCount: 1, RecallNote: "one evidence row"},
		Evidence: []ask.Evidence{
			{SourceKey: "src:kubeval", Kind: "source", Title: "kubeval", URL: "https://kubeval.com", NotePath: "sources/web/kubeval.md", Summary: "kubeval validates Kubernetes manifests."},
			{SourceKey: "src:unrelated", Kind: "source", Title: "Unrelated", URL: "https://example.com/unrelated", NotePath: "sources/web/unrelated.md", Summary: "Unrelated prompt candidate."},
		},
	}

	result, err := Synthesize(context.Background(), cfg, SynthesisOptions{
		Pack:             pack,
		Model:            "cli/test/research",
		CLI:              "codex",
		Binary:           fakeSummarize,
		Timeout:          5 * time.Second,
		MaxEvidenceChars: 4000,
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if result.AnswerStatus != "ok" {
		t.Fatalf("unexpected answer status: %+v", result)
	}
	if result.Answer != "kubeval validates manifests [src:kubeval]." {
		t.Fatalf("unexpected answer: %q", result.Answer)
	}
	if len(result.Citations) != 1 || result.Citations[0].SourceKey != "src:kubeval" {
		t.Fatalf("expected answer-used citations only, got %+v", result.Citations)
	}
	if result.PromptVersion != SynthesisPromptVersion || result.ToolVersion != "test-1.0.0" {
		t.Fatalf("unexpected provenance: %+v", result)
	}
}

func anchorSynthesisPack(evidence []ask.Evidence) Pack {
	return Pack{
		SchemaVersion: SchemaVersion,
		Question:      "Can you synthesize @Kristof_Poland?",
		QueryPlan: QueryPlan{
			TextQuery: "Kristof_Poland",
			ProtectedAnchors: []ProtectedAnchor{{
				Kind:        "handle",
				Relation:    "authored_by",
				Raw:         "@Kristof_Poland",
				Canonical:   "kristof_poland",
				ExactTerms:  []string{"@Kristof_Poland", "Kristof_Poland", "kristof_poland"},
				PhraseTerms: []string{"kristof poland"},
			}},
			Concepts: []QueryConcept{{Key: "kristof_poland", Preferred: "@Kristof_Poland", Terms: []string{"@Kristof_Poland", "Kristof_Poland", "kristof_poland"}, Required: true, Role: "anchor"}},
		},
		Coverage: Coverage{EvidenceCount: len(evidence)},
		Evidence: evidence,
	}
}

func hasCitation(citations []Citation, sourceKey string) bool {
	for _, citation := range citations {
		if citation.SourceKey == sourceKey {
			return true
		}
	}
	return false
}

func installResearchFakeSummarize(t *testing.T, root string) string {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	scriptPath := filepath.Join(binDir, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
last=""
prev=""
cli=""
model=""
for arg in "$@"; do
  if [ "$prev" = "--cli" ]; then
    cli="$arg"
  fi
  if [ "$prev" = "--model" ]; then
    model="$arg"
  fi
  last="$arg"
  prev="$arg"
done
if [ "$model" != "cli/test/research" ]; then
  echo "expected configured model, got $model" >&2
  exit 1
fi
if [ ! -f "$last" ]; then
  echo "expected synthesis input file" >&2
  exit 1
fi
case "$last" in
  "$DBRAIN_TEST_EXPECT_INPUT_DIR"/* ) ;;
  *)
    echo "expected synthesis input under $DBRAIN_TEST_EXPECT_INPUT_DIR, got $last" >&2
    exit 1
    ;;
esac
input="$(cat "$last")"
case "$input" in
  *"source_key: src:kubeval"* ) ;;
  *)
    echo "expected source key in synthesis input" >&2
    exit 1
    ;;
esac
printf '%s\n' '{"input":{"model":"cli/test/research"},"extracted":{"url":"","title":"","description":"","siteName":"","content":"context"},"summary":"kubeval validates manifests [src:kubeval]."}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	return scriptPath
}
