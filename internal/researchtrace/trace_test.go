package researchtrace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/config"
)

func TestWritePersistsMarkdownJSONArtifactsAndRedactsPrivateOperationalData(t *testing.T) {
	cfg := testConfig(t)
	started := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	trace := ResearchTrace{
		SchemaVersion: SchemaVersion,
		RunID:         "trace-test",
		Surface:       "cli",
		Question:      "Where is agent memory?",
		StartedAt:     started,
		CompletedAt:   started.Add(2 * time.Second),
		StopReason:    "enough_evidence",
		Events: []ResearchTraceEvent{{
			At:   started.Add(time.Second),
			Name: "variant_retrieved",
			Data: map[string]interface{}{
				"candidate_count": 1,
				"source_keys":     []string{"src:test"},
			},
		}},
		Pack: &brainresearch.Pack{
			SchemaVersion: brainresearch.SchemaVersion,
			Question:      "Where is agent memory?",
			Mode:          "evidence_only",
			QueryPlan: brainresearch.QueryPlan{
				TextQuery:      "agent memory",
				QueryVariants:  []brainresearch.QueryVariant{{Query: "agent memory", Reason: "terms"}},
				Planner:        "model_assisted",
				PlannerModel:   "cli/test/planner",
				Limit:          1,
				MaxCharsPerDoc: 700,
			},
			Coverage: brainresearch.Coverage{EvidenceCount: 1, RecallNote: "one evidence row"},
			Evidence: []ask.Evidence{{
				SourceKey: "src:test",
				Title:     "Agent memory source",
				URL:       "https://example.com/source/path",
				NotePath:  "sources/web/test.md",
				Summary:   `A "red-team/blue-team" phrase is not an absolute path and must not corrupt trace JSON.`,
			}},
		},
		Synthesis: &brainresearch.SynthesisResult{
			SchemaVersion: brainresearch.SynthesisSchemaVersion,
			Question:      "Where is agent memory?",
			Answer:        "Agent memory needs durable traces [src:test].",
			AnswerStatus:  "ok",
			Model:         "ollama/test",
			Citations:     []brainresearch.Citation{{SourceKey: "src:test", Title: "Agent memory source"}},
		},
	}
	artifacts := ArtifactContents{
		PlannerInput:   "Authorization: Bearer abcdefghijklmnop\n" + filepath.Join(cfg.TempDir, "planner.md") + "\nhttps://example.com/source/path\n",
		PlannerOutput:  `{"summary":"ok","token":"dbrain_mcp_abcdefghijklmnopqrstuvwxyz"}`,
		SynthesisInput: "DBRAIN_OPENROUTER_API_KEY=or-abcdefghijklmnop\n/private/tmp/outside-secret.md\n",
	}

	result, err := Write(cfg, trace, artifacts, WriteOptions{Retention: RetentionOptions{KeepAll: true}})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.RelativePath != "research-runs/trace-test" {
		t.Fatalf("unexpected relative path %q", result.RelativePath)
	}

	for _, name := range []string{"run.md", "run.json", "planner-input.md", "planner-output.json", "synthesis-input.md", CompleteMarker} {
		if _, err := os.Stat(filepath.Join(result.Directory, name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}

	jsonBytes, err := os.ReadFile(filepath.Join(result.Directory, "run.json"))
	if err != nil {
		t.Fatalf("read run.json: %v", err)
	}
	var saved ResearchTrace
	if err := json.Unmarshal(jsonBytes, &saved); err != nil {
		t.Fatalf("unmarshal run.json: %v\n%s", err, string(jsonBytes))
	}
	if saved.Artifacts.PlannerInputPath != "planner-input.md" || saved.Artifacts.PlannerOutputPath != "planner-output.json" || saved.Artifacts.SynthesisInputPath != "synthesis-input.md" {
		t.Fatalf("unexpected artifact paths: %#v", saved.Artifacts)
	}
	if saved.Metrics.QueryVariantCount != 1 || saved.Metrics.CandidateCountBeforeDedupe != 1 || saved.Metrics.EvidenceCountAfterDedupe != 1 {
		t.Fatalf("unexpected metrics: %#v", saved.Metrics)
	}

	all := readAllTraceFiles(t, result.Directory)
	for _, forbidden := range []string{"abcdefghijklmnop", "dbrain_mcp_", cfg.TempDir, "/private/tmp/outside-secret.md"} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("trace output leaked %q:\n%s", forbidden, all)
		}
	}
	if !strings.Contains(all, "Bearer [REDACTED]") || !strings.Contains(all, "DBRAIN_OPENROUTER_API_KEY=[REDACTED]") {
		t.Fatalf("trace output did not include expected redaction markers:\n%s", all)
	}
	if !strings.Contains(all, "https://example.com/source/path") {
		t.Fatalf("trace redaction should preserve URLs:\n%s", all)
	}
}

