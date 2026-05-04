package brainresearch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
)

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
	if len(prepared.Citations) == 0 || prepared.Citations[0].SourceKey != "src:one" {
		t.Fatalf("expected citations from included evidence, got %+v", prepared.Citations)
	}
}

func TestSynthesisPromptFramesSelectiveCorpusAndAccuracy(t *testing.T) {
	if SynthesisPromptVersion != "brain-research-synthesis-v2" {
		t.Fatalf("unexpected synthesis prompt version: %q", SynthesisPromptVersion)
	}
	for _, want := range []string{
		"intentionally selective",
		"Do not criticize the corpus for not being unbiased",
		"Accuracy matters more than appearing objective",
		"separate supported facts, source claims, opinions, and uncertainty",
	} {
		if !strings.Contains(synthesisPrompt, want) {
			t.Fatalf("synthesis prompt missing %q:\n%s", want, synthesisPrompt)
		}
	}
}

func TestSynthesizeRunsConfiguredSummaryPath(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	installResearchFakeSummarize(t, root)
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
		},
	}

	result, err := Synthesize(context.Background(), cfg, SynthesisOptions{
		Pack:             pack,
		Model:            "cli/test/research",
		CLI:              "codex",
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
	if result.PromptVersion != SynthesisPromptVersion || result.ToolVersion != "test-1.0.0" {
		t.Fatalf("unexpected provenance: %+v", result)
	}
}

func installResearchFakeSummarize(t *testing.T, root string) {
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

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
