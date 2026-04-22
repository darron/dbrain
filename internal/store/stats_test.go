package store

import (
	"context"
	"testing"
	"time"

	"dbrain/internal/model"
)

func TestCountItemsBySourceType(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	insertTestItem(t, st, "x:one", "Article one", "Body one", now)
	insertTestItem(t, st, "x:two", "Article two", "Body two", now)

	if _, err := st.UpsertItem(ctx, testItem("github_star:test/repo", "github_star", "https://github.com/test/repo", now)); err != nil {
		t.Fatalf("upsert github item: %v", err)
	}

	buckets, err := st.CountItems(ctx, "", "source-type")
	if err != nil {
		t.Fatalf("CountItems: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(buckets))
	}
	if buckets[0].Key != "x_bookmark" || buckets[0].Count != 2 {
		t.Fatalf("unexpected first bucket: %+v", buckets[0])
	}
	if buckets[1].Key != "github_star" || buckets[1].Count != 1 {
		t.Fatalf("unexpected second bucket: %+v", buckets[1])
	}
}

func TestCountSourcesBySummaryStatusWithFilters(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	inserted, err := st.UpsertItem(ctx, testItem("gh-star:darron:test/one", "github_star", "https://github.com/test/one", now))
	if err != nil {
		t.Fatalf("upsert item one: %v", err)
	}
	sourceOne, err := st.UpsertSourceLink(ctx, inserted.ItemID, modelSourceCandidate("src:github-one", "https://github.com/test/one", "github"))
	if err != nil {
		t.Fatalf("source one: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, sourceOne.SourceID, testExtract("https://github.com/test/one", "README one", "github-api"), "hash-one"); err != nil {
		t.Fatalf("save extract one: %v", err)
	}
	if _, err := st.SaveSourceSummary(ctx, sourceOne.SourceID, testSummary("ok summary", "ok")); err != nil {
		t.Fatalf("save summary one: %v", err)
	}

	inserted, err = st.UpsertItem(ctx, testItem("gh-star:darron:test/two", "github_star", "https://github.com/test/two", now))
	if err != nil {
		t.Fatalf("upsert item two: %v", err)
	}
	sourceTwo, err := st.UpsertSourceLink(ctx, inserted.ItemID, modelSourceCandidate("src:github-two", "https://github.com/test/two", "github"))
	if err != nil {
		t.Fatalf("source two: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, sourceTwo.SourceID, testExtract("https://github.com/test/two", "README two", "github-api"), "hash-two"); err != nil {
		t.Fatalf("save extract two: %v", err)
	}
	if _, err := st.SaveSourceSummary(ctx, sourceTwo.SourceID, testSummary("", "error")); err != nil {
		t.Fatalf("save summary two: %v", err)
	}

	inserted, err = st.UpsertItem(ctx, testItem("gh-star:darron:test/three", "github_star", "https://github.com/test/three", now))
	if err != nil {
		t.Fatalf("upsert item three: %v", err)
	}
	sourceThree, err := st.UpsertSourceLink(ctx, inserted.ItemID, modelSourceCandidate("src:github-three", "https://github.com/test/three", "github"))
	if err != nil {
		t.Fatalf("source three: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, sourceThree.SourceID, testExtract("https://github.com/test/three", "README three", "github-api"), "hash-three"); err != nil {
		t.Fatalf("save extract three: %v", err)
	}

	buckets, err := st.CountSources(ctx, SourceCountFilter{
		SourceType:  "github",
		ExtractTool: "github-api",
	}, "summary-status")
	if err != nil {
		t.Fatalf("CountSources: %v", err)
	}
	if len(buckets) != 3 {
		t.Fatalf("expected 3 buckets, got %d", len(buckets))
	}
	want := map[string]int{"": 1, "error": 1, "ok": 1}
	for _, bucket := range buckets {
		if want[bucket.Key] != bucket.Count {
			t.Fatalf("unexpected bucket %+v", bucket)
		}
	}
}

func TestActivityReportsLatestWritesAndWindowCounts(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	itemOneTime := now.Add(-30 * time.Minute)
	insertTestItem(t, st, "x:old", "Old article", "Old body", itemOneTime)

	itemTwoTime := now.Add(-5 * time.Minute)
	insertTestItem(t, st, "x:new", "New article", "New body", itemTwoTime)

	upserted, err := st.UpsertItem(ctx, testItem("gh-star:darron:test/repo", "github_star", "https://github.com/test/repo", now.Add(-10*time.Minute)))
	if err != nil {
		t.Fatalf("upsert github item: %v", err)
	}
	link, err := st.UpsertSourceLink(ctx, upserted.ItemID, modelSourceCandidate("src:github-test", "https://github.com/test/repo", "github"))
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	extractedAt := now.Add(-4 * time.Minute)
	if _, err := st.SaveSourceExtraction(ctx, link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/repo",
		FinalURL:     "https://github.com/test/repo",
		Title:        "test/repo",
		Content:      "README content",
		Status:       "ok",
		FetchedAt:    extractedAt,
		Tool:         "github-api",
		ToolVersion:  "test-version",
	}, "source-hash"); err != nil {
		t.Fatalf("save extract: %v", err)
	}
	summarizedAt := now.Add(-2 * time.Minute)
	if _, err := st.SaveSourceSummary(ctx, link.SourceID, model.SummaryResult{
		Text:          "summary text",
		RawJSON:       `{"summary":"summary text"}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     summarizedAt,
		Tool:          "summarize",
		ToolVersion:   "test-1.0.0",
	}); err != nil {
		t.Fatalf("save summary: %v", err)
	}

	stats, err := st.Activity(ctx, now, 15*time.Minute)
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if stats.LatestItemUpdatedAt.Format(time.RFC3339) != itemTwoTime.Format(time.RFC3339) {
		t.Fatalf("unexpected latest item time: %v", stats.LatestItemUpdatedAt)
	}
	if stats.LatestSourceUpdatedAt.IsZero() {
		t.Fatalf("unexpected latest source time: %v", stats.LatestSourceUpdatedAt)
	}
	if stats.LatestSourceSummaryAt.Format(time.RFC3339) != summarizedAt.Format(time.RFC3339) {
		t.Fatalf("unexpected latest source summary time: %v", stats.LatestSourceSummaryAt)
	}
	if stats.ItemsUpdatedInWindow != 2 {
		t.Fatalf("expected 2 items updated in window, got %d", stats.ItemsUpdatedInWindow)
	}
	if stats.SourcesUpdatedInWindow != 1 {
		t.Fatalf("expected 1 source updated in window, got %d", stats.SourcesUpdatedInWindow)
	}
	if stats.SourcesSummarizedInWindow != 1 {
		t.Fatalf("expected 1 source summarized in window, got %d", stats.SourcesSummarizedInWindow)
	}
}

func TestBacklogReportsPendingWorkByStage(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	itemID := insertTestItem(t, st, "x:hydrate", "Hydrate article", "Body", now)
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET x_post_status = '', links_json = '["https://example.com/post"]', link_extract_synced_at = ''
		WHERE id = ?`, itemID); err != nil {
		t.Fatalf("update x item: %v", err)
	}

	upserted, err := st.UpsertItem(ctx, testItem("gh-star:darron:test/pending-extract", "github_star", "https://github.com/test/pending-extract", now))
	if err != nil {
		t.Fatalf("upsert pending extract item: %v", err)
	}
	link, err := st.UpsertSourceLink(ctx, upserted.ItemID, modelSourceCandidate("src:pending-extract", "https://github.com/test/pending-extract", "github"))
	if err != nil {
		t.Fatalf("pending extract source link: %v", err)
	}
	if link.SourceID == 0 {
		t.Fatal("expected pending extract source id")
	}

	upserted, err = st.UpsertItem(ctx, testItem("gh-star:darron:test/pending-summary", "github_star", "https://github.com/test/pending-summary", now))
	if err != nil {
		t.Fatalf("upsert pending summary item: %v", err)
	}
	link, err = st.UpsertSourceLink(ctx, upserted.ItemID, modelSourceCandidate("src:pending-summary", "https://github.com/test/pending-summary", "github"))
	if err != nil {
		t.Fatalf("pending summary source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/pending-summary",
		FinalURL:     "https://github.com/test/pending-summary",
		Title:        "pending summary",
		Content:      "README",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "test-version",
	}, "pending-summary-hash"); err != nil {
		t.Fatalf("save pending summary extract: %v", err)
	}

	backlog, err := st.Backlog(ctx, "dbrain-v1", "summarize", "test-1.0.0")
	if err != nil {
		t.Fatalf("Backlog: %v", err)
	}
	if backlog.Drained {
		t.Fatal("expected backlog not to be drained")
	}
	if backlog.XHydrationPending != 1 {
		t.Fatalf("expected 1 x hydration pending, got %d", backlog.XHydrationPending)
	}
	if backlog.LinkDiscoveryPending != 1 {
		t.Fatalf("expected 1 link discovery pending, got %d", backlog.LinkDiscoveryPending)
	}
	if backlog.SourceExtractionPending != 1 {
		t.Fatalf("expected 1 source extraction pending, got %d", backlog.SourceExtractionPending)
	}
	if backlog.SourceSummaryPending != 1 {
		t.Fatalf("expected 1 source summary pending, got %d", backlog.SourceSummaryPending)
	}
}

func TestBacklogSkipsRecentExtractErrorsDuringCooldown(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	recentItem, err := st.UpsertItem(ctx, testItem("gh-star:darron:test/recent-error", "github_star", "https://github.com/test/recent-error", now))
	if err != nil {
		t.Fatalf("upsert recent error item: %v", err)
	}
	recentLink, err := st.UpsertSourceLink(ctx, recentItem.ItemID, modelSourceCandidate("src:recent-error", "https://github.com/test/recent-error", "github"))
	if err != nil {
		t.Fatalf("recent error source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, recentLink.SourceID, model.ExtractResult{
		Status:      "error",
		Error:       "Unable to connect. Is the computer able to access the url?",
		Tool:        "summarize",
		ToolVersion: "test-version",
	}, ""); err != nil {
		t.Fatalf("save recent error extract: %v", err)
	}

	oldItem, err := st.UpsertItem(ctx, testItem("gh-star:darron:test/old-error", "github_star", "https://github.com/test/old-error", now))
	if err != nil {
		t.Fatalf("upsert old error item: %v", err)
	}
	oldLink, err := st.UpsertSourceLink(ctx, oldItem.ItemID, modelSourceCandidate("src:old-error", "https://github.com/test/old-error", "github"))
	if err != nil {
		t.Fatalf("old error source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, oldLink.SourceID, model.ExtractResult{
		Status:      "error",
		Error:       "Unable to connect. Is the computer able to access the url?",
		Tool:        "summarize",
		ToolVersion: "test-version",
	}, ""); err != nil {
		t.Fatalf("save old error extract: %v", err)
	}
	oldFailedAt := now.Add(-13 * time.Hour).Format(time.RFC3339)
	if _, err := st.db.ExecContext(ctx, `
		UPDATE sources
		SET extract_first_failed_at = ?, extract_last_failed_at = ?
		WHERE id = ?`,
		oldFailedAt,
		oldFailedAt,
		oldLink.SourceID,
	); err != nil {
		t.Fatalf("age old error timestamps: %v", err)
	}

	backlog, err := st.Backlog(ctx, "dbrain-v1", "summarize", "test-1.0.0")
	if err != nil {
		t.Fatalf("Backlog: %v", err)
	}
	if backlog.SourceExtractionPending != 1 {
		t.Fatalf("expected only the cooled-down error to count, got %d", backlog.SourceExtractionPending)
	}
	if len(backlog.SourceExtractionPendingByType) != 1 || backlog.SourceExtractionPendingByType[0].Key != "github" || backlog.SourceExtractionPendingByType[0].Count != 1 {
		t.Fatalf("unexpected extract backlog buckets: %+v", backlog.SourceExtractionPendingByType)
	}
}

func testItem(sourceKey string, sourceType string, url string, now time.Time) model.Item {
	return model.Item{
		SourceKey:    sourceKey,
		SourceType:   sourceType,
		ExternalID:   sourceKey,
		CanonicalURL: url,
		Title:        sourceKey,
		ContentHash:  sourceKey + "-hash",
		NotePath:     "items/test.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
}

func modelSourceCandidate(sourceKey string, url string, sourceType string) model.SourceCandidate {
	return model.SourceCandidate{
		SourceKey:     sourceKey,
		OriginalURL:   url,
		CanonicalURL:  url,
		NormalizedURL: url,
		SourceType:    sourceType,
		Domain:        "github.com",
		NotePath:      "sources/test.md",
	}
}

func testExtract(url string, content string, tool string) model.ExtractResult {
	return model.ExtractResult{
		CanonicalURL: url,
		FinalURL:     url,
		Title:        url,
		Content:      content,
		Status:       "ok",
		FetchedAt:    time.Now().UTC(),
		Tool:         tool,
		ToolVersion:  "test-version",
	}
}

func testSummary(text string, status string) model.SummaryResult {
	errText := ""
	if status == "error" {
		errText = "summary failed"
	}
	return model.SummaryResult{
		Text:          text,
		RawJSON:       `{"summary":"` + text + `"}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        status,
		Error:         errText,
		FetchedAt:     time.Now().UTC(),
		Tool:          "summarize",
		ToolVersion:   "test-1.0.0",
	}
}