func TestConcurrentWritesCreateDistinctCompleteRunDirectories(t *testing.T) {
	cfg := testConfig(t)
	const count = 12
	results := make(chan WriteResult, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recorder := NewRecorder("cli", "concurrent trace")
			recorder.Event("question_normalized", map[string]interface{}{"candidate_count": 0})
			trace, artifacts := recorder.Snapshot()
			result, err := Write(cfg, trace, artifacts, WriteOptions{Retention: RetentionOptions{KeepAll: true}})
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("Write: %v", err)
	}
	seen := map[string]struct{}{}
	for result := range results {
		if _, exists := seen[result.RunID]; exists {
			t.Fatalf("duplicate run id %s", result.RunID)
		}
		seen[result.RunID] = struct{}{}
		if _, err := os.Stat(filepath.Join(result.Directory, CompleteMarker)); err != nil {
			t.Fatalf("missing completion marker for %s: %v", result.RunID, err)
		}
	}
	if len(seen) != count {
		t.Fatalf("expected %d traces, got %d", count, len(seen))
	}
}

func TestPruneKeepsNewestAndActiveCompletedRunsOnly(t *testing.T) {
	cfg := testConfig(t)
	root := filepath.Join(cfg.DataDir, "research-runs")
	now := time.Now()
	createTraceDir(t, root, "newest", now)
	createTraceDir(t, root, "active-old", now.Add(-240*time.Hour))
	createTraceDir(t, root, "old-delete-1", now.Add(-240*time.Hour))
	createTraceDir(t, root, "old-delete-2", now.Add(-220*time.Hour))
	incomplete := filepath.Join(root, "incomplete-old")
	if err := os.MkdirAll(incomplete, 0o700); err != nil {
		t.Fatalf("create incomplete: %v", err)
	}

	result, err := Prune(cfg, RetentionOptions{KeepLatest: 1, MaxAge: time.Hour}, "active-old")
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if result.Deleted != 2 {
		t.Fatalf("expected two deleted trace dirs, got %#v", result)
	}
	for _, name := range []string{"newest", "active-old", "incomplete-old"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("expected %s to remain: %v", name, err)
		}
	}
	for _, name := range []string{"old-delete-1", "old-delete-2"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be pruned, err=%v", name, err)
		}
	}
}

