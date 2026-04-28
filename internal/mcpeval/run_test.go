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

func TestRunFixtureCoversMCPRetrievalSurfaces(t *testing.T) {
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

	seedMCPFixture(t, st)

	report, err := Run(context.Background(), cfg, st, Options{Cases: []Case{
		{
			Name:                   "tagged entity query",
			Question:               "Example Person Specific Project",
			Limit:                  5,
			MinEvidence:            1,
			ExpectTopSourceKeys:    []string{"x:fixture-tagged"},
			ExpectText:             []string{"specific project budget memo"},
			ExpectTopText:          []string{"specific project budget memo"},
			RequireTopMatchedTerms: []string{"example", "person", "specific", "project"},
			ForbidTopMissingTerms:  []string{"project"},
			MaxLatencyMS:           5000,
		},
		{
			Name:                   "ocr text query",
			Question:               "Visible OCR Alpha Launch Code",
			Limit:                  5,
			MinEvidence:            1,
			ExpectTopSourceKeys:    []string{"x:fixture-ocr"},
			ExpectText:             []string{"Visible OCR Alpha Launch Code"},
			ExpectTopText:          []string{"Visible OCR Alpha Launch Code"},
			RequireTopMatchedTerms: []string{"visible", "ocr", "alpha", "launch", "code"},
			ForbidTopMissingTerms:  []string{"ocr"},
			MaxLatencyMS:           5000,
		},
		{
			Name:                   "media transcript query",
			Question:               "beta release offline indexing transcript",
			Limit:                  5,
			MinEvidence:            1,
			ExpectTopSourceKeys:    []string{"x:fixture-transcript"},
			ExpectText:             []string{"beta release enables offline indexing"},
			ExpectTopText:          []string{"beta release enables offline indexing"},
			RequireTopMatchedTerms: []string{"beta", "release", "offline", "indexing"},
			ForbidTopMissingTerms:  []string{"offline"},
			MaxLatencyMS:           5000,
		},
		{
			Name:                   "source type query",
			Question:               "Chrome DevTools MCP browser automation",
			Limit:                  5,
			MinEvidence:            1,
			SourceTypes:            []string{"github"},
			ExpectTopSourceKeys:    []string{"src:fixture-github"},
			ExpectText:             []string{"browser automation"},
			ExpectTopText:          []string{"browser automation"},
			RequireTopMatchedTerms: []string{"chrome", "devtools", "mcp"},
			ForbidTopMissingTerms:  []string{"mcp"},
			MaxLatencyMS:           5000,
		},
		{
			Name:                "related source expansion",
			Question:            "linked provenance report",
			Limit:               4,
			IncludeRelated:      true,
			RelatedLimit:        1,
			MinEvidence:         2,
			ExpectSourceKeys:    []string{"x:fixture-linked", "src:fixture-linked-report"},
			ExpectAnySourceKeys: []string{"src:fixture-linked-report"},
			ExpectText:          []string{"linked provenance report"},
			MaxLatencyMS:        5000,
		},
	}})
	if err != nil {
		t.Fatalf("run eval: %v", err)
	}
	if report.Failed != 0 {
		t.Fatalf("expected all fixture eval cases to pass: %+v", report)
	}
	if report.Passed != 5 {
		t.Fatalf("expected 5 passing fixture eval cases, got %+v", report)
	}
}

