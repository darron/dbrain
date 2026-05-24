package researchrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func TestJudgeVerdictsAndRetryActions(t *testing.T) {
	t.Parallel()

	noEvidence := Judge(brainresearch.Pack{}, JudgeOptions{AllowRetry: true})
	if noEvidence.Verdict != JudgeNoEvidence || noEvidence.RetryAction != RetryNone {
		t.Fatalf("unexpected no evidence judge: %+v", noEvidence)
	}

	missing := Judge(brainresearch.Pack{Evidence: []ask.Evidence{{
		SourceKey: "x:weak",
		Retrieval: &ask.RetrievalInfo{MissingTerms: []string{"alpha"}},
	}}}, JudgeOptions{AllowRetry: true})
	if missing.Verdict != JudgeWeakEvidence || missing.RetryAction != RetryFocusedVariant || missing.RetryVariant != "alpha" {
		t.Fatalf("unexpected missing-concept judge: %+v", missing)
	}

	related := Judge(brainresearch.Pack{Evidence: []ask.Evidence{{
		SourceKey: "x:one",
	}}}, JudgeOptions{AllowRetry: true, MinEvidenceForEnough: 2})
	if related.Verdict != JudgeWeakEvidence || related.RetryAction != RetryRelatedExpansion || related.ExpansionLookup != "x:one" {
		t.Fatalf("unexpected related-expansion judge: %+v", related)
	}

	enough := Judge(brainresearch.Pack{Evidence: []ask.Evidence{{SourceKey: "x:one"}}}, JudgeOptions{AllowRetry: true})
	if enough.Verdict != JudgeEnoughEvidence || enough.RetryAction != RetryNone {
		t.Fatalf("unexpected enough-evidence judge: %+v", enough)
	}
}