func TestListAndLoadRejectPathTraversal(t *testing.T) {
	cfg := testConfig(t)
	var firstResult WriteResult
	for _, tc := range []struct {
		runID       string
		question    string
		completedAt time.Time
	}{
		{
			runID:       "older-trace",
			question:    "trace list question",
			completedAt: time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC),
		},
		{
			runID:       "newer-trace",
			question:    "newer trace list question",
			completedAt: time.Date(2026, 5, 23, 12, 1, 0, 0, time.UTC),
		},
	} {
		recorder := NewRecorder("web_chat", tc.question)
		recorder.trace.RunID = tc.runID
		recorder.trace.CompletedAt = tc.completedAt
		recorder.SetStopReason("no_evidence")
		result, err := Write(cfg, recorder.trace, ArtifactContents{}, WriteOptions{Retention: RetentionOptions{KeepAll: true}})
		if err != nil {
			t.Fatalf("Write %s: %v", tc.runID, err)
		}
		if tc.runID == "older-trace" {
			firstResult = result
		}
	}

	traces, err := List(cfg, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(traces) != 2 {
		t.Fatalf("expected two trace summaries, got %#v", traces)
	}
	if traces[0].RelativePath != "research-runs/newer-trace" || traces[0].Question != "newer trace list question" {
		t.Fatalf("newest trace should sort first: %#v", traces)
	}
	if traces[1].RelativePath != firstResult.RelativePath || traces[1].Question != "trace list question" {
		t.Fatalf("unexpected trace summaries: %#v", traces)
	}

	trace, resolved, err := Load(cfg, firstResult.RelativePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if trace.Question != "trace list question" || !strings.HasSuffix(resolved, "run.json") {
		t.Fatalf("unexpected loaded trace question=%q resolved=%q", trace.Question, resolved)
	}
	if _, _, err := Load(cfg, "../outside.json"); err == nil {
		t.Fatalf("expected path traversal to be rejected")
	}
}

func TestLoadRepairsLegacyRedactedJSONAndListIncludesUnreadableRuns(t *testing.T) {
	cfg := testConfig(t)
	root := filepath.Join(cfg.DataDir, "research-runs")
	completedAt := time.Date(2026, 5, 23, 12, 2, 0, 0, time.UTC)
	legacyDir := filepath.Join(root, "legacy-redacted")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("create legacy trace dir: %v", err)
	}
	legacyJSON := `{
  "schema_version": "research_trace.v1",
  "run_id": "legacy-redacted",
  "surface": "web_chat",
  "question": "legacy repaired trace",
  "started_at": "2026-05-23T12:01:59Z",
  "completed_at": "2026-05-23T12:02:00Z",
  "pack": {
    "schema_version": "brain_research_pack.v1",
    "question": "legacy repaired trace",
    "evidence": [
      {"source_key": "src:test", "summary": "A \"red-team[redacted-path]" phrase was corrupted by old redaction."}
    ]
  },
  "stop_reason": "enough_evidence"
}`
	if err := os.WriteFile(filepath.Join(legacyDir, "run.json"), []byte(legacyJSON), 0o600); err != nil {
		t.Fatalf("write legacy run json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, CompleteMarker), []byte(completedAt.Format(time.RFC3339Nano)+"\n"), 0o600); err != nil {
		t.Fatalf("write legacy complete marker: %v", err)
	}

	trace, _, err := Load(cfg, "research-runs/legacy-redacted")
	if err != nil {
		t.Fatalf("Load should repair legacy malformed JSON: %v", err)
	}
	if trace.Question != "legacy repaired trace" || trace.Pack == nil || len(trace.Pack.Evidence) != 1 {
		t.Fatalf("unexpected repaired trace: %#v", trace)
	}

	brokenDir := filepath.Join(root, "unreadable-trace")
	if err := os.MkdirAll(brokenDir, 0o700); err != nil {
		t.Fatalf("create unreadable trace dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(brokenDir, "run.json"), []byte(`{"schema_version":`), 0o600); err != nil {
		t.Fatalf("write unreadable run json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(brokenDir, CompleteMarker), []byte(completedAt.Add(time.Minute).Format(time.RFC3339Nano)+"\n"), 0o600); err != nil {
		t.Fatalf("write unreadable complete marker: %v", err)
	}

	traces, err := List(cfg, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(traces) != 2 {
		t.Fatalf("expected repaired and unreadable traces to list, got %#v", traces)
	}
	if traces[0].RunID != "unreadable-trace" || traces[0].LoadError == "" || traces[0].StopReason != "trace_unreadable" {
		t.Fatalf("expected unreadable trace summary first, got %#v", traces)
	}
	if traces[1].RunID != "legacy-redacted" || traces[1].LoadError != "" {
		t.Fatalf("expected repaired trace summary, got %#v", traces)
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	return cfg
}

func readAllTraceFiles(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var b strings.Builder
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", entry.Name(), err)
		}
		b.Write(content)
		b.WriteString("\n")
	}
	return b.String()
}

func createTraceDir(t *testing.T, root string, name string, modTime time.Time) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, CompleteMarker), []byte("complete\n"), 0o600); err != nil {
		t.Fatalf("WriteFile %s marker: %v", name, err)
	}
	if err := os.Chtimes(dir, modTime, modTime); err != nil {
		t.Fatalf("Chtimes %s: %v", name, err)
	}
}
