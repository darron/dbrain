package app

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
)

type semanticStageExecutorFunc func(context.Context, store.SemanticRefreshRun) (semanticrefresh.StageOutcome, error)

func (f semanticStageExecutorFunc) Execute(ctx context.Context, run store.SemanticRefreshRun) (semanticrefresh.StageOutcome, error) {
	return f(ctx, run)
}

func TestSemanticStageTrackerAggregatesRepeatedExecutionUnits(t *testing.T) {
	t0 := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	times := []time.Time{t0, t0.Add(2 * time.Second), t0.Add(3 * time.Second), t0.Add(6 * time.Second)}
	now := func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}
	generated := []int64{8, 12}
	executor := semanticStageExecutorFunc(func(_ context.Context, run store.SemanticRefreshRun) (semanticrefresh.StageOutcome, error) {
		value := generated[0]
		generated = generated[1:]
		return semanticrefresh.StageOutcome{
			NextStage: store.SemanticRefreshEmbedding,
			Counters:  store.SemanticRefreshCounters{EmbeddedChunks: value},
			Debt:      semanticrefresh.Debt{PendingEmbeddings: int(13 - value)},
		}, nil
	})
	tracker := newSemanticStageTracker(now)
	measured := tracker.Wrap(executor)

	first := store.SemanticRefreshRun{RunID: "run-a", Stage: store.SemanticRefreshEmbedding, Counters: store.SemanticRefreshCounters{EmbeddedChunks: 5}}
	if _, err := measured.Execute(t.Context(), first); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	second := first
	second.Counters.EmbeddedChunks = 8
	if _, err := measured.Execute(t.Context(), second); err != nil {
		t.Fatalf("second Execute: %v", err)
	}

	stages := tracker.Stages()
	if len(stages) != 1 {
		t.Fatalf("stages = %#v, want one aggregate", stages)
	}
	stage := stages[0]
	if stage.Stage != store.SemanticRefreshEmbedding || stage.Duration != 5*time.Second || stage.Units != 2 || stage.Status != "ok" {
		t.Fatalf("stage identity/timing = %#v", stage)
	}
	if stage.Counters.EmbeddedChunks != 7 {
		t.Fatalf("embedded chunk delta = %d, want 7", stage.Counters.EmbeddedChunks)
	}
	if stage.RemainingDebt.PendingEmbeddings != 1 {
		t.Fatalf("remaining debt = %#v", stage.RemainingDebt)
	}
	if !slices.Equal(stage.RunIDs, []string{"run-a"}) {
		t.Fatalf("run IDs = %#v", stage.RunIDs)
	}
}