func TestRunNoEvidencePersistsTraceAndAttemptsSynthesis(t *testing.T) {
	t.Parallel()

	cfg, st := newRunnerStore(t)
	var events []ProgressEvent
	result, err := Run(context.Background(), cfg, st, Options{
		Question:       "no matching runner evidence",
		DisablePlanner: true,
		Model:          "ollama/unused",
		Progress: func(event ProgressEvent) {
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.StopReason != StopNoEvidence || result.Synthesis == nil || result.Synthesis.AnswerStatus != "no_evidence" {
		t.Fatalf("unexpected no-evidence result: %+v", result)
	}
	if !hasProgressStage(events, "prepare_synthesis") || !hasProgressStage(events, "synthesis") || !hasProgressStage(events, "verification") {
		t.Fatalf("expected synthesis and verification progress events: %+v", events)
	}
	assertRunnerTrace(t, cfg, result.TracePath)
}

func TestRunEnforcesMaxStepsAndRunnerTimeout(t *testing.T) {
	t.Parallel()

	cfg, st := newRunnerStore(t)
	result, err := Run(context.Background(), cfg, st, Options{
		Question:       "alpha",
		DisablePlanner: true,
		MaxSteps:       1,
	})
	if err != nil {
		t.Fatalf("Run max steps: %v", err)
	}
	if result.StopReason != StopMaxStepsReached {
		t.Fatalf("stop_reason=%q, want %q", result.StopReason, StopMaxStepsReached)
	}

	expiredCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result, err = Run(expiredCtx, cfg, st, Options{
		Question:       "alpha",
		DisablePlanner: true,
	})
	if err != nil {
		t.Fatalf("Run timeout: %v", err)
	}
	if result.StopReason != StopTimeoutExceeded {
		t.Fatalf("stop_reason=%q, want %q", result.StopReason, StopTimeoutExceeded)
	}
}

func TestRunEnforcesPerStageTimeout(t *testing.T) {
	t.Parallel()

	cfg, st := newRunnerStore(t)
	planner := fakeRunnerBinary(t, "{}", 250*time.Millisecond)
	result, err := Run(context.Background(), cfg, st, Options{
		Question:        "Alpha planner timeout",
		PlannerModel:    "cli/test/planner",
		PlannerBinary:   planner,
		UseModelPlanner: true,
		StageTimeout:    time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.StopReason != StopTimeoutExceeded {
		t.Fatalf("stop_reason=%q, want %q warnings=%v", result.StopReason, StopTimeoutExceeded, result.Warnings)
	}
}

func TestRunPerformsOneJudgedRelatedExpansion(t *testing.T) {
	t.Parallel()

	cfg, st := newRunnerStore(t)
	seedRunnerLinkedEvidence(t, st)
	synth := fakeRunnerBinary(t, "Alpha runner answer [x:runner-alpha].", 0)

	var retryStarts int
	result, err := Run(context.Background(), cfg, st, Options{
		Question:             "Alpha Runner",
		Limit:                4,
		RelatedLimit:         1,
		DisablePlanner:       true,
		Model:                "cli/test/research",
		Binary:               synth,
		MinEvidenceForEnough: 2,
		Progress: func(event ProgressEvent) {
			if event.Stage == "retry" && event.Status == "start" {
				retryStarts++
			}
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if retryStarts != 1 {
		t.Fatalf("expected one retry, got %d", retryStarts)
	}
	if len(result.Pack.Evidence) < 2 || !containsRunnerSourceKey(result.Pack.Evidence, "src:runner-alpha-related") {
		t.Fatalf("expected related evidence after judged expansion: %+v", result.Pack.Evidence)
	}
	if result.Synthesis == nil || result.Synthesis.AnswerStatus != "ok" {
		t.Fatalf("expected synthesis result: %+v", result)
	}
}

func TestRunStopsVerificationFailedForInvalidAnswerCitation(t *testing.T) {
	t.Parallel()

	cfg, st := newRunnerStore(t)
	seedRunnerLinkedEvidence(t, st)
	synth := fakeRunnerBinary(t, "Alpha runner answer cites missing evidence [src:missing-runner].", 0)

	result, err := Run(context.Background(), cfg, st, Options{
		Question:       "Alpha Runner",
		Limit:          4,
		DisablePlanner: true,
		Model:          "cli/test/research",
		Binary:         synth,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.StopReason != StopVerificationFailed || result.Verification.Passed || len(result.Verification.Errors) == 0 {
		t.Fatalf("expected verification_failed result: %+v", result)
	}
	if result.Synthesis == nil || !strings.Contains(result.Synthesis.Answer, "src:missing-runner") {
		t.Fatalf("expected rejected answer to remain in traceable result: %+v", result.Synthesis)
	}
	traceDir := assertRunnerTraceDir(t, cfg, result.TracePath)
	traceJSON, err := os.ReadFile(filepath.Join(traceDir, "run.json"))
	if err != nil {
		t.Fatalf("read trace json: %v", err)
	}
	for _, value := range []string{`"stop_reason": "verification_failed"`, `"code": "verification_failed"`, "runner_verification_failed", "src:missing-runner"} {
		if !strings.Contains(string(traceJSON), value) {
			t.Fatalf("expected verification failure trace to contain %q:\n%s", value, string(traceJSON))
		}
	}
}

func TestRunSurfacesTruncatedEvidenceWarnings(t *testing.T) {
	t.Parallel()

	cfg, st := newRunnerStore(t)
	seedRunnerLinkedEvidence(t, st)
	synth := fakeRunnerBinary(t, "Alpha runner answer [x:runner-alpha].", 0)

	result, err := Run(context.Background(), cfg, st, Options{
		Question:         "Alpha Runner",
		Limit:            4,
		DisablePlanner:   true,
		Model:            "cli/test/research",
		Binary:           synth,
		MaxEvidenceChars: 200,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.StopReason != StopEnoughEvidence || result.Synthesis == nil || result.Synthesis.AnswerStatus != "ok_truncated" {
		t.Fatalf("expected truncated successful result: %+v", result)
	}
	if !containsString(result.Synthesis.Warnings, "evidence_truncated") || !containsString(result.Warnings, "evidence_truncated") {
		t.Fatalf("expected visible truncation warnings: synthesis=%v result=%v", result.Synthesis.Warnings, result.Warnings)
	}
}

func TestRunRecordsAdvisoryAnswerReviewFailures(t *testing.T) {
	t.Parallel()

	cfg, st := newRunnerStore(t)
	seedRunnerLinkedEvidence(t, st)
	synth := fakeRunnerBinary(t, "Alpha runner answer [x:runner-alpha].", 0)
	review := fakeRunnerBinary(t, `{"verdict":"fail","errors":["uncited material claim"]}`, 0)

	result, err := Run(context.Background(), cfg, st, Options{
		Question:           "Alpha Runner",
		Limit:              4,
		DisablePlanner:     true,
		Model:              "cli/test/research",
		Binary:             synth,
		EnableAnswerReview: true,
		AnswerReviewModel:  "cli/test/review",
		AnswerReviewBinary: review,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.StopReason != StopEnoughEvidence || result.AnswerReview.Verdict != AnswerReviewFail {
		t.Fatalf("answer review should be advisory, got %+v", result)
	}
	if !containsString(result.Warnings, "answer_review_failed") || !containsString(result.Warnings, "uncited material claim") {
		t.Fatalf("expected answer review warnings in result: %+v", result.Warnings)
	}
	traceDir := assertRunnerTraceDir(t, cfg, result.TracePath)
	traceJSON, err := os.ReadFile(filepath.Join(traceDir, "run.json"))
	if err != nil {
		t.Fatalf("read trace json: %v", err)
	}
	for _, value := range []string{"runner_answer_review_warning", "uncited material claim", `"verdict": "fail"`} {
		if !strings.Contains(string(traceJSON), value) {
			t.Fatalf("expected answer review trace to contain %q:\n%s", value, string(traceJSON))
		}
	}
}

func TestRunDoesNotMutateBrainDatabase(t *testing.T) {
	t.Parallel()

	cfg, st := newRunnerStore(t)
	seedRunnerLinkedEvidence(t, st)
	brainTables := []string{"items", "sources", "item_source_links", "source_summary_versions"}
	before := runnerTableCounts(t, cfg, brainTables)
	synth := fakeRunnerBinary(t, "Alpha runner answer [x:runner-alpha].", 0)

	if _, err := Run(context.Background(), cfg, st, Options{
		Question:       "Alpha Runner",
		Limit:          4,
		DisablePlanner: true,
		Model:          "cli/test/research",
		Binary:         synth,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	after := runnerTableCounts(t, cfg, brainTables)
	if !equalCounts(before, after) {
		t.Fatalf("runner mutated brain tables: before=%+v after=%+v", before, after)
	}
}

func TestVerifyCitationsRejectsKeysOutsideFinalPack(t *testing.T) {
	t.Parallel()

	pack := brainresearch.Pack{
		Evidence: []ask.Evidence{{SourceKey: "x:allowed"}},
	}
	verification := VerifyCitations(pack, brainresearch.SynthesisResult{
		Answer:       "Missing citation metadata [x:allowed].",
		AnswerStatus: "ok",
		Citations:    []brainresearch.Citation{{SourceKey: "x:missing"}},
	})
	if verification.Passed || len(verification.Errors) == 0 {
		t.Fatalf("expected verification failure: %+v", verification)
	}

	verification = VerifyCitations(pack, brainresearch.SynthesisResult{
		Answer:       "Uppercase citation prefix [X:allowed].",
		AnswerStatus: "ok",
		Citations:    []brainresearch.Citation{{SourceKey: "x:allowed"}},
	})
	if verification.Passed || !strings.Contains(strings.Join(verification.Errors, "\n"), "exact source key") {
		t.Fatalf("expected exact-prefix verification failure: %+v", verification)
	}

	nearMissPack := brainresearch.Pack{
		Evidence: []ask.Evidence{{SourceKey: "src:3fcc0a4088e1"}},
	}
	verification = VerifyCitations(nearMissPack, brainresearch.SynthesisResult{
		Answer:       "Near-miss citation key [src:3fcc0a88e1].",
		AnswerStatus: "ok",
		Citations:    []brainresearch.Citation{{SourceKey: "src:3fcc0a4088e1"}},
	})
	if verification.Passed || !strings.Contains(strings.Join(verification.Errors, "\n"), "nearest evidence source key is src:3fcc0a4088e1") {
		t.Fatalf("expected near-miss source key suggestion: %+v", verification)
	}

	verification = VerifyCitations(pack, brainresearch.SynthesisResult{
		Answer:       "Answer has evidence but no source key marker.",
		AnswerStatus: "ok",
		Citations:    []brainresearch.Citation{{SourceKey: "x:allowed"}},
	})
	if verification.Passed || !strings.Contains(strings.Join(verification.Errors, "\n"), "no source-key citations") {
		t.Fatalf("expected missing citation marker failure: %+v", verification)
	}

	verification = VerifyCitations(brainresearch.Pack{}, brainresearch.SynthesisResult{
		Answer:       "No evidence answer [x:allowed].",
		AnswerStatus: "ok",
	})
	if verification.Passed || !strings.Contains(strings.Join(verification.Errors, "\n"), "no-evidence") {
		t.Fatalf("expected no-evidence completed answer failure: %+v", verification)
	}

	verification = VerifyCitations(pack, brainresearch.SynthesisResult{
		Answer:       "Allowed answer citation [x:allowed].",
		AnswerStatus: "ok",
	})
	if !verification.Passed || len(verification.Warnings) == 0 {
		t.Fatalf("expected citation metadata mismatch warning: %+v", verification)
	}
}

func newRunnerStore(t *testing.T) (config.Config, *store.Store) {
	t.Helper()

	cfg, err := config.Load(t.TempDir())
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
	t.Cleanup(func() {
		_ = st.Close()
	})
	return cfg, st
}

func seedRunnerLinkedEvidence(t *testing.T, st *store.Store) {
	t.Helper()

	ctx := context.Background()
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	item, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:runner-alpha",
		SourceType:   "x_bookmark",
		ExternalID:   "runner-alpha",
		CanonicalURL: "https://x.example/runner-alpha",
		Title:        "Alpha Runner",
		Text:         "Alpha Runner direct evidence.",
		SummaryText:  "Alpha Runner direct evidence.",
		ContentHash:  "runner-alpha-hash",
		NotePath:     "items/x/runner-alpha.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	source, err := st.UpsertSourceLink(ctx, item.ItemID, model.SourceCandidate{
		SourceKey:     "src:runner-alpha-related",
		OriginalURL:   "https://example.com/runner-alpha",
		CanonicalURL:  "https://example.com/runner-alpha",
		NormalizedURL: "https://example.com/runner-alpha",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/runner-alpha.md",
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, source.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/runner-alpha",
		FinalURL:     "https://example.com/runner-alpha",
		Title:        "Supplemental linked source",
		Content:      "Supplemental document attached to the saved item.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "test",
		ToolVersion:  "test",
	}, "runner-alpha-source-hash"); err != nil {
		t.Fatalf("save extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(ctx, source.SourceID, model.SummaryResult{
		Text:          "Supplemental document attached to the saved item.",
		Status:        "ok",
		Model:         "test/model",
		PromptVersion: "test",
		Tool:          "test",
		ToolVersion:   "test",
		FetchedAt:     now,
	}); err != nil {
		t.Fatalf("save summary: %v", err)
	}
}

func fakeRunnerBinary(t *testing.T, summary string, sleep time.Duration) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "summarize")
	if runtime.GOOS == "windows" {
		binary += ".bat"
	}
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	payload := `{"input":{"model":"cli/test/research"},"extracted":{"content":"context"},"summary":` + string(summaryJSON) + `}`
	sleepLine := ""
	if sleep > 0 {
		sleepLine = "sleep 1\n"
	}
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ] || [ \"$1\" = \"version\" ]; then echo summarize-test; exit 0; fi\n" +
		sleepLine +
		"printf '%s\\n' " + shellQuote(payload) + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return binary
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func hasProgressStage(events []ProgressEvent, stage string) bool {
	for _, event := range events {
		if event.Stage == stage {
			return true
		}
	}
	return false
}

func assertRunnerTrace(t *testing.T, cfg config.Config, tracePath string) {
	t.Helper()

	traceDir := assertRunnerTraceDir(t, cfg, tracePath)
	for _, name := range []string{"run.md", "run.json", "synthesis-input.md", ".complete"} {
		if _, err := os.Stat(filepath.Join(traceDir, name)); err != nil {
			t.Fatalf("expected trace file %s: %v", name, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(traceDir, "run.json"))
	if err != nil {
		t.Fatalf("read trace json: %v", err)
	}
	var trace struct {
		Events []struct {
			Name string `json:"name"`
		} `json:"events"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(data, &trace); err != nil {
		t.Fatalf("unmarshal trace json: %v\n%s", err, string(data))
	}
	if trace.StopReason == "" {
		t.Fatalf("expected trace stop reason:\n%s", string(data))
	}
	assertTraceEventBefore(t, trace.Events, "runner_planning_start", "runner_retrieval_start")
	assertTraceEventBefore(t, trace.Events, "runner_retrieval_done", "runner_inspection_start")
	assertTraceEventBefore(t, trace.Events, "runner_verification_done", "runner_trace_start")
}

func assertRunnerTraceDir(t *testing.T, cfg config.Config, tracePath string) string {
	t.Helper()

	if !strings.HasPrefix(tracePath, "research-runs/") {
		t.Fatalf("expected relative trace path, got %q", tracePath)
	}
	traceDir := filepath.Join(cfg.DataDir, filepath.FromSlash(tracePath))
	if _, err := os.Stat(traceDir); err != nil {
		t.Fatalf("expected trace dir %s: %v", traceDir, err)
	}
	return traceDir
}

func containsRunnerSourceKey(rows []ask.Evidence, key string) bool {
	for _, row := range rows {
		if row.SourceKey == key {
			return true
		}
	}
	return false
}

func runnerTableCounts(t *testing.T, cfg config.Config, tables []string) map[string]int {
	t.Helper()

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()
	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}

func equalCounts(a map[string]int, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func assertTraceEventBefore(t *testing.T, events []struct {
	Name string `json:"name"`
}, first string, second string) {
	t.Helper()

	firstIndex := -1
	secondIndex := -1
	for i, event := range events {
		if event.Name == first && firstIndex < 0 {
			firstIndex = i
		}
		if event.Name == second && secondIndex < 0 {
			secondIndex = i
		}
	}
	if firstIndex < 0 || secondIndex < 0 || firstIndex >= secondIndex {
		t.Fatalf("expected trace event %q before %q, got first=%d second=%d events=%+v", first, second, firstIndex, secondIndex, events)
	}
}
