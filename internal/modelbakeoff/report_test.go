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

func TestRenderMarkdownIncludesProviderParityAndRuntimeContext(t *testing.T) {
	local := true
	result := Result{
		SchemaVersion: SchemaVersion,
		Mode:          ModeSourceSummary,
		Models:        []string{"lmstudio/qwen/qwen3.6-35b-a3b"},
		Targets: []TargetRun{
			{
				Lookup:    "src:test",
				SourceKey: "src:test",
				Title:     "Test source",
				Runs: []ModelRun{
					{
						Model:               "lmstudio/qwen/qwen3.6-35b-a3b",
						Provider:            "lmstudio",
						APIModel:            "qwen/qwen3.6-35b-a3b",
						Transport:           "openai_chat_completions",
						Local:               &local,
						Status:              "ok",
						RequestedParams:     map[string]any{"temperature": 0.6, "min_p": 0.0},
						SentParams:          map[string]any{"temperature": 0.6},
						OmittedParams:       map[string]string{"min_p": "not documented"},
						ParamStrictness:     "non-strict",
						PromptParityStatus:  "unknown",
						ReasoningModeStatus: "ollama-think-disabled-lmstudio-unknown",
						RuntimeContext: RuntimeContext{
							Status: "not-collected",
						},
						Summary: &model.SummaryResult{Text: "### What It Is\n\nA concise summary."},
					},
				},
			},
		},
	}

	report := RenderMarkdown(result, 0)
	for _, expected := range []string{
		"Schema: `model_bakeoff.v2`",
		"Provider: `lmstudio`",
		"API model: `qwen/qwen3.6-35b-a3b`",
		"Transport: `openai_chat_completions`",
		"Local backend: `true`",
		"Param strictness: `non-strict`",
		"Prompt parity: `unknown`",
		"Reasoning mode: `ollama-think-disabled-lmstudio-unknown`",
		"Runtime context: `not-collected`",
		"`min_p`: not documented",
	} {
		if !strings.Contains(report, expected) {
			t.Fatalf("expected report to contain %q, got:\n%s", expected, report)
		}
	}
}
