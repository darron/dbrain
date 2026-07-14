package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
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
	appleNote, err := st.UpsertItem(ctx, testItem("apple-note:pending-link", "apple_note", "apple-notes://default/pending-link", now))
	if err != nil {
		t.Fatalf("upsert apple note pending link item: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET links_json = '["https://example.com/from-note"]', link_extract_synced_at = ''
		WHERE id = ?`, appleNote.ItemID); err != nil {
		t.Fatalf("update apple note item links: %v", err)
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
	if backlog.LinkDiscoveryPending != 2 {
		t.Fatalf("expected 2 link discovery pending, got %d", backlog.LinkDiscoveryPending)
	}
	if backlog.SourceExtractionPending != 1 {
		t.Fatalf("expected 1 source extraction pending, got %d", backlog.SourceExtractionPending)
	}
	if backlog.SourceSummaryPending != 1 {
		t.Fatalf("expected 1 source summary pending, got %d", backlog.SourceSummaryPending)
	}
}

func TestBacklogAndPipelineCountQuotedPostRepairAsHydrationPending(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 22, 3, 4, 5, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:quoted-pending", "", "", now)
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET x_post_text = ?,
			x_post_status = ?,
			x_post_fetched_at = ?,
			x_post_json = ?
		WHERE id = ?`,
		"Oh this is delicious...",
		"ok_syndication",
		now.Format(time.RFC3339),
		`{
			"source":"syndication",
			"snapshot":{"id":"2030852374739198197","text":"Oh this is delicious..."},
			"raw":{
				"id_str":"2030852374739198197",
				"text":"Oh this is delicious...",
				"quoted_tweet":{
					"id_str":"2030838203549184127",
					"text":"Quoted context that should become a linked x quote item."
				}
			}
		}`,
		itemID,
	); err != nil {
		t.Fatalf("seed quoted hydration: %v", err)
	}

	backlog, err := st.Backlog(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Backlog: %v", err)
	}
	if backlog.XHydrationPending != 1 {
		t.Fatalf("expected 1 x hydration pending for quote repair, got %d", backlog.XHydrationPending)
	}

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}
	assertPipelineRowCounts(t, stats.Hydration, "x_bookmark", 1, 0, 1, 0, 0)
}

