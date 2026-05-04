package ask

import (
	"reflect"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/queryterms"
)

func TestQueryTermsBuildTagAlias(t *testing.T) {
	t.Parallel()

	hints := Hints("What do we know about Mark Carney from dbrain?")
	terms := hints.Terms
	if !reflect.DeepEqual(terms, []string{"mark", "carney"}) {
		t.Fatalf("unexpected query terms: %#v", terms)
	}

	if hints.TextQuery != "mark carney" {
		t.Fatalf("unexpected text query: %q", hints.TextQuery)
	}

	tags := hints.TagQueries
	if !reflect.DeepEqual(tags, []string{"mark-carney"}) {
		t.Fatalf("unexpected tag queries: %#v", tags)
	}
}

func TestTagQueriesIncludeAdjacentPairs(t *testing.T) {
	t.Parallel()

	tags := queryterms.TagQueries([]string{"mark", "carney", "brookfield"})
	want := []string{"mark-carney-brookfield", "mark-carney", "carney-brookfield"}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("unexpected tag queries: %#v", tags)
	}
}

func TestChatRetrievalScaffoldingDoesNotPolluteQueryTerms(t *testing.T) {
	t.Parallel()

	hints := Hints(`Current question: other alternatives like Tanka?

Recent user questions:
- Kubernetes Helm alternatives

Relevant prior evidence source keys:
- src:56b20d0e9e0c

Prior evidence metadata for query expansion:
- Grafana Tanka | github | jsonnet, configuration-management, yaml-alternative`)

	for _, noisy := range []string{"current", "question", "recent", "relevant", "prior", "evidence", "metadata", "query", "keys"} {
		if containsString(hints.Terms, noisy) {
			t.Fatalf("did not expect scaffolding term %q in %#v", noisy, hints.Terms)
		}
	}
	for _, want := range []string{"tanka", "helm", "jsonnet", "configuration", "management", "yaml", "alternative"} {
		if !containsString(hints.Terms, want) {
			t.Fatalf("expected term %q in %#v", want, hints.Terms)
		}
	}
	if !containsString(hints.TagQueries, "configuration-management") || !containsString(hints.TagQueries, "yaml-alternative") {
		t.Fatalf("expected useful adjacent tag aliases, got %#v", hints.TagQueries)
	}

	hints = Hints(`Current question: Father charged with killing young son, daughter who were found in vehicle in Calgary\n\nRecent user questions:\n- Two young children found in an SUV in Calgary.\n\nRelevant prior evidence source keys:\n- x:1886891289838526774\n- src:c2c2fb606ce8`)
	for _, term := range hints.Terms {
		if strings.Contains(term, `\`) || strings.Contains(term, ":") {
			t.Fatalf("did not expect escaped separator or source-key punctuation in term %q from %#v", term, hints.Terms)
		}
	}
	for _, want := range []string{"father", "charge", "kill", "young", "son", "daughter", "found", "vehicle", "calgary"} {
		if !containsString(hints.Terms, want) {
			t.Fatalf("expected crime-story term %q in %#v", want, hints.Terms)
		}
	}
	for _, noisy := range []string{"calgary\\n\\nrecent", "questions\\n", "keys\\n", "src", "x", "with", "were", "1886891289838526774", "c2c2fb606ce8"} {
		if containsString(hints.Terms, noisy) {
			t.Fatalf("did not expect noisy term %q in %#v", noisy, hints.Terms)
		}
	}

	hints = Hints("Can you find the information about the Calgary man that killed his two kids?")
	wantTerms := []string{"calgary", "man", "kill", "two", "children"}
	if !reflect.DeepEqual(hints.Terms, wantTerms) {
		t.Fatalf("expected conversational crime query to normalize to %#v, got %#v", wantTerms, hints.Terms)
	}

	hints = Hints("Can you look in my brain and find the stories about the father who killed two children?")
	wantTerms = []string{"father", "kill", "two", "children"}
	if !reflect.DeepEqual(hints.Terms, wantTerms) {
		t.Fatalf("expected filler words to be stripped from story query, got %#v", hints.Terms)
	}

	hints = Hints("What models should I use with Hermes agent? Are there favored models in my research?")
	wantTerms = []string{"model", "hermes", "agent"}
	if !reflect.DeepEqual(hints.Terms, wantTerms) {
		t.Fatalf("expected corpus-framing model query to normalize to %#v, got %#v", wantTerms, hints.Terms)
	}
}

func TestBuildEntityMatchIndexRequiresMultiTermEntityMatch(t *testing.T) {
	t.Parallel()

	index := []entities.Entity{
		{
			Key:  "site:github.com/mark3labs/mcp-go",
			Name: "mark3labs/mcp-go",
			Kind: entities.KindProject,
			References: []entities.Reference{{
				SourceKey: "src:mark3labs",
				Title:     "MCP Go",
				NotePath:  "sources/github/mark3labs-mcp-go.md",
			}},
		},
		{
			Key:  "project:markdown-viewer",
			Name: "markdown-viewer",
			Kind: entities.KindProject,
			References: []entities.Reference{{
				SourceKey: "src:markdown-viewer",
				Title:     "Markdown Viewer",
				NotePath:  "sources/github/markdown-viewer.md",
			}},
		},
		{
			Key:     "person:mark-carney",
			Name:    "Mark Carney",
			Kind:    entities.KindPerson,
			Aliases: []string{"mark-carney"},
			References: []entities.Reference{{
				SourceKey: "x:carney",
				Title:     "Mark Carney evidence",
				NotePath:  "items/x/carney.md",
			}},
		},
	}

	matches := buildEntityMatchIndex(index, "What do we know about Mark Carney?", []string{"mark", "carney"}, 10)
	if _, ok := matches["x:carney"]; !ok {
		t.Fatalf("expected Mark Carney reference to match: %#v", matches)
	}
	if _, ok := matches["src:mark3labs"]; ok {
		t.Fatalf("did not expect standalone mark3labs match: %#v", matches)
	}
	if _, ok := matches["src:markdown-viewer"]; ok {
		t.Fatalf("did not expect standalone markdown match: %#v", matches)
	}
}

func TestEvidenceFromSourceUsesQueryWindowForExcerpt(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{
		SourceKey:     "src:carney",
		CanonicalURL:  "https://example.com/carney",
		Title:         "Long extracted source",
		SourceType:    "web",
		NotePath:      "sources/carney.md",
		ExtractedText: strings.Repeat("navigation menu footer cookie settings ", 12) + "Mark Carney appears in the substantive paragraph about saved evidence and policy.",
	}

	candidate := evidenceFromSource(config.Config{VaultDir: "/vault"}, source, model.SearchResult{}, 120, []string{"mark", "carney"})
	if !strings.Contains(candidate.Excerpt, "Mark Carney") {
		t.Fatalf("expected excerpt to include query match, got %q", candidate.Excerpt)
	}
	if strings.HasPrefix(candidate.Excerpt, "navigation menu footer") {
		t.Fatalf("expected excerpt to skip leading boilerplate, got %q", candidate.Excerpt)
	}
}

func TestEvidenceFromSourcePrefersRarerQueryTermForExcerpt(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{
		SourceKey:     "src:carney-gfanz",
		CanonicalURL:  "https://example.com/carney-gfanz",
		Title:         "Long extracted source",
		SourceType:    "web",
		NotePath:      "sources/carney-gfanz.md",
		ExtractedText: strings.Repeat("Mark Carney policy context ", 25) + strings.Repeat("navigation menu footer cookie settings ", 12) + "GFANZ appears in the substantive paragraph about bank climate commitments.",
	}

	candidate := evidenceFromSource(config.Config{VaultDir: "/vault"}, source, model.SearchResult{}, 120, []string{"mark", "carney", "gfanz"})
	if !strings.Contains(candidate.Excerpt, "GFANZ") {
		t.Fatalf("expected excerpt to include rarer query term, got %q", candidate.Excerpt)
	}
	if strings.HasPrefix(strings.TrimPrefix(candidate.Excerpt, "..."), "Mark Carney policy context Mark Carney") {
		t.Fatalf("expected excerpt to skip repeated broad terms, got %q", candidate.Excerpt)
	}
}

func TestEvidenceFromSourceUsesCompactSummaryBeforeRawExtract(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{
		SourceKey:     "src:calgary-crime",
		CanonicalURL:  "https://example.com/calgary-crime",
		Title:         "Calgary father charged",
		SourceType:    "web",
		NotePath:      "sources/calgary-crime.md",
		SummaryText:   "Calgary police charged a father after his two children were found dead in a vehicle.",
		ExtractedText: strings.Repeat("navigation related stories newsletter advertisement ", 2000) + "Calgary police charged a father after his two children were found dead in a vehicle.",
	}

	candidate := evidenceFromSource(config.Config{VaultDir: "/vault"}, source, model.SearchResult{}, 160, []string{"father", "kill", "children"})
	if !strings.Contains(candidate.Excerpt, "father") || !strings.Contains(candidate.Excerpt, "children") {
		t.Fatalf("expected compact summary excerpt to include query terms, got %q", candidate.Excerpt)
	}
	if strings.Contains(candidate.Excerpt, "navigation related stories") {
		t.Fatalf("expected excerpt to avoid raw extract boilerplate when summary matches, got %q", candidate.Excerpt)
	}
	if strings.Contains(candidate.MatchText, "navigation related stories") {
		t.Fatalf("expected match text to avoid full raw extract boilerplate")
	}
	if !strings.Contains(candidate.MatchText, source.SummaryText) {
		t.Fatalf("expected compact match text to include summary")
	}
}

func TestEvidenceFromSourceFallsBackToRawExtractWhenCompactTextIsAbsent(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{
		SourceKey:     "src:raw-only",
		CanonicalURL:  "https://example.com/raw-only",
		Title:         "Sparse source",
		SourceType:    "web",
		NotePath:      "sources/raw-only.md",
		ExtractedText: "Buried raw article text says Mark Carney discussed GFANZ policy.",
	}

	candidate := evidenceFromSource(config.Config{VaultDir: "/vault"}, source, model.SearchResult{}, 160, []string{"mark", "carney", "gfanz"})
	if !strings.Contains(candidate.Excerpt, "Mark Carney") || !strings.Contains(candidate.Excerpt, "GFANZ") {
		t.Fatalf("expected raw extract fallback when compact fields are absent, got %q", candidate.Excerpt)
	}
}

func TestEvidenceFromItemIncludesDerivedSummaryAndOCRExcerpt(t *testing.T) {
	t.Parallel()

	item := model.Item{
		SourceKey:    "x:photo",
		SourceType:   "x_bookmark",
		CanonicalURL: "https://x.com/example/status/photo",
		Title:        "Photo bookmark",
		NotePath:     "items/x/photo.md",
		XPostText:    "short post text without the visual keyword",
		SummaryText:  "derived media summary",
		OCRText:      "The image says Mark Carney in visible text.",
	}

	candidate := evidenceFromItem(config.Config{VaultDir: "/vault"}, item, model.SearchResult{}, 80, []string{"mark", "carney"})
	if candidate.Summary != "derived media summary" {
		t.Fatalf("expected item summary in evidence, got %q", candidate.Summary)
	}
	if !strings.Contains(candidate.Excerpt, "Mark Carney") {
		t.Fatalf("expected OCR query match in excerpt, got %q", candidate.Excerpt)
	}
}

func TestEvidenceFromItemChoosesStrongestQueryWindowAcrossFields(t *testing.T) {
	t.Parallel()

	item := model.Item{
		SourceKey:    "x:photo-gfanz",
		SourceType:   "x_bookmark",
		CanonicalURL: "https://x.com/example/status/photo-gfanz",
		Title:        "Carney cabinet thread",
		NotePath:     "items/x/photo-gfanz.md",
		XPostText:    "Here are posts from people in Mark Carney's cabinet including Carney himself.",
		OCRText:      strings.Repeat("policy screenshot text ", 8) + "Mark Carney referenced the Net Zero Banking Alliance and GFANZ.",
	}

	candidate := evidenceFromItem(config.Config{VaultDir: "/vault"}, item, model.SearchResult{}, 120, []string{"mark", "carney", "gfanz"})
	if !strings.Contains(candidate.Excerpt, "GFANZ") {
		t.Fatalf("expected excerpt to include strongest matching OCR evidence, got %q", candidate.Excerpt)
	}
}

func TestExplainEvidenceScoreReportsTermCoverageAndDemotesMissingFocusedTerms(t *testing.T) {
	t.Parallel()

	terms := []string{"mark", "carney", "gfanz"}
	broad := Evidence{
		Title:    "Introducing the Canada Strong Fund",
		Summary:  "Mark Carney announced a sovereign wealth fund.",
		UserTags: "mark-carney,canadian-politics",
	}
	focused := Evidence{
		Title:    "Carney climate organization GFANZ loses banks",
		Summary:  "Banks left the Glasgow Financial Alliance for Net Zero.",
		UserTags: "mark-carney,gfanz,climate-policy",
	}

	broadInfo := explainEvidenceScore("What about Mark Carney GFANZ?", terms, broad, strings.Join([]string{broad.Title, broad.Summary, broad.UserTags}, "\n"))
	focusedInfo := explainEvidenceScore("What about Mark Carney GFANZ?", terms, focused, strings.Join([]string{focused.Title, focused.Summary, focused.UserTags}, "\n"))

	if !reflect.DeepEqual(broadInfo.MatchedTerms, []string{"mark", "carney"}) {
		t.Fatalf("unexpected broad matched terms: %#v", broadInfo)
	}
	if !reflect.DeepEqual(broadInfo.MissingTerms, []string{"gfanz"}) {
		t.Fatalf("expected broad result to miss gfanz: %#v", broadInfo)
	}
	var foundPenalty bool
	for _, signal := range broadInfo.Signals {
		if signal.Name == "missing_query_terms" && signal.Detail == "gfanz" && signal.Weight < 0 {
			foundPenalty = true
			break
		}
	}
	if !foundPenalty {
		t.Fatalf("expected missing query term penalty, got %#v", broadInfo.Signals)
	}
	if focusedInfo.Score <= broadInfo.Score {
		t.Fatalf("expected focused result to outrank broad tag-only result: focused=%#v broad=%#v", focusedInfo, broadInfo)
	}
	if !reflect.DeepEqual(focusedInfo.MatchedTerms, terms) || len(focusedInfo.MissingTerms) != 0 {
		t.Fatalf("expected focused result to cover all terms, got %#v", focusedInfo)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
