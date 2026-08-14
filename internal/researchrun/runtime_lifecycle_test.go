package researchrun

import (
	"context"
	"testing"

	"github.com/darron/dbrain/internal/brainresearch"
)

func TestRunnerRetrySharesOwnerRuntime(t *testing.T) {
	cfg, st := newRunnerStore(t)
	runtime := brainresearch.NewRuntime(cfg, st)
	t.Cleanup(func() { _ = runtime.Close() })

	traceDisabled := false
	r := newRunner(context.Background(), cfg, st, Options{
		Question:       "Alpha Runner",
		DisablePlanner: true,
		TraceEnabled:   &traceDisabled,
		Runtime:        runtime,
	})
	defer r.cancel()

	builds := 0
	r.buildResearchPack = func(context.Context, brainresearch.Options) (brainresearch.Pack, error) {
		builds++
		return brainresearch.Pack{Question: "Alpha Runner"}, nil
	}

	initial, err := r.buildPack(false, r.opts.Question, "initial")
	if err != nil {
		t.Fatalf("initial build: %v", err)
	}
	_, _, _, err = r.runRetry(initial, JudgeResult{
		RetryAction:  RetryFocusedVariant,
		RetryVariant: "Alpha Runner retry",
	})
	if err != nil {
		t.Fatalf("retry build: %v", err)
	}
	if builds != 2 {
		t.Fatalf("runtime-bound builds = %d, want initial plus retry", builds)
	}
	if r.runtime != runtime {
		t.Fatal("runner did not retain the owner-supplied runtime")
	}
}
