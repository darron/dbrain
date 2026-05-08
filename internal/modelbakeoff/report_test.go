package modelbakeoff

import (
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/itemcategorize"
	"github.com/darron/dbrain/internal/model"
)

func TestRenderMarkdownIncludesSummaryAndCategorizationRuns(t *testing.T) {
	result := Result{
		SchemaVersion: SchemaVersion,
		Mode:          ModeCategorizeItem,
		Models:        []string{"ollama/a", "ollama/b"},
		Targets: []TargetRun{
			{
				Lookup:    "x:test",
				SourceKey: "x:test",
				Title:     "Test item",
				Runs: []ModelRun{
					{
						Model:  "ollama/a",
						Status: "ok",
						Categorize: &itemcategorize.Result{
							PrimaryCategory: "software-development",
							Categories:      []string{"software-development"},
							Tags:            []string{"local-models", "testing"},
						},
					},
					{
						Model:  "ollama/b",
						Status: "error",
						Error:  "context deadline exceeded",
					},
				},
			},
			{
				Lookup:    "src:test",
				SourceKey: "src:test",
				Title:     "Test source",
				Runs: []ModelRun{
					{
						Model:  "ollama/a",
						Status: "ok",
						Summary: &model.SummaryResult{
							Text: "### What It Is\n\nA concise summary.",
						},
					},
				},
			},
		},
	}

	report := RenderMarkdown(result, 0)
	for _, expected := range []string{
		"# dbrain Model Bakeoff",
		"Primary category: `software-development`",
		"Tags: `local-models`, `testing`",
		"Error: `context deadline exceeded`",
		"### What It Is",
	} {
		if !strings.Contains(report, expected) {
			t.Fatalf("expected report to contain %q, got:\n%s", expected, report)
		}
	}
}
