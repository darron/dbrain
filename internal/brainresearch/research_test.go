package brainresearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/queryterms"
	"github.com/darron/dbrain/internal/retrieval"
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
	if len(pack.QueryPlan.RetrievalLanes) < 2 || pack.QueryPlan.RetrievalLanes[0].Name != "lexical" || pack.QueryPlan.RetrievalLanes[1].Name != "semantic" || pack.QueryPlan.RetrievalLanes[1].Status != "disabled" {
		t.Fatalf("expected lexical used and semantic disabled lane metadata, got %#v", pack.QueryPlan.RetrievalLanes)
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
	if len(example.Retrieval.Lanes) != 1 || example.Retrieval.Lanes[0].Name != "exact_tag" {
		t.Fatalf("expected exact tag lane on tag example, got %#v", example.Retrieval)
	}
}

func TestBuildPromotesExactTagCandidatesIntoPrimaryEvidence(t *testing.T) {
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
	saveSource := func(key string, title string, summary string, tags string) {
		t.Helper()
		url := "https://example.com/" + strings.TrimPrefix(key, "src:")
		source, err := st.UpsertSource(ctx, model.SourceCandidate{
			SourceKey:     key,
			OriginalURL:   url,
			CanonicalURL:  url,
			NormalizedURL: url,
			SourceType:    "web",
			Domain:        "example.com",
			NotePath:      "sources/web/" + strings.TrimPrefix(key, "src:") + ".md",
		})
		if err != nil {
			t.Fatalf("upsert source %s: %v", key, err)
		}
		if _, err := st.SaveSourceExtraction(ctx, source.SourceID, model.ExtractResult{
			CanonicalURL: url,
			FinalURL:     url,
			Title:        title,
			Content:      summary,
			Status:       "ok",
			FetchedAt:    now,
			Tool:         "test",
			ToolVersion:  "test",
		}, key+"-extract-hash"); err != nil {
			t.Fatalf("save source extraction %s: %v", key, err)
		}
		if _, err := st.SaveSourceSummary(ctx, source.SourceID, model.SummaryResult{
			Text:          summary,
			Status:        "ok",
			Model:         "test-model",
			PromptVersion: "test",
			Tool:          "test",
			ToolVersion:   "test",
			FetchedAt:     now,
		}); err != nil {
			t.Fatalf("save source summary %s: %v", key, err)
		}
		if tags != "" {
			if err := st.SaveSourceUserTags(ctx, source.SourceID, tags); err != nil {
				t.Fatalf("save source tags %s: %v", key, err)
			}
		}
	}

	saveSource(
		"src:strict-mac-recorder-one",
		"Mac screen recording software roundup",
		"This source mentions screen recording software for Mac but has no saved category tag.",
		"",
	)
	saveSource(
		"src:strict-mac-recorder-two",
		"Another Mac screen recording software note",
		"This source also mentions screen recording software for Mac and competes lexically.",
		"",
	)
	saveSource(
		"src:tagged-demo-recorder",
		"Tagged demo product",
		"Create polished demo videos with cursor follow, zooms, and voiceover for product walkthroughs.",
		"demo-video,screen-recorder,macos-apps,developer-tools",
	)

	includeTopic := false
	pack, err := Build(ctx, cfg, st, Options{
		Question:       "screen recording software mac",
		Limit:          3,
		MaxCharsPerDoc: 180,
		IncludeTopic:   &includeTopic,
		DisablePlanner: true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var tagged *ask.Evidence
	for i := range pack.Evidence {
		if pack.Evidence[i].SourceKey == "src:tagged-demo-recorder" {
			tagged = &pack.Evidence[i]
			break
		}
	}
	if tagged == nil {
		t.Fatalf("expected exact-tagged source to survive primary evidence cap, got %#v", pack.Evidence)
	}
	if tagged.Retrieval == nil || !hasRetrievalLane(tagged.Retrieval.Lanes, "exact_tag") {
		t.Fatalf("expected exact-tag lane on promoted evidence, got %#v", tagged)
	}
	if !hasRetrievalSignal(tagged.Retrieval.Signals, "exact_tag_primary_candidate") {
		t.Fatalf("expected exact-tag primary boost signal, got %#v", tagged.Retrieval)
	}
	if !hasRetrievalLane(pack.QueryPlan.RetrievalLanes, "exact_tag") {
		t.Fatalf("expected query plan to report exact-tag lane, got %#v", pack.QueryPlan.RetrievalLanes)
	}
}

func TestExactTagEvidenceExamplesRankByQueryCoverage(t *testing.T) {
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
	saveTaggedSource := func(key string, title string, summary string, tags string, fetchedAt time.Time) {
		t.Helper()
		url := "https://example.com/" + strings.TrimPrefix(key, "src:")
		source, err := st.UpsertSource(ctx, model.SourceCandidate{
			SourceKey:     key,
			OriginalURL:   url,
			CanonicalURL:  url,
			NormalizedURL: url,
			SourceType:    "web",
			Domain:        "example.com",
			NotePath:      "sources/web/" + strings.TrimPrefix(key, "src:") + ".md",
		})
		if err != nil {
			t.Fatalf("upsert source %s: %v", key, err)
		}
		if _, err := st.SaveSourceExtraction(ctx, source.SourceID, model.ExtractResult{
			CanonicalURL: url,
			FinalURL:     url,
			Title:        title,
			Content:      summary,
			Status:       "ok",
			FetchedAt:    fetchedAt,
			Tool:         "test",
			ToolVersion:  "test",
		}, key+"-extract-hash"); err != nil {
			t.Fatalf("save source extraction %s: %v", key, err)
		}
		if _, err := st.SaveSourceSummary(ctx, source.SourceID, model.SummaryResult{
			Text:          summary,
			Status:        "ok",
			Model:         "test-model",
			PromptVersion: "test",
			Tool:          "test",
			ToolVersion:   "test",
			FetchedAt:     fetchedAt,
		}); err != nil {
			t.Fatalf("save source summary %s: %v", key, err)
		}
		if err := st.SaveSourceUserTags(ctx, source.SourceID, tags); err != nil {
			t.Fatalf("save source tags %s: %v", key, err)
		}
	}

	saveTaggedSource("src:z-noise-one", "Recent screen recording note", "A generic capture workflow note with clips and sharing steps.", "screen-recording", now.Add(3*time.Minute))
	saveTaggedSource("src:y-noise-two", "Recent recording workflow", "Another capture workflow mention with editing and captions.", "screen-recording", now.Add(2*time.Minute))
	saveTaggedSource("src:x-noise-three", "Recent screen capture note", "Capture-only workflow notes with no platform detail.", "screen-recording", now.Add(time.Minute))
	saveTaggedSource("src:a-relevant", "Kite demo recorder", "Create polished demo videos with cursor follow for a native app.", "screen-recorder,macos-apps,developer-tools", now.Add(-time.Hour))

	hints := ask.Hints("screen recording software mac")
	examples, err := New(cfg, st).buildExactTagEvidence(ctx, "", hints, nil, 160)
	if err != nil {
		t.Fatalf("buildExactTagEvidence: %v", err)
	}
	if len(examples) != maxExactTagEvidence {
		t.Fatalf("expected capped exact tag examples, got %#v", examples)
	}
	if examples[0].SourceKey != "src:a-relevant" {
		t.Fatalf("expected query-covered exact tag example first, got %#v", examples)
	}
}

func TestBuildResearchStrategyExpandsSoftwareMacAndRecorderAliases(t *testing.T) {
	t.Parallel()

	hints := ask.Hints("screen recording software mac")
	strategy := buildResearchStrategy("screen recording software mac", hints)

	if !hasConceptTerm(strategy.Concepts, "software", "app") ||
		!hasConceptTerm(strategy.Concepts, "software", "tools") ||
		!hasConceptTerm(strategy.Concepts, "mac", "macos") ||
		!hasConceptTerm(strategy.Concepts, "video", "recorder") {
		t.Fatalf("expected software/mac/recorder aliases, got %#v", strategy.Concepts)
	}
}

func TestBuildFindsTranscriptBackedMediaEvidence(t *testing.T) {
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
	if !st.HasFTS() {
		t.Skip("FTS is not available")
	}

	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-generic-red-balloon-title",
		SourceType:   "x_bookmark",
		ExternalID:   "test-generic-red-balloon-title",
		CanonicalURL: "https://x.com/darron/status/test-generic-red-balloon-title",
		Title:        "Red balloon promise discussion",
		AuthorHandle: "darron",
		AuthorName:   "Darron",
		Text:         "A generic saved post with the title words but no recording claim.",
		SummaryText:  "This generic note mentions a red balloon promise but has no transcript evidence.",
		ContentHash:  "test-generic-red-balloon-title-hash",
		NotePath:     "items/x/2026/test-generic-red-balloon-title.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert generic item: %v", err)
	}
	upsert, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-recording-red-balloon",
		SourceType:   "x_bookmark",
		ExternalID:   "test-recording-red-balloon",
		CanonicalURL: "https://x.com/darron/status/test-recording-red-balloon",
		Title:        "Darron recording",
		AuthorHandle: "darron",
		AuthorName:   "Darron",
		Text:         "Short clip.",
		ArticleTitle: model.XMediaTranscriptArticleTitle,
		ArticleText:  "Transcript:\n\nDarron is saying the red balloon promise out loud.",
		SummaryText:  "A short saved video clip.",
		ContentHash:  "test-recording-red-balloon-hash",
		NotePath:     "items/x/2026/test-recording-red-balloon.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.SaveXHydration(ctx, upsert.ItemID, model.XHydration{
		FullText:  "Short clip.",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"source":"graphql",
			"snapshot":{
				"id":"test-recording-red-balloon",
				"text":"Short clip.",
				"media_objects":[
					{"type":"video","url":"https://video.twimg.com/ext/red-balloon.mp4","expanded_url":"https://x.com/darron/status/test-recording-red-balloon/video/1","width":1280,"height":720}
				]
			}
		}`,
	}); err != nil {
		t.Fatalf("save x hydration: %v", err)
	}
	if err := st.SaveXMediaTranscriptionState(ctx, upsert.ItemID, model.XMediaTranscriptStatusOK, "", now); err != nil {
		t.Fatalf("save transcript state: %v", err)
	}

	includeTopic := false
	pack, err := Build(ctx, cfg, st, Options{
		Question:       "Is there a recording of Darron saying red balloon promise?",
		Limit:          4,
		MaxCharsPerDoc: 240,
		IncludeTopic:   &includeTopic,
		DisablePlanner: true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(pack.Evidence) == 0 || pack.Evidence[0].SourceKey != "x:test-recording-red-balloon" {
		t.Fatalf("expected transcript-backed media evidence first, got %#v", pack.Evidence)
	}
	if pack.QueryPlan.QueryFamily != queryFamilyMediaEvidence {
		t.Fatalf("expected media query family, got %#v", pack.QueryPlan)
	}
	if !strings.Contains(pack.Evidence[0].Excerpt, "red balloon promise") {
		t.Fatalf("expected transcript phrase in excerpt, got %q", pack.Evidence[0].Excerpt)
	}
	if len(pack.Evidence[0].Media) != 1 || pack.Evidence[0].Media[0].MediaType != "video" {
		t.Fatalf("expected embedded media ref, got %#v", pack.Evidence[0].Media)
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
	if strategy.Family != queryFamilyPeopleEventLookup {
		t.Fatalf("expected people-event family, got %#v", strategy)
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
	if strategy.Family != queryFamilyComparison {
		t.Fatalf("expected comparison family, got %#v", strategy)
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

func TestCerebrasQuestionKeepsOnlyDiscriminativeRequiredConcepts(t *testing.T) {
	terms := queryterms.Terms("What can we learn from the Cerebras articles about their new knowledge base system and ontology, and apply to dbrain?")
	got := buildQueryConcepts(terms)

	byKey := map[string]QueryConcept{}
	var required []string
	for _, concept := range got {
		byKey[concept.Key] = concept
		if concept.Required {
			required = append(required, concept.Key)
		}
	}
	if !reflect.DeepEqual(required, []string{"cerebras", "ontology"}) {
		t.Fatalf("required concepts = %#v", required)
	}
	for key, role := range map[string]string{
		"learn": conceptRoleIntent, "apply": conceptRoleIntent,
		"articles": conceptRoleFrame, "new": conceptRoleFrame, "system": conceptRoleFrame,
	} {
		concept, ok := byKey[key]
		if !ok || concept.Role != role || concept.Required {
			t.Fatalf("concept %q = %#v", key, concept)
		}
	}
	for _, key := range []string{"knowledge", "base"} {
		if byKey[key].Required {
			t.Fatalf("concept %q remained required", key)
		}
	}
}

func TestCerebrasPlannerMergePreservesRolesAndRequiredConceptsAtCap(t *testing.T) {
	terms := queryterms.Terms("What can we learn from the Cerebras articles about their new knowledge base system and ontology, and apply to dbrain?")
	base := buildQueryConceptsWithAnchors(terms, []ProtectedAnchor{
		anchorFromHandle("@CerebrasAI", "current_user_text"),
	})

	rolePlannerConcepts := sanitizeModelConcepts([]QueryConcept{
		{Key: "cerebras", Preferred: "cerebras", Terms: []string{"cerebras"}, Required: true, Role: conceptRoleContent},
		{Key: "ontology", Preferred: "ontology", Terms: []string{"ontology"}, Required: true, Role: conceptRoleContent},
		{Key: "learn", Preferred: "learn", Terms: []string{"learn"}, Required: true, Role: conceptRoleContent},
		{Key: "apply", Preferred: "apply", Terms: []string{"apply"}, Required: true, Role: conceptRoleContent},
		{Key: "articles", Preferred: "articles", Terms: []string{"articles"}, Required: true, Role: conceptRoleContent},
		{Key: "new", Preferred: "new", Terms: []string{"new"}, Required: true, Role: conceptRoleContent},
		{Key: "system", Preferred: "system", Terms: []string{"system"}, Required: true, Role: conceptRoleContent},
	})
	roleByKey := map[string]QueryConcept{}
	for _, concept := range rolePlannerConcepts {
		roleByKey[concept.Key] = concept
	}
	for key, role := range map[string]string{
		"learn": conceptRoleIntent, "apply": conceptRoleIntent,
		"articles": conceptRoleFrame, "new": conceptRoleFrame, "system": conceptRoleFrame,
	} {
		concept := roleByKey[key]
		if concept.Role != role || concept.Required {
			t.Fatalf("sanitized planner concept %q = %#v", key, concept)
		}
	}

	componentPlannerConcepts := sanitizeModelConcepts([]QueryConcept{
		{Key: "cerebras", Preferred: "cerebras", Terms: []string{"cerebras"}, Required: true, Role: conceptRoleContent},
		{Key: "ontology", Preferred: "ontology", Terms: []string{"ontology"}, Required: true, Role: conceptRoleContent},
		{Key: "knowledge", Preferred: "knowledge", Terms: []string{"knowledge"}, Required: true, Role: conceptRoleContent},
		{Key: "base", Preferred: "base", Terms: []string{"base"}, Required: true, Role: conceptRoleContent},
	})
	merged := mergeQueryConcepts(base, append(rolePlannerConcepts, componentPlannerConcepts...))
	if len(merged) != maxPlannerConcepts {
		t.Fatalf("merged concept count = %d, want cap %d: %#v", len(merged), maxPlannerConcepts, merged)
	}
	var required []string
	mergedByKey := map[string]QueryConcept{}
	for _, concept := range merged {
		mergedByKey[concept.Key] = concept
		if concept.Required {
			required = append(required, concept.Key)
		}
	}
	if !reflect.DeepEqual(required, []string{"cerebrasai", "cerebras", "ontology"}) {
		t.Fatalf("required concepts after planner merge = %#v; merged=%#v", required, merged)
	}
	for _, key := range []string{"learn", "apply", "articles", "new", "knowledge", "base", "system"} {
		if concept, ok := mergedByKey[key]; ok && concept.Required {
			t.Fatalf("generic planner concept %q was re-required: %#v", key, concept)
		}
	}
	if merged[0].Role != conceptRoleAnchor || merged[1].Key != "cerebras" || merged[2].Key != "ontology" {
		t.Fatalf("semantic merge priority lost anchor or required content: %#v", merged)
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
	if strategy.Family != queryFamilyModelToolSelection {
		t.Fatalf("expected model/tool family, got %#v", strategy)
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

func TestDeterministicStrategyNamesMaintainedQueryFamilies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		question      string
		family        string
		wantVariant   string
		wantConcept   string
		forbidVariant string
	}{
		{
			name:        "entity topic overview",
			question:    "What do I know about Mark Carney?",
			family:      queryFamilyEntityTopicOverview,
			wantVariant: "mark carney",
			wantConcept: "mark",
		},
		{
			name:        "person news event lookup",
			question:    "Can you find the Calgary father that killed two kids?",
			family:      queryFamilyPeopleEventLookup,
			wantVariant: "calgary father charged killing children",
			wantConcept: "children",
		},
		{
			name:        "model tool selection",
			question:    "What models should I use with Hermes agent?",
			family:      queryFamilyModelToolSelection,
			wantVariant: "llm model stack hermes agent",
			wantConcept: "model",
		},
		{
			name:        "software project lookup",
			question:    "Chrome DevTools MCP browser automation project",
			family:      queryFamilySoftwareProject,
			wantVariant: "github chrome devtools mcp browser automation",
			wantConcept: "project",
		},
		{
			name:        "comparison",
			question:    "K8s Helm alternatives",
			family:      queryFamilyComparison,
			wantVariant: "kubernetes helm alternatives",
			wantConcept: "alternative",
		},
		{
			name:        "timeline history",
			question:    "history of Litestream SQLite backups",
			family:      queryFamilyTimeline,
			wantVariant: "litestream sqlite backups timeline",
			wantConcept: "timeline",
		},
		{
			name:        "media transcript OCR lookup",
			question:    "video transcript red balloon promise",
			family:      queryFamilyMediaEvidence,
			wantVariant: "transcript red balloon promise",
			wantConcept: "transcript",
		},
		{
			name:          "corrective followup",
			question:      "Current question: not Marmot, what about Litestream instead?\nPrior evidence titles for query focus:\n- Marmot V2 | web",
			family:        queryFamilyCorrectiveFollowup,
			wantVariant:   "litestream",
			wantConcept:   "litestream",
			forbidVariant: "marmot",
		},
		{
			name:        "exact title source lookup",
			question:    "Open exact source src:phase3-exact-source",
			family:      queryFamilyExactLookup,
			wantVariant: "src:phase3-exact-source",
			wantConcept: "exact",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			searchQuestion := ask.SearchText(tt.question)
			hints := ask.Hints(searchQuestion)
			strategy := buildResearchStrategy(searchQuestion, hints)
			if strategy.Family != tt.family {
				t.Fatalf("family=%q, want %q; strategy=%#v", strategy.Family, tt.family, strategy)
			}
			if tt.wantVariant != "" && !hasQueryVariant(strategy.Variants, tt.wantVariant) {
				t.Fatalf("expected variant %q, got %#v", tt.wantVariant, strategy.Variants)
			}
			if tt.wantConcept != "" && !hasConceptKey(strategy.Concepts, tt.wantConcept) {
				t.Fatalf("expected concept %q, got %#v", tt.wantConcept, strategy.Concepts)
			}
			if tt.forbidVariant != "" && hasQueryVariantContaining(strategy.Variants, tt.forbidVariant) {
				t.Fatalf("did not expect variant term %q in %#v", tt.forbidVariant, strategy.Variants)
			}
		})
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
	observer := &testResearchObserver{}
	strategy := b.buildResearchStrategy(context.Background(), "K8s Helm alternatives", hints, Options{
		PlannerModel:   "cli/test/planner",
		PlannerTimeout: 5 * time.Second,
		PlannerBinary:  fakeSummarize,
		Observer:       observer,
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
	if !strings.Contains(observer.plannerInput, "K8s Helm alternatives") {
		t.Fatalf("expected planner input to be observed, got %q", observer.plannerInput)
	}
	if !strings.Contains(observer.plannerOutput, "cli/test/planner") || !strings.Contains(observer.plannerOutput, "helm tanka kustomize") {
		t.Fatalf("expected raw planner output to be observed, got %q", observer.plannerOutput)
	}
}

func TestBuildResearchStrategyPlannerCanUseOMLXThroughSummarizeCLI(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	var hit bool
	var capturedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		capturedModel = payload.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"concepts\":[{\"key\":\"local_models\",\"preferred\":\"local models\",\"terms\":[\"local models\"],\"required\":true}],\"query_variants\":[{\"query\":\"local models\",\"reason\":\"direct question term\"}]}"}}]}`))
	}))
	defer server.Close()

	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
