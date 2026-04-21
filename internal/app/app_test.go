package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
)

func TestRootCommandHelpIncludesCoreCommands(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(nil)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"import", "extract", "hydrate", "stats", "search", "get"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected help output to contain %q, got %q", value, output)
		}
	}
}

func TestImportCommandHelpIncludesYouTubeImporter(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"import"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"ft", "youtube"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected import help output to contain %q, got %q", value, output)
		}
	}
}

func TestExtractCommandHelpIncludesLinksAndSources(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"extract"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"links", "sources"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected extract help output to contain %q, got %q", value, output)
		}
	}
}

func TestExtractSourcesCommandOutputsZeroStatsForEmptyBacklog(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "extract", "sources", "--limit", "5"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"Sources queued: 0", "Sources summarized: 0", "Errors: 0"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected extract sources output to contain %q, got %q", value, output)
		}
	}
}

func TestStatsSourcesCommandOutputsSummaryStatusCounts(t *testing.T) {
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
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:test/one",
		SourceType:   "github_star",
		ExternalID:   "test/one",
		CanonicalURL: "https://github.com/test/one",
		Title:        "test/one",
		ContentHash:  "hash-one",
		NotePath:     "items/github/test-one.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item one: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-one",
		OriginalURL:   "https://github.com/test/one",
		CanonicalURL:  "https://github.com/test/one",
		NormalizedURL: "https://github.com/test/one",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-one.md",
	})
	if err != nil {
		t.Fatalf("source link one: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/one",
		FinalURL:     "https://github.com/test/one",
		Title:        "test/one",
		Content:      "README one",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "2022-11-28",
	}, "source-hash-one"); err != nil {
		t.Fatalf("save extract one: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "summary one",
		RawJSON:       `{"summary":"summary one"}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "summarize",
		ToolVersion:   "test-1.0.0",
	}); err != nil {
		t.Fatalf("save summary one: %v", err)
	}

	item, err = st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:test/two",
		SourceType:   "github_star",
		ExternalID:   "test/two",
		CanonicalURL: "https://github.com/test/two",
		Title:        "test/two",
		ContentHash:  "hash-two",
		NotePath:     "items/github/test-two.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item two: %v", err)
	}
	link, err = st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-two",
		OriginalURL:   "https://github.com/test/two",
		CanonicalURL:  "https://github.com/test/two",
		NormalizedURL: "https://github.com/test/two",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-two.md",
	})
	if err != nil {
		t.Fatalf("source link two: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/two",
		FinalURL:     "https://github.com/test/two",
		Title:        "test/two",
		Content:      "README two",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "2022-11-28",
	}, "source-hash-two"); err != nil {
		t.Fatalf("save extract two: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"stats", "sources",
		"--source-type", "github",
		"--extract-tool", "github-api",
		"--group-by", "summary-status",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"ok: 1", "pending: 1", "Total: 2"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected stats output to contain %q, got %q", value, output)
		}
	}
}

func TestStatsActivityCommandOutputsRecentWrites(t *testing.T) {
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
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:activity/test",
		SourceType:   "github_star",
		ExternalID:   "activity/test",
		CanonicalURL: "https://github.com/activity/test",
		Title:        "activity/test",
		ContentHash:  "activity-hash",
		NotePath:     "items/github/activity-test.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
		SourceKey:     "src:activity-test",
		OriginalURL:   "https://github.com/activity/test",
		CanonicalURL:  "https://github.com/activity/test",
		NormalizedURL: "https://github.com/activity/test",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/activity-test.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/activity/test",
		FinalURL:     "https://github.com/activity/test",
		Title:        "activity/test",
		Content:      "README",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "2022-11-28",
	}, "activity-source-hash"); err != nil {
		t.Fatalf("save extract: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "summary",
		RawJSON:       `{"summary":"summary"}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "summarize",
		ToolVersion:   "test-1.0.0",
	}); err != nil {
		t.Fatalf("save summary: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"stats", "activity",
		"--window", "15m",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{
		"Latest item write:",
		"Latest source write:",
		"Latest source summary:",
		"Items updated in window: 1",
		"Sources updated in window: 1",
		"Sources summarized in window: 1",
	} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected activity output to contain %q, got %q", value, output)
		}
	}
}

func TestStatsBacklogCommandOutputsPendingQueues(t *testing.T) {
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
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:backlog",
		SourceType:   "x_bookmark",
		ExternalID:   "backlog",
		CanonicalURL: "https://x.com/example/status/backlog",
		Title:        "backlog",
		ContentHash:  "backlog-hash",
		LinksJSON:    `["https://example.com/post"]`,
		NotePath:     "items/x/backlog.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert x item: %v", err)
	}
	if _, err := st.SaveXHydration(context.Background(), item.ItemID, model.XHydration{
		Status: "error",
		Error:  "boom",
	}); err != nil {
		t.Fatalf("save hydration error: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"stats", "backlog",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{
		"Queue drained: no",
		"X hydration pending: 1",
		"Link discovery pending: 1",
	} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected backlog output to contain %q, got %q", value, output)
		}
	}
}
