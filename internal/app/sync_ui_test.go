package app

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
)

func TestSyncProgressUISemanticStagesRenderKnownProgressInPlace(t *testing.T) {
	stages := []store.SemanticRefreshStage{
		store.SemanticRefreshProjection,
		store.SemanticRefreshEmbedding,
		store.SemanticRefreshFlush,
		store.SemanticRefreshCompaction,
		store.SemanticRefreshVerify,
		store.SemanticRefreshReadiness,
	}
	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			var output bytes.Buffer
			ui := newSyncProgressUI(&output)
			ui.tty = true
			reporter := newSemanticProgressReporter(ui, time.Now)
			if err := reporter.Report(semanticrefresh.Progress{
				Stage: stage,
				Work:  semanticrefresh.StageWork{Current: 1, Total: 4, TotalKnown: true},
			}); err != nil {
				t.Fatalf("report: %v", err)
			}
			if err := reporter.Finish(true); err != nil {
				t.Fatalf("finish: %v", err)
			}
			ui.Close()

			got := output.String()
			if strings.Contains(got, "Semantic "+string(stage)+" progress:") {
				t.Fatalf("TTY progress left a permanent semantic snapshot: %q", got)
			}
			for _, want := range []string{"\r\x1b[K", "1/4", "25%"} {
				if !strings.Contains(got, want) {
					t.Fatalf("TTY stage %s omitted %q: %q", stage, want, got)
				}
			}
		})
	}
}

func TestSyncProgressUISemanticETAUsesObservedThroughputAndExpandedTotal(t *testing.T) {
	base := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	now := base
	var output bytes.Buffer
	ui := newSyncProgressUI(&output)
	ui.tty = true
	ui.now = func() time.Time { return now }
	reporter := newSemanticProgressReporter(ui, func() time.Time { return now })

	progress := semanticrefresh.Progress{
		Stage: store.SemanticRefreshEmbedding,
		Work:  semanticrefresh.StageWork{Current: 0, Total: 100, TotalKnown: true},
	}
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("initial report: %v", err)
	}
	now = base.Add(10 * time.Second)
	progress.Work.Current = 25
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("progress report: %v", err)
	}
	if got := renderSyncProgressLine(ui); !strings.Contains(got, "25/100") ||
		!strings.Contains(got, "25%") || !strings.Contains(got, "(10s)") ||
		!strings.Contains(got, "ETA 30s") {
		t.Fatalf("measured progress = %q, want count, percentage, elapsed, and ETA", got)
	}

	now = base.Add(20 * time.Second)
	progress.Work = semanticrefresh.StageWork{Current: 50, Total: 200, TotalKnown: true}
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("expanded report: %v", err)
	}
	if got := renderSyncProgressLine(ui); !strings.Contains(got, "50/200") ||
		!strings.Contains(got, "25%") || !strings.Contains(got, "ETA 1m0s") {
		t.Fatalf("expanded progress = %q, want safely recalculated percentage and ETA", got)
	}

	if err := reporter.Finish(true); err != nil {
		t.Fatalf("finish: %v", err)
	}
	ui.Close()
}

func TestSyncProgressUISemanticUnknownAndKnownZeroDiffer(t *testing.T) {
	base := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		work      semanticrefresh.StageWork
		want      []string
		forbidden []string
	}{
		{name: "unknown", work: semanticrefresh.StageWork{}, want: []string{"planning"}, forbidden: []string{"0/0"}},
		{name: "known zero", work: semanticrefresh.StageWork{TotalKnown: true}, want: []string{"0/0", "100%"}, forbidden: []string{"planning"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			ui := newSyncProgressUI(&output)
			ui.tty = true
			ui.now = func() time.Time { return base }
			reporter := newSemanticProgressReporter(ui, func() time.Time { return base })
			if err := reporter.Report(semanticrefresh.Progress{Stage: store.SemanticRefreshReadiness, Work: tc.work}); err != nil {
				t.Fatalf("report: %v", err)
			}
			got := renderSyncProgressLine(ui)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("progress = %q, want %q", got, want)
				}
			}
			for _, forbidden := range tc.forbidden {
				if strings.Contains(got, forbidden) {
					t.Fatalf("progress = %q, unexpectedly contains %q", got, forbidden)
				}
			}
			if err := reporter.Finish(true); err != nil {
				t.Fatalf("finish: %v", err)
			}
			ui.Close()
		})
	}
}

func TestSyncProgressUISemanticProgressIgnoresGenericDebugCounters(t *testing.T) {
	base := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	var output bytes.Buffer
	ui := newSyncProgressUI(&output)
	ui.tty = true
	ui.now = func() time.Time { return base }
	reporter := newSemanticProgressReporter(ui, func() time.Time { return base })
	if err := reporter.Report(semanticrefresh.Progress{
		Stage: store.SemanticRefreshEmbedding,
		Work:  semanticrefresh.StageWork{Current: 25, Total: 100, TotalKnown: true},
	}); err != nil {
		t.Fatalf("report: %v", err)
	}
	if _, err := ui.LogWriter().Write([]byte(`time=2026-08-05T12:00:00Z level=WARN msg="provider retry" processed=7 candidates=8` + "\n")); err != nil {
		t.Fatalf("write log: %v", err)
	}

	if got := renderSyncProgressLine(ui); !strings.Contains(got, "25/100") || strings.Contains(got, "7/8") {
		t.Fatalf("semantic progress was overwritten by generic debug counters: %q", got)
	}
	if got := output.String(); !strings.Contains(got, "WARN") || !strings.Contains(got, "provider retry") {
		t.Fatalf("interleaved log was not printed before redraw: %q", got)
	}

	if err := reporter.Finish(true); err != nil {
		t.Fatalf("finish: %v", err)
	}
	ui.Close()
}

