package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/researcheval"
	"github.com/darron/dbrain/internal/store"
)

func TestResearchEvalCommandRunsAndPrintsPlannerMetadata(t *testing.T) {
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
	seedResearchEvalCommandItem(t, st)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	casesPath := filepath.Join(root, "research-eval.json")
	cases := []researcheval.Case{{
		Name:                       "alpha command",
		Question:                   "Alpha Eval Command",
		Limit:                      5,
		DisablePlanner:             true,
		DisableSemantic:            true,
		ExpectPlanner:              "deterministic",
		ExpectSemanticStatus:       "disabled",
		MinEvidence:                1,
		ExpectAnySourceKeys:        []string{"x:eval-command-alpha"},
		ExpectCitationSourceKeys:   []string{"x:eval-command-alpha"},
		ExpectAnswerStatus:         "ok",
		MinRetrievalSignals:        1,
		ExpectQueryTerms:           []string{"alpha", "eval", "command"},
		ForbidCitationSourceKeys:   []string{"x:missing"},
		ExpectPlannerErrorContains: "",
	}}
	data, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		t.Fatalf("marshal cases: %v", err)
	}
	if err := os.WriteFile(casesPath, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write cases: %v", err)
	}

	output := runRootCommand(t, root, "eval", "research", "--file", casesPath)
	for _, want := range []string{
		"Research eval: 1 passed, 0 failed",
		"planner=deterministic",
		"retrieval_lanes:",
		"query_terms:",
		"citation_source_keys (prompt_admitted):",
		"prompt_admitted_source_keys:",
		"top_retrieval_signals:",
		"x:eval-command-alpha",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestResearchEvalProposeTranscriptDryRunDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "transcript.md")
	outputPath := filepath.Join(root, "research-proposal.json")
	transcript := `# dbrain chat transcript

## Turn 1

Status: ` + "`ready`" + `

### Question

What does my brain know about Alpha Proposal?

### Answer

Alpha proposal answer should not be promoted by default [x:alpha-proposal].

### Citations

- ` + "`x:alpha-proposal`" + ` - Alpha Proposal

### Research Pack

Query plan:
- text: ` + "`alpha proposal`" + `
- terms: alpha, proposal
- planner: deterministic

### Evidence

- ` + "`x:alpha-proposal`" + ` - Alpha Proposal
`
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "--no-debug", "eval", "research", "propose", "--from-transcript", transcriptPath, "--output", outputPath})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run proposal should not write %s, err=%v", outputPath, err)
	}
	if !strings.Contains(stderr.String(), "Dry run only") {
		t.Fatalf("expected dry-run notice on stderr, got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), `"schema_version": "research_eval_proposal.v1"`) {
		t.Fatalf("expected proposal JSON, got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "expect_answer_text") {
		t.Fatalf("default transcript proposal should omit answer text assertions: %s", stdout.String())
	}

	cmd = NewRootCommand()
	stdout.Reset()
	stderr.Reset()
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "--no-debug", "eval", "research", "propose", "--from-transcript", transcriptPath, "--include-answer-text"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext include answer text: %v", err)
	}
	if !strings.Contains(stdout.String(), "expect_answer_text") {
		t.Fatalf("opt-in transcript proposal should include answer text assertions: %s", stdout.String())
	}
}

func seedResearchEvalCommandItem(t *testing.T, st *store.Store) {
	t.Helper()

	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	_, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:eval-command-alpha",
		SourceType:   "x_bookmark",
		ExternalID:   "eval-command-alpha",
		CanonicalURL: "https://x.example/eval-command-alpha",
		Title:        "Alpha Eval Command",
		Text:         "Alpha Eval Command local evidence for research eval command tests.",
		SummaryText:  "Alpha Eval Command local evidence for research eval command tests.",
		ContentHash:  "eval-command-alpha-hash",
		NotePath:     "items/x/eval-command-alpha.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
}