func seedMCPFixture(t *testing.T, st *store.Store) {
	t.Helper()

	ctx := context.Background()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	upsertItem := func(item model.Item) int64 {
		t.Helper()
		item.ImportedAt = now
		item.UpdatedAt = now
		item.LastSeenAt = now
		result, err := st.UpsertItem(ctx, item)
		if err != nil {
			t.Fatalf("upsert %s: %v", item.SourceKey, err)
		}
		if item.UserTags != "" {
			if err := st.SaveItemUserTags(ctx, result.ItemID, item.UserTags); err != nil {
				t.Fatalf("save tags %s: %v", item.SourceKey, err)
			}
		}
		return result.ItemID
	}

	upsertItem(model.Item{
		SourceKey:    "x:fixture-tagged",
		SourceType:   "x_bookmark",
		ExternalID:   "fixture-tagged",
		CanonicalURL: "https://x.example/fixture-tagged",
		Title:        "Example Person specific project update",
		Text:         "A specific project budget memo from Example Person.",
		UserTags:     "example-person,specific-project,policy-budget",
		ContentHash:  "fixture-tagged-hash",
		NotePath:     "items/x/fixture-tagged.md",
		RawJSON:      `{}`,
	})

	upsertItem(model.Item{
		SourceKey:    "x:fixture-ocr",
		SourceType:   "x_bookmark",
		ExternalID:   "fixture-ocr",
		CanonicalURL: "https://x.example/fixture-ocr",
		Title:        "Screenshot evidence",
		Text:         "A saved screenshot with OCR-only evidence.",
		OCRText:      "Visible OCR Alpha Launch Code appears only in the image.",
		OCRStatus:    "ok",
		OCRModel:     "test/vision",
		OCRTool:      "test-ocr",
		OCRAt:        now,
		UserTags:     "screenshot-evidence",
		ContentHash:  "fixture-ocr-hash",
		NotePath:     "items/x/fixture-ocr.md",
		RawJSON:      `{}`,
	})

	upsertItem(model.Item{
		SourceKey:              "x:fixture-transcript",
		SourceType:             "x_bookmark",
		ExternalID:             "fixture-transcript",
		CanonicalURL:           "https://x.example/fixture-transcript",
		Title:                  "Video transcript evidence",
		Text:                   "A saved video post.",
		ArticleTitle:           "X Media Transcript",
		ArticleText:            "Transcript: The beta release enables offline indexing for local memory search.",
		XMediaTranscriptStatus: "ok",
		XMediaTranscriptAt:     now,
		UserTags:               "video-transcript,offline-indexing",
		ContentHash:            "fixture-transcript-hash",
		NotePath:               "items/x/fixture-transcript.md",
		RawJSON:                `{}`,
	})

	githubItemID := upsertItem(model.Item{
		SourceKey:    "github_star:fixture/devtools-mcp",
		SourceType:   "github_star",
		ExternalID:   "fixture/devtools-mcp",
		CanonicalURL: "https://github.com/fixture/devtools-mcp",
		Title:        "fixture/devtools-mcp",
		Text:         "Saved GitHub repository.",
		UserTags:     "mcp,browser-automation",
		ContentHash:  "fixture-github-item-hash",
		NotePath:     "items/github/fixture-devtools-mcp.md",
		RawJSON:      `{}`,
	})
	githubLink, err := st.UpsertSourceLink(ctx, githubItemID, model.SourceCandidate{
		SourceKey:     "src:fixture-github",
		OriginalURL:   "https://github.com/fixture/devtools-mcp",
		CanonicalURL:  "https://github.com/fixture/devtools-mcp",
		NormalizedURL: "https://github.com/fixture/devtools-mcp",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/fixture-devtools-mcp.md",
	})
	if err != nil {
		t.Fatalf("upsert github source: %v", err)
	}
	saveFixtureSource(t, st, githubLink.SourceID, "src:fixture-github", "Chrome DevTools MCP fixture", "Chrome DevTools MCP provides browser automation for coding agents.", "github")

	linkedItemID := upsertItem(model.Item{
		SourceKey:    "x:fixture-linked",
		SourceType:   "x_bookmark",
		ExternalID:   "fixture-linked",
		CanonicalURL: "https://x.example/fixture-linked",
		Title:        "Linked provenance note",
		Text:         "This bookmark points to a linked provenance report.",
		UserTags:     "linked-provenance",
		ContentHash:  "fixture-linked-hash",
		NotePath:     "items/x/fixture-linked.md",
		RawJSON:      `{}`,
	})
	linkedSource, err := st.UpsertSourceLink(ctx, linkedItemID, model.SourceCandidate{
		SourceKey:     "src:fixture-linked-report",
		OriginalURL:   "https://example.com/provenance-report",
		CanonicalURL:  "https://example.com/provenance-report",
		NormalizedURL: "https://example.com/provenance-report",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/fixture-linked-report.md",
	})
	if err != nil {
		t.Fatalf("upsert linked source: %v", err)
	}
	saveFixtureSource(t, st, linkedSource.SourceID, "src:fixture-linked-report", "Linked provenance report", "The linked provenance report documents the bookmark evidence chain.", "web")
}

func saveFixtureSource(t *testing.T, st *store.Store, sourceID int64, sourceKey string, title string, content string, sourceType string) {
	t.Helper()

	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	url := "https://example.com/" + sourceKey
	if _, err := st.SaveSourceExtraction(context.Background(), sourceID, model.ExtractResult{
		CanonicalURL: url,
		FinalURL:     url,
		Title:        title,
		Description:  "fixture " + sourceType + " source",
		SiteName:     sourceType,
		Content:      content,
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "fixture",
		ToolVersion:  "test",
	}, sourceKey+"-extract-hash"); err != nil {
		t.Fatalf("save extraction %s: %v", sourceKey, err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), sourceID, model.SummaryResult{
		Text:          content,
		RawJSON:       `{"summary":"fixture"}`,
		Model:         "fixture/model",
		PromptVersion: "fixture",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "fixture",
		ToolVersion:   "test",
	}); err != nil {
		t.Fatalf("save summary %s: %v", sourceKey, err)
	}
}
