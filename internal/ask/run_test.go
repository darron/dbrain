package ask

import (
	"reflect"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/model"
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

	tags := tagQueries([]string{"mark", "carney", "brookfield"})
	want := []string{"mark-carney-brookfield", "mark-carney", "carney-brookfield"}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("unexpected tag queries: %#v", tags)
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
