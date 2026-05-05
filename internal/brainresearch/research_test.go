package brainresearch

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func TestBuildIncludesSourceExactTagEvidence(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
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
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	now := time.Now().UTC()
	source, err := st.UpsertSource(ctx, model.SourceCandidate{
		SourceKey:     "src:test-source-tag-only",
		OriginalURL:   "https://example.com/agent-memory",
		CanonicalURL:  "https://example.com/agent-memory",
		NormalizedURL: "https://example.com/agent-memory",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-source-tag-only.md",
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, source.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/agent-memory",
		FinalURL:     "https://example.com/agent-memory",
		Title:        "Agent Memory Source",
		Content:      "Long-form source material about agent memory and retrieval.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "test",
		ToolVersion:  "test",
	}, "source-tag-only-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	if err := st.SaveSourceUserTags(ctx, source.SourceID, "agent-memory"); err != nil {
		t.Fatalf("save source tags: %v", err)
	}

	pack, err := Build(ctx, cfg, st, Options{
		Question:       "What do I have in my brain about agent memory?",
		Limit:          4,
		MaxCharsPerDoc: 120,
		DisablePlanner: true,
	})
	if err != nil {
		t.Fatalf("build pack: %v", err)
	}

	if pack.SchemaVersion != SchemaVersion {
		t.Fatalf("unexpected schema version: %q", pack.SchemaVersion)
	}
	if len(pack.ExactTagEvidence) != 1 {
		t.Fatalf("expected one source exact tag example, got %#v", pack.ExactTagEvidence)
	}
	example := pack.ExactTagEvidence[0]
	if example.SourceKey != "src:test-source-tag-only" || example.Kind != "source" {
		t.Fatalf("expected source exact tag example, got %#v", example)
	}
	if example.UserTags != "agent-memory" {
		t.Fatalf("expected source user tags, got %#v", example)
	}
	if example.Retrieval == nil || len(example.Retrieval.Signals) == 0 {
		t.Fatalf("expected retrieval signal on exact tag example, got %#v", example)
	}
}