func TestSyncProgressUISemanticFailureRendersFailureNotSuccess(t *testing.T) {
	base := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	var output bytes.Buffer
	ui := newSyncProgressUI(&output)
	ui.tty = true
	ui.now = func() time.Time { return base }
	reporter := newSemanticProgressReporter(ui, func() time.Time { return base })
	if err := reporter.Report(semanticrefresh.Progress{
		Stage: store.SemanticRefreshEmbedding,
		Work:  semanticrefresh.StageWork{Current: 25, Total: 100, TotalKnown: true},
	}); err != nil {
		t.Fatalf("report: %v", err)
	}
	if err := reporter.Finish(false); err != nil {
		t.Fatalf("finish: %v", err)
	}
	ui.Close()

	got := output.String()
	if !strings.Contains(got, "✗") || !strings.Contains(got, "Semantic embedding") {
		t.Fatalf("failed TTY stage omitted a clean failure line: %q", got)
	}
	if strings.Contains(got, "✓") {
		t.Fatalf("failed TTY stage rendered as success: %q", got)
	}
}

func TestSyncProgressUINonTTYKeepsBoundedSemanticSnapshots(t *testing.T) {
	base := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	now := base
	var output bytes.Buffer
	ui := newSyncProgressUI(&output)
	reporter := newSemanticProgressReporter(ui, func() time.Time { return now })
	progress := semanticrefresh.Progress{
		RunID:      "do-not-print-run",
		ProfileID:  "do-not-print-profile",
		Checkpoint: "do-not-print-checkpoint",
		Stage:      store.SemanticRefreshEmbedding,
		Work:       semanticrefresh.StageWork{Current: 0, Total: 100, TotalKnown: true},
	}
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("initial report: %v", err)
	}
	now = base.Add(time.Second)
	progress.Work.Current = 1
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("early changed report: %v", err)
	}
	now = base.Add(5 * time.Second)
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("due changed report: %v", err)
	}
	now = base.Add(34 * time.Second)
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("early heartbeat: %v", err)
	}
	now = base.Add(35 * time.Second)
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("due heartbeat: %v", err)
	}
	if err := reporter.Finish(true); err != nil {
		t.Fatalf("finish: %v", err)
	}
	ui.Close()

	got := output.String()
	if count := strings.Count(got, "Semantic embedding progress:"); count != 3 {
		t.Fatalf("progress line count = %d, want 3; output:\n%s", count, got)
	}
	for _, secret := range []string{"do-not-print-run", "do-not-print-profile", "do-not-print-checkpoint"} {
		if strings.Contains(got, secret) {
			t.Fatalf("non-TTY output leaked raw diagnostic field %q: %s", secret, got)
		}
	}
}

func TestSyncProgressUINonSemanticStageStillUsesGenericDebugCounters(t *testing.T) {
	base := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	var output bytes.Buffer
	ui := newSyncProgressUI(&output)
	ui.tty = true
	ui.now = func() time.Time { return base }
	if _, err := fmt.Fprintln(ui, "==> import youtube"); err != nil {
		t.Fatalf("start stage: %v", err)
	}
	if _, err := ui.LogWriter().Write([]byte(`time=2026-08-05T12:00:00Z level=INFO msg="scanning" processed=2 candidates=4` + "\n")); err != nil {
		t.Fatalf("write log: %v", err)
	}

	got := renderSyncProgressLine(ui)
	if !strings.Contains(got, "2/4") || !strings.Contains(got, "50%") {
		t.Fatalf("generic stage progress = %q, want debug-derived count and percent", got)
	}
	if strings.Contains(got, "planning") || strings.Contains(got, "ETA") {
		t.Fatalf("generic stage unexpectedly used semantic presentation: %q", got)
	}
	ui.Close()
}

func TestSyncProgressUISemanticStageTransitionResetsETASample(t *testing.T) {
	base := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	now := base
	var output bytes.Buffer
	ui := newSyncProgressUI(&output)
	ui.tty = true
	ui.now = func() time.Time { return now }
	reporter := newSemanticProgressReporter(ui, func() time.Time { return now })
	progress := semanticrefresh.Progress{
		Stage: store.SemanticRefreshEmbedding,
		Work:  semanticrefresh.StageWork{Current: 0, Total: 100, TotalKnown: true},
	}
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("initial embedding report: %v", err)
	}
	now = base.Add(10 * time.Second)
	progress.Work.Current = 50
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("embedding progress: %v", err)
	}
	if got := renderSyncProgressLine(ui); !strings.Contains(got, "ETA 10s") {
		t.Fatalf("embedding progress = %q, want measured ETA", got)
	}

	now = base.Add(11 * time.Second)
	progress.Stage = store.SemanticRefreshProjection
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("projection transition: %v", err)
	}
	if got := renderSyncProgressLine(ui); strings.Contains(got, "ETA") {
		t.Fatalf("new stage retained prior ETA sample: %q", got)
	}
	now = base.Add(21 * time.Second)
	progress.Work.Current = 60
	if err := reporter.Report(progress); err != nil {
		t.Fatalf("projection progress: %v", err)
	}
	if got := renderSyncProgressLine(ui); !strings.Contains(got, "ETA 40s") {
		t.Fatalf("projection progress = %q, want stage-local ETA", got)
	}
	if err := reporter.Finish(true); err != nil {
		t.Fatalf("finish: %v", err)
	}
	ui.Close()
}

func renderSyncProgressLine(ui *syncProgressUI) string {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	return ui.renderActiveLocked()
}