func TestBacklogAndPipelineCountQuotedSnapshotRepairAsHydrationPending(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 3, 4, 5, 0, time.UTC)

	item, err := st.UpsertItem(ctx, testItem("x:quoted-snapshot-pending", "x_quote", "https://x.com/example/status/2040448463540830705", now))
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET external_id = ?,
			x_post_text = ?,
			x_post_status = ?,
			x_post_fetched_at = ?,
			x_post_json = ?
		WHERE id = ?`,
		"2040448463540830705",
		"https://t.co/example",
		"ok_graphql",
		now.Format(time.RFC3339),
		`{
			"source":"graphql",
			"fetched_at":"2026-04-25T03:04:05Z",
			"snapshot":{"id":"2040448463540830705","text":"https://t.co/example"},
			"raw":{"__typename":"Tweet","rest_id":"2040448463540830705","legacy":{"full_text":"https://t.co/example"}}
		}`,
		item.ItemID,
	); err != nil {
		t.Fatalf("seed quoted snapshot hydration: %v", err)
	}

	backlog, err := st.Backlog(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Backlog: %v", err)
	}
	if backlog.XHydrationPending != 1 {
		t.Fatalf("expected 1 x hydration pending for quoted snapshot repair, got %d", backlog.XHydrationPending)
	}

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}
	assertPipelineRowCounts(t, stats.Hydration, "x_quote", 1, 0, 1, 0, 0)
}

func TestBacklogAndPipelineTreatBlockedSummariesAsBlockedNotPending(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	upserted, err := st.UpsertItem(ctx, testItem("gh-star:darron:test/blocked-summary", "github_star", "https://github.com/test/blocked-summary", now))
	if err != nil {
		t.Fatalf("upsert blocked summary item: %v", err)
	}
	link, err := st.UpsertSourceLink(ctx, upserted.ItemID, modelSourceCandidate("src:blocked-summary", "https://github.com/test/blocked-summary", "github"))
	if err != nil {
		t.Fatalf("blocked summary source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/blocked-summary",
		FinalURL:     "https://github.com/test/blocked-summary",
		Status:       "empty",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test-version",
	}, ""); err != nil {
		t.Fatalf("save blocked summary extract: %v", err)
	}
	if _, err := st.SaveSourceSummary(ctx, link.SourceID, model.SummaryResult{
		Status:        "blocked",
		Error:         "no extracted content available for summary",
		Model:         "openrouter/qwen/qwen3.5-27b",
		PromptVersion: "dbrain-v1",
		FetchedAt:     now,
		Tool:          "openrouter-direct",
		ToolVersion:   "openrouter-direct-v1",
	}); err != nil {
		t.Fatalf("save blocked summary: %v", err)
	}

	backlog, err := st.Backlog(ctx, "dbrain-v1", "openrouter-direct", "openrouter-direct-v1")
	if err != nil {
		t.Fatalf("Backlog: %v", err)
	}
	if backlog.SourceSummaryPending != 0 {
		t.Fatalf("expected blocked summary to not count as pending, got %d", backlog.SourceSummaryPending)
	}

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}
	assertPipelineRowCounts(t, stats.Summary, "github", 1, 0, 0, 1, 0)
}

func TestPipelineSummaryTreatsPendingExtractionAsPendingNotBlocked(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	upserted, err := st.UpsertItem(ctx, testItem("x:pending-x-article", "x_bookmark", "https://x.com/i/article/pending", now))
	if err != nil {
		t.Fatalf("upsert x article item: %v", err)
	}
	if _, err := st.UpsertSourceLink(ctx, upserted.ItemID, modelSourceCandidate("src:pending-x-article", "https://x.com/i/article/pending", "x_article")); err != nil {
		t.Fatalf("pending x article source link: %v", err)
	}

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}
	assertPipelineRowCounts(t, stats.Summary, "x_article", 1, 0, 1, 0, 0)
}

func TestPipelineTreatsShortLocalXArticlePreviewAsPendingExtraction(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	upserted, err := st.UpsertItem(ctx, testItem("x:short-x-article-preview", "x_quote", "https://x.com/i/article/preview", now))
	if err != nil {
		t.Fatalf("upsert x article item: %v", err)
	}
	link, err := st.UpsertSourceLink(ctx, upserted.ItemID, modelSourceCandidate("src:short-x-article-preview", "https://x.com/i/article/2040008490929037312", "x_article"))
	if err != nil {
		t.Fatalf("x article source link: %v", err)
	}

	content := strings.Repeat("x", 199)
	if _, err := st.SaveSourceExtraction(ctx, link.SourceID, model.ExtractResult{
		CanonicalURL: "https://x.com/i/article/2040008490929037312",
		FinalURL:     "https://x.com/example/article/2040008490929037312",
		Title:        "Example",
		SiteName:     "x.com",
		Content:      content,
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "x-hydration",
		ToolVersion:  "local-article-preview-cache",
	}, testHashText(content)); err != nil {
		t.Fatalf("save short preview extract: %v", err)
	}

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}
	assertPipelineRowCounts(t, stats.Extraction, "x_article", 1, 0, 1, 0, 0)
	assertPipelineRowCounts(t, stats.Summary, "x_article", 1, 0, 1, 0, 0)
}

func TestBacklogAndPipelineRequeueInvalidCurrentSummariesForRepair(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	emptyItem, err := st.UpsertItem(ctx, testItem("gh-star:darron:test/empty-summary-ok", "github_star", "https://github.com/test/empty-summary-ok", now))
	if err != nil {
		t.Fatalf("upsert empty summary item: %v", err)
	}
	emptyLink, err := st.UpsertSourceLink(ctx, emptyItem.ItemID, modelSourceCandidate("src:empty-summary-ok", "https://github.com/test/empty-summary-ok", "github"))
	if err != nil {
		t.Fatalf("empty summary source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, emptyLink.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/empty-summary-ok",
		FinalURL:     "https://github.com/test/empty-summary-ok",
		Status:       "empty",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test-version",
	}, ""); err != nil {
		t.Fatalf("save empty extract: %v", err)
	}
	if _, err := st.SaveSourceSummary(ctx, emptyLink.SourceID, model.SummaryResult{
		Text:          "metadata-only summary",
		Status:        "ok",
		Model:         "openrouter/qwen/qwen3.5-27b",
		PromptVersion: "dbrain-v1",
		FetchedAt:     now,
		Tool:          "openrouter-direct",
		ToolVersion:   "openrouter-direct-v1",
	}); err != nil {
		t.Fatalf("save empty extract summary: %v", err)
	}

	placeholderItem, err := st.UpsertItem(ctx, testItem("gh-star:darron:test/placeholder-summary-ok", "github_star", "https://github.com/test/placeholder-summary-ok", now))
	if err != nil {
		t.Fatalf("upsert placeholder summary item: %v", err)
	}
	placeholderLink, err := st.UpsertSourceLink(ctx, placeholderItem.ItemID, modelSourceCandidate("src:placeholder-summary-ok", "https://github.com/test/placeholder-summary-ok", "github"))
	if err != nil {
		t.Fatalf("placeholder summary source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, placeholderLink.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/placeholder-summary-ok",
		FinalURL:     "https://github.com/test/placeholder-summary-ok",
		Content:      "Redirecting to latest/...",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test-version",
	}, "redirect-hash"); err != nil {
		t.Fatalf("save placeholder extract: %v", err)
	}
	if _, err := st.SaveSourceSummary(ctx, placeholderLink.SourceID, model.SummaryResult{
		Text:          "placeholder summary",
		Status:        "ok",
		Model:         "openrouter/qwen/qwen3.5-27b",
		PromptVersion: "dbrain-v1",
		FetchedAt:     now,
		Tool:          "openrouter-direct",
		ToolVersion:   "openrouter-direct-v1",
	}); err != nil {
		t.Fatalf("save placeholder extract summary: %v", err)
	}

	backlog, err := st.Backlog(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Backlog: %v", err)
	}
	if backlog.SourceSummaryPending != 2 {
		t.Fatalf("expected 2 source summaries pending for repair, got %d", backlog.SourceSummaryPending)
	}

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}
	assertPipelineRowCounts(t, stats.Summary, "github", 2, 0, 2, 0, 0)
}

func TestBacklogAndPipelineKeepSubstantiveSignupTeaserCurrent(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	item, err := st.UpsertItem(ctx, testItem("gh-star:darron:test/substantive-signup-teaser", "github_star", "https://github.com/test/substantive-signup-teaser", now))
	if err != nil {
		t.Fatalf("upsert teaser item: %v", err)
	}
	link, err := st.UpsertSourceLink(ctx, item.ItemID, modelSourceCandidate("src:substantive-signup-teaser", "https://example.com/moltbook-like", "github"))
	if err != nil {
		t.Fatalf("teaser source link: %v", err)
	}

	content := "A Social Network for AI Agents Where AI agents share, discuss, and upvote. Humans welcome to observe. Send Your AI Agent to Moltbook. They sign up and send you a claim link. AI Agents Live Activity. Build for Agents. Let AI agents authenticate with your app using their Moltbook identity."

	if _, err := st.SaveSourceExtraction(ctx, link.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/moltbook-like",
		FinalURL:     "https://example.com/moltbook-like",
		Content:      content,
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test-version",
	}, testHashText(content)); err != nil {
		t.Fatalf("save teaser extract: %v", err)
	}
	if _, err := st.SaveSourceSummary(ctx, link.SourceID, model.SummaryResult{
		Text:          "valid summary",
		Status:        "ok",
		Model:         "openrouter/qwen/qwen3.5-27b",
		PromptVersion: "dbrain-v1",
		FetchedAt:     now,
		Tool:          "openrouter-direct",
		ToolVersion:   "openrouter-direct-v1",
	}); err != nil {
		t.Fatalf("save teaser summary: %v", err)
	}

	backlog, err := st.Backlog(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Backlog: %v", err)
	}
	if backlog.SourceSummaryPending != 0 {
		t.Fatalf("expected no source summaries pending, got %d", backlog.SourceSummaryPending)
	}

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}
	assertPipelineRowCounts(t, stats.Summary, "github", 1, 1, 0, 0, 0)
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

func TestPipelineXMediaTranscriptionClassifiesTerminalRetryAndUnknown(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	insertVideoCandidate := func(sourceKey string) int64 {
		t.Helper()

		itemResult, err := st.UpsertItem(ctx, model.Item{
			SourceKey:    sourceKey,
			SourceType:   "x_bookmark",
			ExternalID:   sourceKey,
			CanonicalURL: "https://x.com/example/status/" + sourceKey,
			Title:        sourceKey,
			ContentHash:  sourceKey + "-hash",
			LinksJSON:    "[]",
			NotePath:     "items/x/test.md",
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		})
		if err != nil {
			t.Fatalf("UpsertItem %s: %v", sourceKey, err)
		}

		hydration := model.XHydration{
			FullText:  "hello",
			Language:  "en",
			Status:    "ok_graphql",
			FetchedAt: now,
			APIJSON: `{
				"snapshot":{
					"media_objects":[
						{"type":"video","url":"https://video.twimg.com/ext/` + sourceKey + `.mp4","expanded_url":"https://x.com/example/status/` + sourceKey + `/video/1","width":1280,"height":720}
					]
				}
			}`,
		}
		if _, err := st.SaveXHydration(ctx, itemResult.ItemID, hydration); err != nil {
			t.Fatalf("SaveXHydration %s: %v", sourceKey, err)
		}

		refs, err := st.ListItemMediaRefs(ctx, itemResult.ItemID)
		if err != nil {
			t.Fatalf("ListItemMediaRefs %s: %v", sourceKey, err)
		}
		if len(refs) != 1 {
			t.Fatalf("expected one media ref for %s, got %d", sourceKey, len(refs))
		}
		if _, err := st.SaveMediaDownload(ctx, refs[0].MediaAssetID, model.MediaDownloadResult{
			LocalPath:    "media/x/video/" + sourceKey + ".mp4",
			ContentHash:  sourceKey + "-download-hash",
			Status:       "downloaded",
			DownloadedAt: now,
		}); err != nil {
			t.Fatalf("SaveMediaDownload %s: %v", sourceKey, err)
		}

		return itemResult.ItemID
	}

	currentID := insertVideoCandidate("x-media-current")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET article_title = 'X Media Transcript',
			article_text = 'materialized transcript',
			x_media_transcript_status = 'ok',
			x_media_transcript_at = ?
		WHERE id = ?`,
		now.Format(time.RFC3339),
		currentID,
	); err != nil {
		t.Fatalf("seed current transcript: %v", err)
	}
	if err := st.SaveXMediaTranscriptionState(ctx, currentID, model.XMediaTranscriptStatusOK, "", now); err != nil {
		t.Fatalf("save current transcript mirror: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET article_title = '',
			article_text = '',
			x_media_transcript_status = '',
			x_media_transcript_at = ''
		WHERE id = ?`,
		currentID,
	); err != nil {
		t.Fatalf("clear current transcript compatibility columns: %v", err)
	}

	blockedID := insertVideoCandidate("x-media-blocked")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET x_media_transcript_status = 'ok',
			x_media_transcript_at = ?
		WHERE id = ?`,
		now.Format(time.RFC3339),
		blockedID,
	); err != nil {
		t.Fatalf("seed blocked transcript: %v", err)
	}

	for _, status := range []string{
		model.XMediaTranscriptStatusNoAudio,
		model.XMediaTranscriptStatusNoise,
		model.XMediaTranscriptStatusTooShort,
		model.XMediaTranscriptStatusEmpty,
	} {
		itemID := insertVideoCandidate("x-media-terminal-" + status)
		if err := st.SaveXMediaTranscriptionState(ctx, itemID, status, "terminal", now); err != nil {
			t.Fatalf("seed terminal transcript %s: %v", status, err)
		}
	}

	dueErrorID := insertVideoCandidate("x-media-error-due")
	if err := st.SaveXMediaTranscriptionState(ctx, dueErrorID, model.XMediaTranscriptStatusError, "retry", now.Add(-25*time.Hour)); err != nil {
		t.Fatalf("seed due transcript error: %v", err)
	}
	youngErrorID := insertVideoCandidate("x-media-error-young")
	if err := st.SaveXMediaTranscriptionState(ctx, youngErrorID, model.XMediaTranscriptStatusError, "cooldown", now); err != nil {
		t.Fatalf("seed young transcript error: %v", err)
	}
	invalidID := insertVideoCandidate("x-media-invalid")
	if err := st.SaveXMediaTranscriptionState(ctx, invalidID, "legacy_bogus", "unknown", now); err != nil {
		t.Fatalf("seed invalid transcript status: %v", err)
	}

	prunedPendingID := insertVideoCandidate("x-media-pruned-pending")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE media_assets
		SET local_pruned_at = ?,
			archive_status = 'archived'
		WHERE id IN (
			SELECT media_asset_id
			FROM item_media_links
			WHERE item_id = ?
		)`,
		now.Format(time.RFC3339),
		prunedPendingID,
	); err != nil {
		t.Fatalf("seed pruned pending media: %v", err)
	}

	items, err := st.ListItemsForXMediaTranscription(ctx, 100, false)
	if err != nil {
		t.Fatalf("ListItemsForXMediaTranscription: %v", err)
	}
	if len(items) != 1 || items[0].SourceKey != "x-media-error-due" {
		t.Fatalf("expected only due transcription error candidate, got %+v", items)
	}

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}

	row := pipelineRowByKind(t, stats.Transcription, "x_media_transcript")
	if row.Total != 10 || row.Current != 1 || row.Pending != 1 || row.Blocked != 3 || row.Terminal != 4 || row.Failed != 0 || row.Unknown != 1 || !row.PartitionValid {
		t.Fatalf("unexpected transcription partitions: %+v", row)
	}
}

func TestPipelineXMediaSummaryClassifiesPendingBlockedAndFailed(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	insertTranscriptItem := func(sourceKey string) int64 {
		t.Helper()

		itemResult, err := st.UpsertItem(ctx, model.Item{
			SourceKey:    sourceKey,
			SourceType:   "x_bookmark",
			ExternalID:   sourceKey,
			CanonicalURL: "https://x.com/example/status/" + sourceKey,
			Title:        sourceKey,
			ContentHash:  sourceKey + "-hash",
			LinksJSON:    "[]",
			NotePath:     "items/x/test.md",
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		})
		if err != nil {
			t.Fatalf("UpsertItem %s: %v", sourceKey, err)
		}
		if _, err := st.db.ExecContext(ctx, `
			UPDATE items
			SET article_title = 'X Media Transcript',
				article_text = 'materialized transcript',
				x_media_transcript_status = 'ok',
				x_media_transcript_at = ?
			WHERE id = ?`,
			now.Format(time.RFC3339),
			itemResult.ItemID,
		); err != nil {
			t.Fatalf("seed transcript %s: %v", sourceKey, err)
		}
		if err := st.SaveXMediaTranscriptionState(ctx, itemResult.ItemID, model.XMediaTranscriptStatusOK, "", now); err != nil {
			t.Fatalf("save transcript mirror %s: %v", sourceKey, err)
		}
		if _, err := st.db.ExecContext(ctx, `
			UPDATE items
			SET article_title = '',
				article_text = '',
				x_media_transcript_status = '',
				x_media_transcript_at = ''
			WHERE id = ?`,
			itemResult.ItemID,
		); err != nil {
			t.Fatalf("clear transcript compatibility columns %s: %v", sourceKey, err)
		}
		return itemResult.ItemID
	}

	currentID := insertTranscriptItem("x-media-summary-current")
	if _, err := st.SaveItemSummary(ctx, currentID, model.SummaryResult{
		Text:      "saved summary",
		Status:    model.ItemSummaryStatusOK,
		FetchedAt: now,
	}, "x-media-summary-current-input"); err != nil {
		t.Fatalf("save current x media summary mirror: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET summary_status = '',
			summary_text = '',
			summarized_at = ''
		WHERE id = ?`,
		currentID,
	); err != nil {
		t.Fatalf("clear current x media summary compatibility columns: %v", err)
	}

	_ = insertTranscriptItem("x-media-summary-pending")

	blockedID := insertTranscriptItem("x-media-summary-blocked")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET summary_status = 'blocked',
			summary_error = 'context limit'
		WHERE id = ?`,
		blockedID,
	); err != nil {
		t.Fatalf("seed blocked x media summary: %v", err)
	}

	failedID := insertTranscriptItem("x-media-summary-failed")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET summary_status = 'fatal',
			summary_error = 'unexpected parser failure'
		WHERE id = ?`,
		failedID,
	); err != nil {
		t.Fatalf("seed failed x media summary: %v", err)
	}

	items, err := st.ListItemsForXMediaSummary(ctx, 100, false)
	if err != nil {
		t.Fatalf("ListItemsForXMediaSummary: %v", err)
	}
	if len(items) != 1 || items[0].SourceKey != "x-media-summary-pending" || items[0].ArticleText == "" {
		t.Fatalf("expected only pending x media summary candidate with mirrored transcript text, got %+v", items)
	}

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}

	xSummaryRow := pipelineRowByKind(t, stats.Summary, "x_media_summary")
	if xSummaryRow.Failed != 0 || xSummaryRow.Unknown != 1 || !xSummaryRow.PartitionValid {
		t.Fatalf("invalid x media summary status must be unknown: %+v", xSummaryRow)
	}
}