omlx:
  base_url: `+server.URL+`/v1
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	b := New(cfg, nil)
	hints := ask.Hints("local models")
	strategy := b.buildResearchStrategy(context.Background(), "local models", hints, Options{
		PlannerModel:    "omlx/qwen3.5-coder",
		UseModelPlanner: true,
		PlannerTimeout:  2 * time.Second,
	})

	if !hit {
		t.Fatal("expected oMLX planner endpoint to be called")
	}
	if capturedModel != "qwen3.5-coder" {
		t.Fatalf("captured model = %q", capturedModel)
	}
	if strategy.Planner != "model_assisted" || strategy.PlannerModel != "omlx/qwen3.5-coder" || strategy.PlannerError != "" {
		t.Fatalf("unexpected planner metadata: %#v", strategy)
	}
	if !hasQueryVariant(strategy.Variants, "local models") {
		t.Fatalf("expected model planner variant, got %#v", strategy.Variants)
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

func TestBuildQueryConceptsPreservesDiscriminativeShortTokenPhrase(t *testing.T) {
	t.Parallel()

	concepts := buildQueryConcepts([]string{"anthropic", "j", "space"})
	jSpace := conceptByKey(concepts, "j_space")
	if jSpace == nil || !jSpace.Required || jSpace.Preferred != "j space" {
		t.Fatalf("expected required j-space phrase concept, got %#v in %#v", jSpace, concepts)
	}
	for _, term := range []string{"j space", "j-space", "jspace"} {
		if !hasConceptTerm(concepts, "j_space", term) {
			t.Fatalf("expected j_space alias %q, got %#v", term, concepts)
		}
	}

	merged := mergeQueryConcepts(concepts, []QueryConcept{{
		Key: "j_space", Preferred: "J-Space", Terms: []string{"J Space", "JSpace", "Joint Space"}, Required: true,
	}})
	jSpace = conceptByKey(merged, "j_space")
	if jSpace == nil || !jSpace.Required {
		t.Fatalf("planner merge weakened deterministic short phrase: %#v", merged)
	}
	variants := buildQueryVariants("anthropic j space", "anthropic j space", concepts, queryFamilyEntityTopicOverview)
	if !hasQueryVariant(variants, `"j-space"`) {
		t.Fatalf("expected exact short-token phrase retrieval variant, got %#v", variants)
	}
	mergedVariants := mergeQueryVariants(variants, []QueryVariant{{Query: "Anthropic joint space", Reason: "planner alias"}})
	if !hasQueryVariant(mergedVariants, `"j-space"`) {
		t.Fatalf("model planner merge dropped trusted exact phrase variant: %#v", mergedVariants)
	}
}

func TestBuildResearchStrategyRolesIntentAndFrameTermsForAnchoredSynthesis(t *testing.T) {
	t.Parallel()

	question := "Can you synthesize the Tweets from @Kristof_Poland - they're in the dbrain."
	hints := ask.Hints(question)
	concepts := buildQueryConceptsWithAnchors(hints.Terms, []ProtectedAnchor{anchorFromHandle("@Kristof_Poland", "current_user_text")})

	anchor := conceptByKey(concepts, "kristof_poland")
	if anchor == nil || anchor.Role != "anchor" || !anchor.Required {
		t.Fatalf("expected required kristof_poland anchor concept, got concepts=%#v anchor=%#v", concepts, anchor)
	}
	intent := conceptByKey(concepts, "synthesize")
	if intent == nil || intent.Role != "intent" || intent.Required {
		t.Fatalf("expected synthesize to be optional intent when anchor exists, got concepts=%#v intent=%#v", concepts, intent)
	}
	for _, frameKey := range []string{"they", "re"} {
		if frame := conceptByKey(concepts, frameKey); frame == nil || frame.Role != "frame" || frame.Required {
			t.Fatalf("expected %q to be retained as an optional frame, got %#v in %#v", frameKey, frame, concepts)
		}
	}
}

func TestBuildResearchStrategyRetainsFrameTermsAsOptionalConcepts(t *testing.T) {
	t.Parallel()

	concepts := buildQueryConceptsWithAnchors([]string{"what", "dbrain", "research", "notes"}, nil)
	for _, key := range []string{"what", "dbrain", "research", "notes"} {
		concept := conceptByKey(concepts, key)
		if concept == nil {
			t.Fatalf("expected frame concept %q to be retained as optional metadata, got %#v", key, concepts)
		}
		if concept.Role != "frame" || concept.Required {
			t.Fatalf("expected frame concept %q to be optional, got %#v in %#v", key, concept, concepts)
		}
	}
}

func TestBuildResearchStrategyAllowsIntentTermsWhenNoStrongerTopicExists(t *testing.T) {
	t.Parallel()

	concepts := buildQueryConceptsWithAnchors(ask.Hints("Find notes about synthesis essays").Terms, nil)
	synthesis := conceptByKey(concepts, "synthesis")
	essays := conceptByKey(concepts, "essays")
	if synthesis == nil {
		t.Fatalf("expected synthesis to remain searchable without stronger anchor/content, got %#v", concepts)
	}
	if essays == nil || !essays.Required {
		t.Fatalf("expected essays content concept to remain required, got concepts=%#v essays=%#v", concepts, essays)
	}
	if !synthesis.Required && essays == nil {
		t.Fatalf("expected query to retain at least one required concept, got %#v", concepts)
	}
}

func TestBuildPopulatesProtectedAnchorsInQueryPlan(t *testing.T) {
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

	pack, err := Build(context.Background(), cfg, st, Options{
		Question:       "Can you synthesize the Tweets from Kristof_Poland?",
		RawQuestion:    "Can you synthesize the Tweets from @Kristof_Poland - they're in the dbrain.",
		Limit:          4,
		DisablePlanner: true,
		AnchorResolver: fakeAnchorResolver{},
	})
	if err != nil {
		t.Fatalf("build pack: %v", err)
	}
	if len(pack.QueryPlan.ProtectedAnchors) != 1 {
		t.Fatalf("expected one protected anchor in query plan, got %#v", pack.QueryPlan.ProtectedAnchors)
	}
	anchor := pack.QueryPlan.ProtectedAnchors[0]
	if anchor.Canonical != "kristof_poland" || anchor.Kind != "handle" || anchor.Relation != "authored_by" {
		t.Fatalf("unexpected protected anchor: %#v", anchor)
	}
	concept := conceptByKey(pack.QueryPlan.Concepts, "kristof_poland")
	if concept == nil || concept.Role != "anchor" || !concept.Required {
		t.Fatalf("expected required anchor concept from protected anchor, got concepts=%#v concept=%#v", pack.QueryPlan.Concepts, concept)
	}
}

func TestMergeQueryConceptsCannotReRequireIntentAfterPlannerMerge(t *testing.T) {
	t.Parallel()

	base := buildQueryConceptsWithAnchors([]string{"synthesize", "tweets"}, []ProtectedAnchor{
		anchorFromHandle("@Kristof_Poland", "current_user_text"),
	})
	merged := applyConceptRolePolicy(mergeQueryConcepts(base, []QueryConcept{
		{Key: "synthesize", Preferred: "synthesize", Terms: []string{"synthesize", "synthesis"}, Required: true, Role: "content"},
		{Key: "summary", Preferred: "summary", Terms: []string{"summary"}, Required: true, Role: "content"},
	}))

	for _, key := range []string{"synthesize", "summary"} {
		concept := conceptByKey(merged, key)
		if concept == nil {
			t.Fatalf("expected planner concept %q to remain searchable, got %#v", key, merged)
		}
		if concept.Role != "intent" || concept.Required {
			t.Fatalf("planner must not re-upgrade %q into a hard content constraint, got %#v in %#v", key, concept, merged)
		}
	}
}

func TestSanitizeMergedConceptPreservesIncomingIntentRole(t *testing.T) {
	t.Parallel()

	concept := sanitizeMergedConcept(QueryConcept{
		Key:       "answer_shape",
		Preferred: "answer shape",
		Terms:     []string{"answer shape"},
		Required:  true,
		Role:      conceptRoleIntent,
	})
	if concept.Role != conceptRoleIntent {
		t.Fatalf("expected incoming intent role to survive sanitize, got %#v", concept)
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

func TestBuildReturnsRawChunkWindowForLongSource(t *testing.T) {
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
		SourceKey:     "src:long-raw-window",
		OriginalURL:   "https://example.com/long-raw-window",
		CanonicalURL:  "https://example.com/long-raw-window",
		NormalizedURL: "https://example.com/long-raw-window",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/long-raw-window.md",
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	raw := strings.Repeat("navigation newsletter unrelated text ", 120) + "The rare quasar passage says Mark Carney discussed GFANZ policy from a primary extract."
	if _, err := st.SaveSourceExtraction(ctx, source.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/long-raw-window",
		FinalURL:     "https://example.com/long-raw-window",
		Title:        "Long raw source",
		Content:      raw,
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "test-extract",
		ToolVersion:  "test",
	}, "long-raw-window-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(ctx, source.SourceID, model.SummaryResult{
		Text:          "A broad derived summary that omits the rare term.",
		Status:        "ok",
		Model:         "test-summary-model",
		PromptVersion: "test",
		Tool:          "test-summary",
		ToolVersion:   "test",
		FetchedAt:     now,
	}); err != nil {
		t.Fatalf("save source summary: %v", err)
	}

	includeTopic := false
	pack, err := Build(ctx, cfg, st, Options{
		Question:       "quasar Mark Carney GFANZ",
		Limit:          3,
		MaxCharsPerDoc: 220,
		IncludeTopic:   &includeTopic,
		DisablePlanner: true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(pack.Evidence) == 0 {
		t.Fatal("expected evidence")
	}
	got := pack.Evidence[0]
	if got.SourceKey != "src:long-raw-window" || got.EvidenceRole != "raw_extract_window" || got.Chunk == nil {
		t.Fatalf("expected raw chunk evidence, got %+v", got)
	}
	if !strings.Contains(got.Excerpt, "quasar") || !strings.Contains(got.Excerpt, "GFANZ") {
		t.Fatalf("expected raw chunk excerpt with query terms, got %q", got.Excerpt)
	}
	if !hasContentSection(got.ContentSections, "summary_text", "derived") || !hasContentSection(got.ContentSections, "extracted_text_window", "raw") {
		t.Fatalf("expected role-labelled content sections, got %+v", got.ContentSections)
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

func hasContentSection(sections []retrieval.ContentSection, name string, role string) bool {
	for _, section := range sections {
		if section.Name == name && section.Role == role && strings.TrimSpace(section.Text) != "" {
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

func conceptByKey(concepts []QueryConcept, key string) *QueryConcept {
	for i := range concepts {
		if concepts[i].Key == key {
			return &concepts[i]
		}
	}
	return nil
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

func hasRetrievalLane(lanes []ask.RetrievalLane, name string) bool {
	for _, lane := range lanes {
		if lane.Name == name {
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

type testResearchObserver struct {
	plannerInput  string
	plannerOutput string
	events        []string
}

func (o *testResearchObserver) Event(name string, _ map[string]interface{}) {
	o.events = append(o.events, name)
}

func (o *testResearchObserver) PlannerInput(input string) {
	o.plannerInput = input
}

func (o *testResearchObserver) PlannerOutput(output string) {
	o.plannerOutput = output
}
