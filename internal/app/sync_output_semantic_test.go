package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
)

func TestWriteSyncStatsWithSemanticIncludesStageRowsAndFullDuration(t *testing.T) {
	stats := syncSemanticTestStats()
	result := semanticrefresh.Result{
		Outcome:  semanticrefresh.OutcomeCompleted,
		Duration: 5 * time.Second,
		Stages: []semanticrefresh.StageStats{
			{
				Stage:    store.SemanticRefreshEmbedding,
				Duration: 2 * time.Second,
				Units:    3,
				Status:   "ok",
				Counters: store.SemanticRefreshCounters{EmbeddedChunks: 17},
				RemainingDebt: semanticrefresh.Debt{
					PendingEmbeddings: 2,
				},
			},
			{
				Stage:         store.SemanticRefreshReadiness,
				Duration:      time.Second,
				Units:         1,
				Status:        "ok",
				RemainingDebt: semanticrefresh.Debt{Indexed: 315000},
			},
		},
	}
	stats = completeSyncStatsWithSemantic(stats, result)
	var output bytes.Buffer
	if err := writeSyncStatsWithSemantic(&output, stats, result, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Duration:  7.00s",
		"Semantic Embedding",
		"chunks=17",
		"units=3 pending=2",
		"Semantic Readiness",
		"indexed=315000",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output omitted %q:\n%s", want, output.String())
		}
	}
}

func TestSemanticSyncSummaryRowsShowsExplicitSkip(t *testing.T) {
	rows := semanticSyncSummaryRows(semanticrefresh.Result{
		Outcome:    semanticrefresh.OutcomeSkipped,
		SkipReason: "semantic_mode_off",
		Duration:   time.Second,
	}, nil)
	if len(rows) != 1 || rows[0][0] != "Semantic Refresh" || rows[0][4] != "0" || !strings.Contains(rows[0][3], "reason=semantic_mode_off") {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestWriteSyncStatsWithSemanticGCIncludesTerminalRowAndFullDuration(t *testing.T) {
	stats := syncSemanticTestStats()
	semantic := semanticrefresh.Result{
		Outcome:  semanticrefresh.OutcomeCompleted,
		Duration: 5 * time.Second,
	}
	gc := &syncSemanticGCResult{
		Status:            syncSemanticGCStatusError,
		Duration:          3 * time.Second,
		GenerationsPruned: 4,
		SegmentsPruned:    2,
		MemberRowsPruned:  19,
		FilesystemDeleted: 6,
		DeletedBytes:      2048,
		Error:             "filesystem_unlink",
	}
	stats = completeSyncStatsWithSemanticGC(stats, semantic, gc)
	if stats.Duration != 10*time.Second {
		t.Fatalf("full sync duration=%s, want 10s", stats.Duration)
	}
	var output bytes.Buffer
	if err := writeSyncStatsWithSemantic(&output, stats, semantic, nil, gc); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Duration:  10.00s",
		"Semantic GC",
		"dirs=6 bytes=2048",
		"status=error generations=4 segments=2 members=19",
		"error=filesystem_unlink",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output omitted %q:\n%s", want, output.String())
		}
	}
}
