package ask

import (
	"reflect"
	"strings"
	"testing"

	"dbrain/internal/config"
	"dbrain/internal/entities"
	"dbrain/internal/model"
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
