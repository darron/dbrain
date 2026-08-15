package researcheval

import (
	"context"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/brainresearch"
)

func TestEvalDirectAndRunnerCasesReuseOwnerRuntime(t *testing.T) {
	cfg, st := newResearchEvalStore(t)
	runtime := brainresearch.NewRuntime(cfg, st)
	if err := runtime.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}

	_, err := runCase(context.Background(), cfg, st, runtime, Case{
		Name:           "direct",
		Question:       "Alpha eval",
		DisablePlanner: true,
	})
	if err == nil || !strings.Contains(err.Error(), "semantic research runtime is shut down") {
		t.Fatalf("direct case did not use closed owner runtime: %v", err)
	}

	runnerOpts := runnerOptions(Case{Question: "Alpha eval"}, runtime)
	if runnerOpts.Runtime != runtime {
		t.Fatal("runner case did not receive eval runtime")
	}
}