func TestPipelineAppleNoteExtractionAndSummaryClassifyItemCoverage(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	insertAppleNote := func(sourceKey string, body string, attachmentText string) int64 {
		t.Helper()

		item := testItem(sourceKey, "apple_note", "apple-notes://default/"+sourceKey, now)
		item.Text = body
		item.ArticleTitle = "Apple Notes Attachment Text"
		item.ArticleText = attachmentText
		result, err := st.UpsertItem(ctx, item)
		if err != nil {
			t.Fatalf("UpsertItem %s: %v", sourceKey, err)
		}
		return result.ItemID
	}

	currentID := insertAppleNote("apple-note-summary-current", "current body", "")
	if _, err := st.SaveItemSummary(ctx, currentID, model.SummaryResult{
		Text:      "saved note summary",
		Status:    model.ItemSummaryStatusOK,
		FetchedAt: now,
	}, "apple-note-summary-current-input"); err != nil {
		t.Fatalf("save current apple note summary mirror: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET summary_status = '',
			summary_text = '',
			summarized_at = ''
		WHERE id = ?`,
		currentID,
	); err != nil {
		t.Fatalf("clear current apple note summary compatibility columns: %v", err)
	}

	_ = insertAppleNote("apple-note-summary-pending", "pending body", "")

	blockedID := insertAppleNote("apple-note-summary-blocked", "", "attachment-only note")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET summary_status = 'blocked',
			summary_error = 'context limit'
		WHERE id = ?`,
		blockedID,
	); err != nil {
		t.Fatalf("seed blocked apple note summary: %v", err)
	}

	failedID := insertAppleNote("apple-note-summary-failed", "failed body", "")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET summary_status = 'fatal',
			summary_error = 'unexpected parser failure'
		WHERE id = ?`,
		failedID,
	); err != nil {
		t.Fatalf("seed failed apple note summary: %v", err)
	}

	_ = insertAppleNote("apple-note-summary-empty", "", "")

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}

	assertPipelineRowCounts(t, stats.Extraction, "apple_note", 5, 4, 0, 1, 0)
	appleSummaryRow := pipelineRowByKind(t, stats.Summary, "apple_note")
	if appleSummaryRow.Failed != 0 || appleSummaryRow.Unknown != 1 || !appleSummaryRow.PartitionValid {
		t.Fatalf("invalid Apple Notes summary status must be unknown: %+v", appleSummaryRow)
	}
}

func TestPipelineSafariTabExtractionClassifiesItemMaterialization(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	insertSafariTab := func(sourceKey string, body string, canonicalURL string) {
		t.Helper()

		item := testItem(sourceKey, "safari_tab", canonicalURL, now)
		item.Text = body
		if _, err := st.UpsertItem(ctx, item); err != nil {
			t.Fatalf("UpsertItem %s: %v", sourceKey, err)
		}
	}

	insertSafariTab("safari-tab:current", "Safari tab captured from iCloud Tabs.", "https://example.com/current")
	insertSafariTab("safari-tab:missing-text", "", "https://example.com/missing-text")
	insertSafariTab("safari-tab:missing-url", "Safari tab captured from iCloud Tabs.", "")

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}

	assertPipelineRowCounts(t, stats.Extraction, "safari_tab", 3, 1, 0, 2, 0)
}

func TestAppendPipelineStageRowRecomputesAggregate(t *testing.T) {
	t.Parallel()

	rows := []PipelineStageRow{
		{Kind: "ALL", Total: 2, Current: 1, Failed: 1, PercentCurrent: 50},
		{Kind: "web", Total: 2, Current: 1, Failed: 1, PercentCurrent: 50},
	}
	extra := PipelineStageRow{Kind: "apple_note", Total: 1, Current: 1, PercentCurrent: 100}

	got := appendPipelineStageRow(rows, extra)
	assertPipelineRowCounts(t, got, "ALL", 3, 2, 0, 0, 1)
	assertPipelineRowCounts(t, got, "apple_note", 1, 1, 0, 0, 0)
}

func TestPipelineXMediaTranscriptionCountsPrunedCurrentItems(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	itemResult, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x-media-pruned-current",
		SourceType:   "x_bookmark",
		ExternalID:   "x-media-pruned-current",
		CanonicalURL: "https://x.com/example/status/x-media-pruned-current",
		Title:        "x-media-pruned-current",
		ContentHash:  "x-media-pruned-current-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/test.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	hydration := model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"snapshot":{
				"media_objects":[
					{"type":"video","url":"https://video.twimg.com/ext_tw_video/x-media-pruned-current.mp4","expanded_url":"https://x.com/example/status/x-media-pruned-current/video/1","width":1280,"height":720}
				]
			}
		}`,
	}
	if _, err := st.SaveXHydration(ctx, itemResult.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	refs, err := st.ListItemMediaRefs(ctx, itemResult.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %d", len(refs))
	}
	if _, err := st.SaveMediaDownload(ctx, refs[0].MediaAssetID, model.MediaDownloadResult{
		LocalPath:    "media/x/video/x-media-pruned-current.mp4",
		ContentHash:  "x-media-pruned-current-download",
		Status:       "downloaded",
		DownloadedAt: now,
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET article_title = 'X Media Transcript',
			article_text = 'materialized transcript',
			x_media_transcript_status = 'ok',
			x_media_transcript_at = ?
		WHERE id = ?`,
		now.Format(time.RFC3339),
		itemResult.ItemID,
	); err != nil {
		t.Fatalf("seed current transcript: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE media_assets
		SET local_pruned_at = ?
		WHERE id = ?`,
		now.Format(time.RFC3339),
		refs[0].MediaAssetID,
	); err != nil {
		t.Fatalf("prune media asset: %v", err)
	}

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}

	assertPipelineRowCounts(t, stats.Transcription, "x_media_transcript", 1, 1, 0, 0, 0)
}

func TestPipelineXPhotoOCRClassifiesPendingBlockedAndFailed(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	insertPhotoCandidate := func(sourceKey string) int64 {
		t.Helper()

		itemResult, err := st.UpsertItem(ctx, model.Item{
			SourceKey:    sourceKey,
			SourceType:   "x_bookmark",
			ExternalID:   sourceKey,
			CanonicalURL: "https://x.com/example/status/" + sourceKey,
			Title:        sourceKey,
			ContentHash:  sourceKey + "-hash",
			LinksJSON:    "[]",
			NotePath:     "items/x/test.md",
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		})
		if err != nil {
			t.Fatalf("UpsertItem %s: %v", sourceKey, err)
		}

		hydration := model.XHydration{
			FullText:  "hello",
			Language:  "en",
			Status:    "ok_graphql",
			FetchedAt: now,
			APIJSON: `{
				"snapshot":{
					"media_objects":[
						{"type":"photo","url":"https://pbs.twimg.com/media/` + sourceKey + `.png","expanded_url":"https://x.com/example/status/` + sourceKey + `/photo/1","width":1280,"height":720}
					]
				}
			}`,
		}
		if _, err := st.SaveXHydration(ctx, itemResult.ItemID, hydration); err != nil {
			t.Fatalf("SaveXHydration %s: %v", sourceKey, err)
		}

		refs, err := st.ListItemMediaRefs(ctx, itemResult.ItemID)
		if err != nil {
			t.Fatalf("ListItemMediaRefs %s: %v", sourceKey, err)
		}
		if len(refs) != 1 {
			t.Fatalf("expected one media ref for %s, got %d", sourceKey, len(refs))
		}
		if _, err := st.SaveMediaDownload(ctx, refs[0].MediaAssetID, model.MediaDownloadResult{
			LocalPath:    "media/x/photo/" + sourceKey + ".png",
			ContentHash:  sourceKey + "-download-hash",
			Status:       "downloaded",
			DownloadedAt: now,
		}); err != nil {
			t.Fatalf("SaveMediaDownload %s: %v", sourceKey, err)
		}

		return itemResult.ItemID
	}

	currentID := insertPhotoCandidate("x-photo-ocr-current")
	if _, err := st.SaveItemOCR(ctx, currentID, model.OCRResult{
		Text:      "saved ocr text",
		Status:    model.ItemOCRStatusOK,
		FetchedAt: now,
	}, "x-photo-ocr-current-input"); err != nil {
		t.Fatalf("save current ocr mirror: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET ocr_status = '',
			ocr_text = '',
			ocr_at = ''
		WHERE id = ?`,
		currentID,
	); err != nil {
		t.Fatalf("clear current ocr compatibility columns: %v", err)
	}

	_ = insertPhotoCandidate("x-photo-ocr-pending")

	blockedID := insertPhotoCandidate("x-photo-ocr-blocked")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET ocr_status = 'blocked',
			ocr_error = 'policy hold'
		WHERE id = ?`,
		blockedID,
	); err != nil {
		t.Fatalf("seed blocked ocr: %v", err)
	}

	failedID := insertPhotoCandidate("x-photo-ocr-failed")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET ocr_status = 'too_large',
			ocr_error = 'image too large'
		WHERE id = ?`,
		failedID,
	); err != nil {
		t.Fatalf("seed failed ocr: %v", err)
	}

	items, err := st.ListItemsForXPhotoOCR(ctx, 100, false)
	if err != nil {
		t.Fatalf("ListItemsForXPhotoOCR: %v", err)
	}
	if len(items) != 1 || items[0].SourceKey != "x-photo-ocr-pending" {
		t.Fatalf("expected only pending x photo OCR candidate, got %+v", items)
	}

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}

	ocrRow := pipelineRowByKind(t, stats.OCR, "x_photo_ocr")
	if ocrRow.Total != 4 || ocrRow.Current != 1 || ocrRow.Pending != 1 || ocrRow.Blocked != 1 || ocrRow.Failed != 0 || ocrRow.Unknown != 1 || !ocrRow.PartitionValid {
		t.Fatalf("unexpected OCR partitions: %+v", ocrRow)
	}
}

func TestPipelineXPhotoOCRCountsPrunedCurrentItems(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	itemResult, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x-photo-pruned-current",
		SourceType:   "x_bookmark",
		ExternalID:   "x-photo-pruned-current",
		CanonicalURL: "https://x.com/example/status/x-photo-pruned-current",
		Title:        "x-photo-pruned-current",
		ContentHash:  "x-photo-pruned-current-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/test.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	hydration := model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"snapshot":{
				"media_objects":[
					{"type":"photo","url":"https://pbs.twimg.com/media/x-photo-pruned-current.png","expanded_url":"https://x.com/example/status/x-photo-pruned-current/photo/1","width":1280,"height":720}
				]
			}
		}`,
	}
	if _, err := st.SaveXHydration(ctx, itemResult.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	refs, err := st.ListItemMediaRefs(ctx, itemResult.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %d", len(refs))
	}
	if _, err := st.SaveMediaDownload(ctx, refs[0].MediaAssetID, model.MediaDownloadResult{
		LocalPath:    "media/x/photo/x-photo-pruned-current.png",
		ContentHash:  "x-photo-pruned-current-download",
		Status:       "downloaded",
		DownloadedAt: now,
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET ocr_status = 'ok',
			ocr_text = 'saved ocr text',
			ocr_at = ?
		WHERE id = ?`,
		now.Format(time.RFC3339),
		itemResult.ItemID,
	); err != nil {
		t.Fatalf("seed current ocr: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE media_assets
		SET local_pruned_at = ?
		WHERE id = ?`,
		now.Format(time.RFC3339),
		refs[0].MediaAssetID,
	); err != nil {
		t.Fatalf("prune media asset: %v", err)
	}

	stats, err := st.Pipeline(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}

	assertPipelineRowCounts(t, stats.OCR, "x_photo_ocr", 1, 1, 0, 0, 0)
}

func TestSourceActivityFeedReturnsRecentSuccessesAndFailures(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	summarySuccessID := insertTestSource(t, st, "src:summary-success", "https://example.com/summary-success")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE sources
		SET title = ?, summary_status = ?, summarized_at = ?, updated_at = ?
		WHERE id = ?`,
		"Summary Success",
		"ok",
		now.Add(-1*time.Minute).Format(time.RFC3339),
		now.Add(-1*time.Minute).Format(time.RFC3339),
		summarySuccessID,
	); err != nil {
		t.Fatalf("update summary success source: %v", err)
	}

	extractSuccessID := insertTestSource(t, st, "src:extract-success", "https://example.com/extract-success")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE sources
		SET title = ?, extract_status = ?, extracted_at = ?, updated_at = ?
		WHERE id = ?`,
		"Extract Success",
		"ok",
		now.Add(-2*time.Minute).Format(time.RFC3339),
		now.Add(-2*time.Minute).Format(time.RFC3339),
		extractSuccessID,
	); err != nil {
		t.Fatalf("update extract success source: %v", err)
	}

	summaryFailureID := insertTestSource(t, st, "src:summary-failure", "https://example.com/summary-failure")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE sources
		SET title = ?, summary_status = ?, summary_error = ?, updated_at = ?
		WHERE id = ?`,
		"Summary Failure",
		"error",
		"model timed out",
		now.Add(-30*time.Second).Format(time.RFC3339),
		summaryFailureID,
	); err != nil {
		t.Fatalf("update summary failure source: %v", err)
	}

	extractFailureID := insertTestSource(t, st, "src:extract-failure", "https://example.com/extract-failure")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE sources
		SET title = ?, extract_status = ?, extract_error = ?, extract_last_failed_at = ?, updated_at = ?
		WHERE id = ?`,
		"Extract Failure",
		"error",
		"Unable to connect. Is the computer able to access the url?",
		now.Add(-90*time.Second).Format(time.RFC3339),
		now.Add(-90*time.Second).Format(time.RFC3339),
		extractFailureID,
	); err != nil {
		t.Fatalf("update extract failure source: %v", err)
	}

	feed, err := st.SourceActivityFeed(ctx, 2)
	if err != nil {
		t.Fatalf("SourceActivityFeed: %v", err)
	}

	if len(feed.RecentSuccesses) != 2 {
		t.Fatalf("expected 2 recent successes, got %d", len(feed.RecentSuccesses))
	}
	if feed.RecentSuccesses[0].SourceKey != "src:summary-success" || feed.RecentSuccesses[0].EventKind != "summary_ok" {
		t.Fatalf("unexpected first success event: %+v", feed.RecentSuccesses[0])
	}
	if feed.RecentSuccesses[1].SourceKey != "src:extract-success" || feed.RecentSuccesses[1].EventKind != "extract_ok" {
		t.Fatalf("unexpected second success event: %+v", feed.RecentSuccesses[1])
	}

	if len(feed.RecentFailures) != 2 {
		t.Fatalf("expected 2 recent failures, got %d", len(feed.RecentFailures))
	}
	if feed.RecentFailures[0].SourceKey != "src:summary-failure" || feed.RecentFailures[0].EventKind != "summary_error" {
		t.Fatalf("unexpected first failure event: %+v", feed.RecentFailures[0])
	}
	if feed.RecentFailures[1].SourceKey != "src:extract-failure" || feed.RecentFailures[1].EventKind != "extract_error" {
		t.Fatalf("unexpected second failure event: %+v", feed.RecentFailures[1])
	}
	if feed.RecentFailures[1].Message == "" {
		t.Fatalf("expected failure message to be preserved, got %+v", feed.RecentFailures[1])
	}
	if feed.RecentFailures[1].FailureKind != "extract_error" && feed.RecentFailures[1].FailureKind != "connectivity" {
		t.Fatalf("expected extract failure kind to be preserved, got %+v", feed.RecentFailures[1])
	}
	if len(feed.FailureKinds) != 2 {
		t.Fatalf("expected 2 failure kind buckets, got %+v", feed.FailureKinds)
	}
	if len(feed.FailureStatuses) != 1 || feed.FailureStatuses[0].Key != "error" || feed.FailureStatuses[0].Count != 2 {
		t.Fatalf("unexpected failure status buckets: %+v", feed.FailureStatuses)
	}
	if len(feed.FailureDomains) != 1 || feed.FailureDomains[0].Key != "example.com" || feed.FailureDomains[0].Count != 2 {
		t.Fatalf("unexpected failure domain buckets: %+v", feed.FailureDomains)
	}
	if len(feed.FailureTable) != 2 || feed.FailureTableTotal != 2 {
		t.Fatalf("unexpected failure table: total=%d rows=%+v", feed.FailureTableTotal, feed.FailureTable)
	}
	if feed.FailureTableSort != "newest" {
		t.Fatalf("expected default failure table sort newest, got %q", feed.FailureTableSort)
	}
	if len(feed.Trend) == 0 || feed.TrendBucket == "" {
		t.Fatalf("expected trend points and trend bucket, got bucket=%q trend=%+v", feed.TrendBucket, feed.Trend)
	}
	if feed.Window != "24h0m0s" {
		t.Fatalf("expected default window 24h0m0s, got %q", feed.Window)
	}
}

func TestSourceActivityFeedSupportsFiltering(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	webSourceID := insertTestSource(t, st, "src:web-success", "https://web.example.com/post")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE sources
		SET source_type = ?, domain = ?, title = ?, extract_status = ?, extracted_at = ?, updated_at = ?
		WHERE id = ?`,
		"web",
		"web.example.com",
		"Web Success",
		"ok",
		now.Add(-2*time.Minute).Format(time.RFC3339),
		now.Add(-2*time.Minute).Format(time.RFC3339),
		webSourceID,
	); err != nil {
		t.Fatalf("update web success source: %v", err)
	}

	githubSourceID := insertTestSource(t, st, "src:github-failure", "https://github.com/test/repo")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE sources
		SET source_type = ?, domain = ?, title = ?, updated_at = ?
		WHERE id = ?`,
		"github",
		"github.com",
		"GitHub Failure",
		now.Add(-1*time.Minute).Format(time.RFC3339),
		githubSourceID,
	); err != nil {
		t.Fatalf("update github failure source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, githubSourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/repo",
		FinalURL:     "https://github.com/test/repo",
		Status:       "error",
		Error:        "Unable to connect. Is the computer able to access the url?",
		FetchedAt:    now.Add(-1 * time.Minute),
		Tool:         "summarize",
		ToolVersion:  "test-version",
	}, ""); err != nil {
		t.Fatalf("save github failure source extraction: %v", err)
	}

	githubSourceIDTwo := insertTestSource(t, st, "src:github-failure-two", "https://github.com/test/repo-two")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE sources
		SET source_type = ?, domain = ?, title = ?, updated_at = ?
		WHERE id = ?`,
		"github",
		"github.com",
		"GitHub Failure Two",
		now.Add(-30*time.Minute).Format(time.RFC3339),
		githubSourceIDTwo,
	); err != nil {
		t.Fatalf("update second github failure source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, githubSourceIDTwo, model.ExtractResult{
		CanonicalURL: "https://github.com/test/repo-two",
		FinalURL:     "https://github.com/test/repo-two",
		Status:       "error",
		Error:        "Unable to connect. Is the computer able to access the url?",
		FetchedAt:    now.Add(-30 * time.Minute),
		Tool:         "summarize",
		ToolVersion:  "test-version",
	}, ""); err != nil {
		t.Fatalf("save second github failure source extraction: %v", err)
	}

	feed, err := st.SourceActivityFeedFiltered(ctx, SourceActivityFilter{
		Limit:      10,
		SourceType: "github",
		Domain:     "github.com",
		Status:     "error",
		Message:    "connect",
		Window:     2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("SourceActivityFeedFiltered: %v", err)
	}

	if len(feed.RecentSuccesses) != 0 {
		t.Fatalf("expected no github successes, got %+v", feed.RecentSuccesses)
	}
	if len(feed.RecentFailures) != 2 {
		t.Fatalf("expected 2 github failures, got %d", len(feed.RecentFailures))
	}
	if feed.RecentFailures[0].SourceKey != "src:github-failure" {
		t.Fatalf("unexpected filtered failure event: %+v", feed.RecentFailures[0])
	}
	if feed.RecentFailures[0].Domain != "github.com" {
		t.Fatalf("expected domain github.com, got %+v", feed.RecentFailures[0])
	}
	if feed.RecentFailures[0].FailureKind != "connectivity" {
		t.Fatalf("expected connectivity failure kind, got %+v", feed.RecentFailures[0])
	}
	if len(feed.FailureHotspots) != 1 {
		t.Fatalf("expected 1 failure hotspot, got %+v", feed.FailureHotspots)
	}
	if feed.FailureHotspots[0].Domain != "github.com" || feed.FailureHotspots[0].Count != 2 {
		t.Fatalf("unexpected failure hotspot: %+v", feed.FailureHotspots[0])
	}
	if feed.FailureHotspots[0].FailureKind != "connectivity" {
		t.Fatalf("expected connectivity hotspot, got %+v", feed.FailureHotspots[0])
	}
	if len(feed.FailureKinds) != 1 || feed.FailureKinds[0].Key != "connectivity" || feed.FailureKinds[0].Count != 2 {
		t.Fatalf("unexpected filtered failure kinds: %+v", feed.FailureKinds)
	}
	if len(feed.FailureStatuses) != 1 || feed.FailureStatuses[0].Key != "error" || feed.FailureStatuses[0].Count != 2 {
		t.Fatalf("unexpected filtered failure statuses: %+v", feed.FailureStatuses)
	}
	if len(feed.FailureDomains) != 1 || feed.FailureDomains[0].Key != "github.com" || feed.FailureDomains[0].Count != 2 {
		t.Fatalf("unexpected filtered failure domains: %+v", feed.FailureDomains)
	}
	if len(feed.FailureTable) != 2 || feed.FailureTableTotal != 2 {
		t.Fatalf("unexpected filtered failure table: total=%d rows=%+v", feed.FailureTableTotal, feed.FailureTable)
	}
	if len(feed.Trend) == 0 {
		t.Fatalf("expected filtered trend points, got %+v", feed.Trend)
	}
}

func TestSourceActivityFeedFailureTableSupportsSortingAndPaging(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	oldestID := insertTestSource(t, st, "src:alpha-oldest", "https://alpha.example.com/oldest")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE sources
		SET source_type = ?, domain = ?, title = ?, updated_at = ?
		WHERE id = ?`,
		"web",
		"alpha.example.com",
		"Alpha Oldest",
		now.Add(-90*time.Minute).Format(time.RFC3339),
		oldestID,
	); err != nil {
		t.Fatalf("update oldest failure source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, oldestID, model.ExtractResult{
		CanonicalURL: "https://alpha.example.com/oldest",
		FinalURL:     "https://alpha.example.com/oldest",
		Status:       "error",
		Error:        "Unable to connect. Is the computer able to access the url?",
		FetchedAt:    now.Add(-90 * time.Minute),
		Tool:         "summarize",
		ToolVersion:  "test-version",
	}, ""); err != nil {
		t.Fatalf("save oldest failure extraction: %v", err)
	}

	newerID := insertTestSource(t, st, "src:beta-newer", "https://beta.example.com/newer")
	if _, err := st.db.ExecContext(ctx, `
		UPDATE sources
		SET source_type = ?, domain = ?, title = ?, updated_at = ?
		WHERE id = ?`,
		"web",
		"beta.example.com",
		"Beta Newer",
		now.Add(-30*time.Minute).Format(time.RFC3339),
		newerID,
	); err != nil {
		t.Fatalf("update newer failure source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, newerID, model.ExtractResult{
		CanonicalURL: "https://beta.example.com/newer",
		FinalURL:     "https://beta.example.com/newer",
		Status:       "error",
		Error:        "Unable to connect. Is the computer able to access the url?",
		FetchedAt:    now.Add(-30 * time.Minute),
		Tool:         "summarize",
		ToolVersion:  "test-version",
	}, ""); err != nil {
		t.Fatalf("save newer failure extraction: %v", err)
	}

	feed, err := st.SourceActivityFeedFiltered(ctx, SourceActivityFilter{
		Limit:         1,
		FailureSort:   "oldest",
		FailureOffset: 1,
		Window:        4 * time.Hour,
	})
	if err != nil {
		t.Fatalf("SourceActivityFeedFiltered oldest/offset: %v", err)
	}
	if feed.FailureTableTotal != 2 {
		t.Fatalf("expected 2 total failure rows, got %d", feed.FailureTableTotal)
	}
	if len(feed.FailureTable) != 1 || feed.FailureTable[0].SourceKey != "src:beta-newer" {
		t.Fatalf("unexpected oldest/offset failure table rows: %+v", feed.FailureTable)
	}

	domainFeed, err := st.SourceActivityFeedFiltered(ctx, SourceActivityFilter{
		Limit:       2,
		FailureSort: "domain",
		Window:      4 * time.Hour,
	})
	if err != nil {
		t.Fatalf("SourceActivityFeedFiltered domain sort: %v", err)
	}
	if len(domainFeed.FailureTable) != 2 {
		t.Fatalf("expected 2 failure rows for domain sort, got %+v", domainFeed.FailureTable)
	}
	if domainFeed.FailureTable[0].Domain != "alpha.example.com" || domainFeed.FailureTable[1].Domain != "beta.example.com" {
		t.Fatalf("unexpected domain sort order: %+v", domainFeed.FailureTable)
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

func assertPipelineRowCounts(t *testing.T, rows []PipelineStageRow, kind string, total int, current int, pending int, blocked int, failed int) {
	t.Helper()

	for _, row := range rows {
		if row.Kind != kind {
			continue
		}
		if row.Total != total || row.Current != current || row.Pending != pending || row.Blocked != blocked || row.Failed != failed {
			t.Fatalf("unexpected pipeline row for %s: %+v", kind, row)
		}
		return
	}
	t.Fatalf("missing pipeline row for %s in %+v", kind, rows)
}

func TestPipelinePartitionsClassifiesUnexplainedRemainderAsUnknown(t *testing.T) {
	t.Parallel()

	row := PipelineStageRow{Kind: "legacy", Total: 3, Current: 1, Pending: 1}
	finalizePipelineStageRow(&row)
	if row.Failed != 0 || row.Unknown != 1 || !row.PartitionValid {
		t.Fatalf("unexplained remainder must be unknown, got %+v", row)
	}

	overlap := PipelineStageRow{Kind: "overlap", Total: 1, Current: 1, Pending: 1}
	finalizePipelineStageRow(&overlap)
	if overlap.PartitionValid {
		t.Fatalf("overlapping partitions must be invalid: %+v", overlap)
	}
}

func pipelineRowByKind(t *testing.T, rows []PipelineStageRow, kind string) PipelineStageRow {
	t.Helper()
	for _, row := range rows {
		if row.Kind == kind {
			return row
		}
	}
	t.Fatalf("missing pipeline row %q in %+v", kind, rows)
	return PipelineStageRow{}
}
