package ask

import (
	"reflect"
	"testing"

	"dbrain/internal/entities"
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
