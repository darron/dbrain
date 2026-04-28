package mcpeval

import (
	"context"
	"testing"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
)

func TestRunEvaluatesExpectedRetrieval(t *testing.T) {
	t.Parallel()

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
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:carney-eval",
		SourceType:   "x_bookmark",
		ExternalID:   "carney-eval",
		CanonicalURL: "https://x.com/example/status/carney-eval",
		Title:        "Mark Carney saved item",
		Text:         "Saved evidence about Mark Carney and fiscal policy.",
		SummaryText:  "Mark Carney appears in the local corpus.",
		ContentHash:  "carney-eval-hash",
		NotePath:     "items/x/carney-eval.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if err := st.SaveItemUserTags(context.Background(), item.ItemID, "mark-carney, politics"); err != nil {
		t.Fatalf("save tags: %v", err)
	}

	report, err := Run(context.Background(), cfg, st, Options{Cases: []Case{{
		Name:                   "carney",
		Question:               "What does my brain know about Mark Carney?",
		Limit:                  5,
		MinEvidence:            1,
		ExpectSourceKeys:       []string{"x:carney-eval"},
		ExpectTopSourceKeys:    []string{"x:carney-eval"},
		ExpectText:             []string{"fiscal policy"},
		ExpectTopText:          []string{"fiscal policy"},
		ForbidText:             []string{"unrelated boilerplate"},
		RequireTopMatchedTerms: []string{"mark", "carney"},
		ForbidTopMissingTerms:  []string{"mark", "carney"},
		MaxLatencyMS:           5000,
	}}})
	if err != nil {
		t.Fatalf("run eval: %v", err)
	}
	if report.Passed != 1 || report.Failed != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.Cases) != 1 || !report.Cases[0].Passed {
		t.Fatalf("expected passing case: %+v", report.Cases)
	}
	if report.Cases[0].EvidenceCount == 0 {
		t.Fatalf("expected evidence in report: %+v", report.Cases[0])
	}
}