func TestBuildChatFollowupIgnoresPriorEvidenceTitleNoise(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
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
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	now := time.Now().UTC()
	saveSource := func(key string, title string, sourceType string, content string) {
		t.Helper()
		url := "https://example.com/" + strings.TrimPrefix(key, "src:")
		source, err := st.UpsertSource(ctx, model.SourceCandidate{
			SourceKey:     key,
			OriginalURL:   url,
			CanonicalURL:  url,
			NormalizedURL: url,
			SourceType:    sourceType,
			Domain:        "example.com",
			NotePath:      "sources/" + sourceType + "/" + strings.TrimPrefix(key, "src:") + ".md",
		})
		if err != nil {
			t.Fatalf("upsert source %s: %v", key, err)
		}
		if _, err := st.SaveSourceExtraction(ctx, source.SourceID, model.ExtractResult{
			CanonicalURL: url,
			FinalURL:     url,
			Title:        title,
			Content:      content,
			Status:       "ok",
			FetchedAt:    now,
			Tool:         "test",
			ToolVersion:  "test",
		}, key+"-hash"); err != nil {
			t.Fatalf("save source extraction %s: %v", key, err)
		}
	}

	saveSource(
		"src:litestream",
		"benbjohnson/litestream",
		"github",
		"Litestream provides streaming replication for SQLite databases and backs them up to object storage.",
	)
	saveSource(
		"src:marmot",
		"Marmot V2 - Distributed SQLite Replicator - Nextra",
		"web",
		"Marmot is a distributed SQLite replicator and appears in prior evidence titles.",
	)
	saveSource(
		"src:colmi",
		"colmi_r02_client API documentation",
		"web",
		"Client API documentation from a Safari tab that should not steer this follow-up.",
	)

	pack, err := Build(ctx, cfg, st, Options{
		Question: `Current question: what about litestream?

Recent user questions:
- sqlite replication

Prior evidence titles for query focus:
- Marmot V2 - Distributed SQLite Replicator - Nextra | web
- maxpert/marmot | github
- colmi_r02_client API documentation | web
- colmi_r02_client API documentation | safari_tab`,
		Limit:          3,
		MaxCharsPerDoc: 200,
		DisablePlanner: true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if pack.QueryPlan.TextQuery != "litestream sqlite replication" {
		t.Fatalf("expected clean chat follow-up query, got %q", pack.QueryPlan.TextQuery)
	}
	for _, noisy := range []string{"marmot", "colmi", "api", "safari"} {
		if containsString(pack.QueryPlan.QueryTerms, noisy) || hasQueryVariantContaining(pack.QueryPlan.QueryVariants, noisy) {
			t.Fatalf("did not expect prior evidence term %q in query plan %#v", noisy, pack.QueryPlan)
		}
	}
	if len(pack.Evidence) == 0 || pack.Evidence[0].SourceKey != "src:litestream" {
		t.Fatalf("expected litestream evidence first, got %#v", pack.Evidence)
	}
}

func TestBuildResearchStrategyExpandsPeopleEventQuery(t *testing.T) {
	t.Parallel()

	hints := ask.Hints("Can you find the information in my brain about the Calgary father that killed two kids")
	strategy := buildResearchStrategy("Can you find the information in my brain about the Calgary father that killed two kids", hints)

	if !hasQueryVariant(strategy.Variants, "calgary father charged killing children") {
		t.Fatalf("expected charged killing children variant, got %#v", strategy.Variants)
	}
	if !hasQueryVariant(strategy.Variants, "calgary father son daughter") {
		t.Fatalf("expected son/daughter variant, got %#v", strategy.Variants)
	}
	if !hasConceptKey(strategy.Concepts, "children") || !hasConceptKey(strategy.Concepts, "kill") || !hasConceptKey(strategy.Concepts, "father") {
		t.Fatalf("expected people-event concepts, got %#v", strategy.Concepts)
	}
}

func TestBuildResearchStrategyExpandsGenericTechnicalQuery(t *testing.T) {
	t.Parallel()

	hints := ask.Hints("K8s Helm alternatives")
	strategy := buildResearchStrategy("K8s Helm alternatives", hints)

	if !hasQueryVariant(strategy.Variants, "k8s helm alternatives") {
		t.Fatalf("expected normalized query variant, got %#v", strategy.Variants)
	}
	if !hasQueryVariant(strategy.Variants, "kubernetes helm alternative") {
		t.Fatalf("expected preferred concept variant, got %#v", strategy.Variants)
	}
	if !hasConceptTerm(strategy.Concepts, "kubernetes", "k8s") ||
		!hasConceptKey(strategy.Concepts, "helm") ||
		!hasConceptTerm(strategy.Concepts, "alternative", "replacement") {
		t.Fatalf("expected generic technical concepts, got %#v", strategy.Concepts)
	}
}

func TestBuildResearchStrategyDropsCorpusFrameTermsWhenPlannerFallsBack(t *testing.T) {
	t.Parallel()

	question := "What models should I use with Hermes agent? Are there favored models in my research?"
	hints := ask.Hints(question)
	strategy := buildResearchStrategy(question, hints)

	if strategy.Variants[0].Query != "model hermes agent" {
		t.Fatalf("expected clean normalized model query first, got %#v", strategy.Variants)
	}
	if !hasQueryVariant(strategy.Variants, "llm model stack hermes agent") ||
		!hasQueryVariant(strategy.Variants, "qwen gpt hermes agent") {
		t.Fatalf("expected model-strategy fallback variants, got %#v", strategy.Variants)
	}
	for _, noisy := range []string{"should", "favored", "research"} {
		if hasConceptKey(strategy.Concepts, noisy) || hasQueryVariantContaining(strategy.Variants, noisy) {
			t.Fatalf("did not expect noisy corpus-frame term %q in strategy %#v", noisy, strategy)
		}
	}
	if !hasConceptKey(strategy.Concepts, "model") || !hasConceptKey(strategy.Concepts, "hermes") || !hasConceptKey(strategy.Concepts, "agent") {
		t.Fatalf("expected model/hermes/agent concepts, got %#v", strategy.Concepts)
	}
}

func TestBuildResearchStrategyMergesModelPlannerOutput(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	b := New(cfg, nil)
	fakeSummarize := filepath.Join(t.TempDir(), "summarize")
	if runtime.GOOS == "windows" {
		fakeSummarize += ".bat"
	}
	script := `#!/bin/sh
last=""
for arg in "$@"; do
  last="$arg"
done
case "$last" in
  "$DBRAIN_TEST_EXPECT_INPUT_DIR"/* ) ;;
  *)
    echo "expected planner input under $DBRAIN_TEST_EXPECT_INPUT_DIR, got $last" >&2
    exit 1
    ;;
esac
printf '%s\n' '{"input":{"model":"cli/test/planner"},"extracted":{"content":"planner"},"summary":"{\"concepts\":[{\"key\":\"kubernetes\",\"preferred\":\"kubernetes\",\"terms\":[\"k8s\",\"kubernetes\"],\"required\":true},{\"key\":\"alternative\",\"preferred\":\"alternative\",\"terms\":[\"alternative\",\"replacement\",\"tanka\",\"kustomize\"],\"required\":true}],\"query_variants\":[{\"query\":\"kubernetes helm alternatives\",\"reason\":\"abbr expansion\"},{\"query\":\"helm tanka kustomize\",\"reason\":\"adjacent tools\"},{\"query\":\"src:bad\",\"reason\":\"bad source key\"}]}"}'
`
	if err := os.WriteFile(fakeSummarize, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}
	t.Setenv("DBRAIN_TEST_EXPECT_INPUT_DIR", cfg.TempDir)
	hints := ask.Hints("K8s Helm alternatives")
	strategy := b.buildResearchStrategy(context.Background(), "K8s Helm alternatives", hints, Options{
		PlannerModel:   "cli/test/planner",
		PlannerTimeout: 5 * time.Second,
		PlannerBinary:  fakeSummarize,
	})

	if strategy.Planner != "model_assisted" {
		t.Fatalf("expected model-assisted planner, got %#v", strategy)
	}
	if strategy.PlannerModel != "cli/test/planner" || strategy.PlannerError != "" {
		t.Fatalf("unexpected planner metadata: %#v", strategy)
	}
	if !hasQueryVariant(strategy.Variants, "helm tanka kustomize") {
		t.Fatalf("expected model planner variant, got %#v", strategy.Variants)
	}
	if hasQueryVariant(strategy.Variants, "src:bad") {
		t.Fatalf("source-key planner variant should be rejected, got %#v", strategy.Variants)
	}
	if !hasConceptTerm(strategy.Concepts, "alternative", "kustomize") {
		t.Fatalf("expected merged model concept aliases, got %#v", strategy.Concepts)
	}
}

func TestMergeQueryConceptsCoalescesPlannerDuplicateConcepts(t *testing.T) {
	t.Parallel()

	base := buildQueryConcepts([]string{"calgary", "father", "killed", "kids"})
	merged := mergeQueryConcepts(base, []QueryConcept{
		{Key: "location", Preferred: "Calgary", Terms: []string{"Calgary"}, Required: true},
		{Key: "subject_role", Preferred: "father", Terms: []string{"dad", "parent"}, Required: true},
		{Key: "action_severity", Preferred: "killed", Terms: []string{"murdered"}, Required: true},
		{Key: "victim_group", Preferred: "children", Terms: []string{"children", "son", "daughter"}, Required: true},
	})

	if hasConceptKey(merged, "location") || hasConceptKey(merged, "subject_role") || hasConceptKey(merged, "action_severity") {
		t.Fatalf("expected overlapping planner concepts to merge into deterministic concepts, got %#v", merged)
	}
	if !hasConceptTerm(merged, "father", "dad") ||
		!hasConceptTerm(merged, "kill", "murdered") ||
		!hasConceptTerm(merged, "children", "son") {
		t.Fatalf("expected planner aliases to augment existing concepts, got %#v", merged)
	}
	if hasConceptTerm(merged, "calgary", "location") ||
		hasConceptTerm(merged, "father", "subject_role") ||
		hasConceptTerm(merged, "kill", "action_severity") {
		t.Fatalf("planner slot labels should not become search terms, got %#v", merged)
	}
	if len(merged) != len(base) {
		t.Fatalf("expected no new concept lanes for semantic duplicates, got base=%#v merged=%#v", base, merged)
	}
}

func TestMergeQueryConceptsTreatsModelOnlyExpansionsAsOptional(t *testing.T) {
	t.Parallel()

	base := buildQueryConcepts([]string{"father", "killed", "kids"})
	merged := mergeQueryConcepts(base, []QueryConcept{
		{Key: "filicide", Preferred: "father killed children", Terms: []string{"filicide", "double homicide"}, Required: true},
	})

	var found QueryConcept
	for _, concept := range merged {
		if concept.Key == "filicide" {
			found = concept
			break
		}
	}
	if found.Key == "" {
		t.Fatalf("expected model-only expansion concept, got %#v", merged)
	}
	if found.Required {
		t.Fatalf("model-only expansions should not become hard retrieval constraints: %#v", found)
	}
}

func TestBuildRanksPeopleEventEvidenceByConceptCoverage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
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
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	now := time.Now().UTC()
	saveTestSource := func(key string, title string, content string) {
		t.Helper()
		source, err := st.UpsertSource(ctx, model.SourceCandidate{
			SourceKey:     key,
			OriginalURL:   "https://example.com/" + strings.TrimPrefix(key, "src:"),
			CanonicalURL:  "https://example.com/" + strings.TrimPrefix(key, "src:"),
			NormalizedURL: "https://example.com/" + strings.TrimPrefix(key, "src:"),
			SourceType:    "web",
			Domain:        "example.com",
			NotePath:      "sources/web/" + strings.TrimPrefix(key, "src:") + ".md",
		})
		if err != nil {
			t.Fatalf("upsert source %s: %v", key, err)
		}
		if _, err := st.SaveSourceExtraction(ctx, source.SourceID, model.ExtractResult{
			CanonicalURL: "https://example.com/" + strings.TrimPrefix(key, "src:"),
			FinalURL:     "https://example.com/" + strings.TrimPrefix(key, "src:"),
			Title:        title,
			Content:      content,
			Status:       "ok",
			FetchedAt:    now,
			Tool:         "test",
			ToolVersion:  "test",
		}, key+"-hash"); err != nil {
			t.Fatalf("save source extraction %s: %v", key, err)
		}
		if _, err := st.SaveSourceSummary(ctx, source.SourceID, model.SummaryResult{
			Text:          content,
			Status:        "ok",
			Model:         "test-model",
			PromptVersion: "test",
			Tool:          "test",
			ToolVersion:   "test",
			FetchedAt:     now,
		}); err != nil {
			t.Fatalf("save source summary %s: %v", key, err)
		}
	}

	saveTestSource(
		"src:wrong-calgary-homicide",
		"So heartbreaking: Woman killed by husband planned to leave him after Christmas Day fight",
		"A Calgary homicide story about a husband and wife. It is a different incident and does not involve children.",
	)
	saveTestSource(
		"src:correct-calgary-children",
		"Father charged with killing young son, daughter who were found in vehicle in Calgary",
		"Police said a Calgary father was charged with first-degree murder after his two young children, a son and daughter, were found dead in a vehicle.",
	)

	includeTopic := false
	pack, err := Build(ctx, cfg, st, Options{
		Question:       "Can you find the information in my brain about the Calgary father that killed two kids",
		Limit:          2,
		MaxCharsPerDoc: 240,
		IncludeTopic:   &includeTopic,
		DisablePlanner: true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(pack.Evidence) == 0 {
		t.Fatal("expected evidence")
	}
	if pack.Evidence[0].SourceKey != "src:correct-calgary-children" {
		t.Fatalf("expected correct people-event source first, got %#v", pack.Evidence)
	}
	if pack.Evidence[0].Retrieval == nil || !hasRetrievalSignal(pack.Evidence[0].Retrieval.Signals, "all_required_research_concepts_matched") {
		t.Fatalf("expected concept coverage signal, got %#v", pack.Evidence[0].Retrieval)
	}
}

func hasQueryVariant(variants []QueryVariant, query string) bool {
	for _, variant := range variants {
		if variant.Query == query {
			return true
		}
	}
	return false
}

func hasQueryVariantContaining(variants []QueryVariant, term string) bool {
	for _, variant := range variants {
		for _, field := range strings.Fields(variant.Query) {
			if field == term {
				return true
			}
		}
	}
	return false
}

func hasConceptKey(concepts []QueryConcept, key string) bool {
	for _, concept := range concepts {
		if concept.Key == key {
			return true
		}
	}
	return false
}

func hasConceptTerm(concepts []QueryConcept, key string, term string) bool {
	for _, concept := range concepts {
		if concept.Key != key {
			continue
		}
		for _, got := range concept.Terms {
			if got == term {
				return true
			}
		}
	}
	return false
}

func hasRetrievalSignal(signals []ask.RetrievalSignal, name string) bool {
	for _, signal := range signals {
		if signal.Name == name {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
