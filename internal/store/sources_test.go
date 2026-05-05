package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestListSourcesForEnrichmentIgnoresExtractToolVersionMismatchWhenSummaryCurrent(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := st.db.ExecContext(ctx, `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, title,
			extracted_text, extract_status, extracted_at, extract_tool, extract_tool_version,
			summary_text, summary_status, summary_model, summary_content_hash, summary_prompt_version, summary_tool, summary_tool_version, summarized_at,
			content_hash, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:test-current",
		"https://example.com/post",
		"https://example.com/post",
		"web",
		"example.com",
		"Example",
		"cached content",
		"ok",
		now,
		"summarize",
		"0.10.0",
		"cached summary",
		"ok",
		"cli/test/model",
		"hash-1",
		"dbrain-v1",
		"summarize",
		"0.13.0",
		now,
		"hash-1",
		"sources/web/example.md",
		now,
		now,
	)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}

	sources, err := st.ListSourcesForEnrichment(ctx, 10, false, true, "dbrain-v1", "summarize", "0.13.0")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("expected no queued sources, got %d", len(sources))
	}
}

func TestUpsertSourceQueuesManualSourceForEnrichment(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	candidate := model.SourceCandidate{
		OriginalURL:   "https://example.com/manual",
		CanonicalURL:  "https://example.com/manual",
		NormalizedURL: "https://example.com/manual",
		SourceType:    "web",
		Domain:        "example.com",
		SourceKey:     "src:manual",
		NotePath:      "sources/web/example-manual.md",
	}

	result, err := st.UpsertSource(ctx, candidate)
	if err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	if !result.SourceCreated || result.SourceID == 0 {
		t.Fatalf("unexpected upsert result %+v", result)
	}

	pending, err := st.ListSourcesForEnrichment(ctx, 10, false, true, "dbrain-v1", "summarize", "0.1.0")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(pending) != 1 || pending[0].SourceKey != candidate.SourceKey {
		t.Fatalf("expected manual source pending, got %+v", pending)
	}

	again, err := st.UpsertSource(ctx, candidate)
	if err != nil {
		t.Fatalf("UpsertSource again: %v", err)
	}
	if again.SourceCreated || again.SourceID != result.SourceID {
		t.Fatalf("expected existing source result, got %+v", again)
	}
}

func TestListItemsForLinkDiscoveryIncludesAppleNotes(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	item := testItem("apple-note:test-note", "apple_note", "apple-notes://default/test-note", now)
	item.LinksJSON = `["https://example.com/from-note"]`
	if _, err := st.UpsertItem(ctx, item); err != nil {
		t.Fatalf("upsert apple note item: %v", err)
	}

	items, err := st.ListItemsForLinkDiscovery(ctx, 10, false)
	if err != nil {
		t.Fatalf("ListItemsForLinkDiscovery: %v", err)
	}
	if len(items) != 1 || items[0].SourceKey != item.SourceKey {
		t.Fatalf("expected Apple Note link discovery candidate, got %+v", items)
	}
}

func TestResetSourceEnrichmentByDomainClearsCurrentState(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	insert := func(sourceKey string, canonicalURL string, domain string) int64 {
		t.Helper()
		result, err := st.db.ExecContext(ctx, `
			INSERT INTO sources (
				source_key, canonical_url, normalized_url, source_type, domain, title, description, site_name,
				extracted_text, extract_json, extract_status, extract_error, extract_failure_kind, extract_failure_count,
				extract_first_failed_at, extract_last_failed_at, extracted_at, extract_tool, extract_tool_version,
				summary_text, summary_json, summary_status, summary_error, summary_model, summary_content_hash,
				summary_prompt_version, summary_tool, summary_tool_version, summarized_at, content_hash, note_path,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sourceKey,
			canonicalURL,
			canonicalURL,
			"web",
			domain,
			"Title",
			"Description",
			"Site",
			"Extracted text",
			`{"extract":true}`,
			"ok",
			"old error",
			"http_5xx",
			2,
			now,
			now,
			now,
			"summarize",
			"test-version",
			"Summary",
			`{"summary":true}`,
			"ok",
			"summary error",
			"model",
			"hash",
			"dbrain-v1",
			"summarize",
			"summary-version",
			now,
			"hash",
			"sources/web/"+sourceKey+".md",
			now,
			now,
		)
		if err != nil {
			t.Fatalf("insert source %s: %v", sourceKey, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("source id: %v", err)
		}
		return id
	}

	canadaID := insert("src:canada", "https://canada.ca/en/news", "canada.ca")
	subID := insert("src:sub-canada", "https://foo.canada.ca/en/news", "foo.canada.ca")
	otherID := insert("src:other", "https://example.com/news", "example.com")

	dryRun, err := st.ResetSourceEnrichment(ctx, ResetSourceEnrichmentOptions{Domains: []string{"canada.ca"}, DryRun: true})
	if err != nil {
		t.Fatalf("dry run reset source enrichment: %v", err)
	}
	if dryRun.Matched != 2 || dryRun.Reset != 0 || !dryRun.DryRun {
		t.Fatalf("unexpected dry-run stats: %+v", dryRun)
	}

	stats, err := st.ResetSourceEnrichment(ctx, ResetSourceEnrichmentOptions{Domains: []string{"canada.ca"}})
	if err != nil {
		t.Fatalf("reset source enrichment: %v", err)
	}
	if stats.Matched != 2 || stats.Reset != 2 || stats.DryRun {
		t.Fatalf("unexpected reset stats: %+v", stats)
	}

	for _, sourceID := range []int64{canadaID, subID} {
		source, err := st.GetSourceByID(ctx, sourceID)
		if err != nil {
			t.Fatalf("get reset source: %v", err)
		}
		if source.ExtractStatus != "" || source.ExtractedText != "" || source.SummaryStatus != "" || source.SummaryText != "" || source.ContentHash != "" {
			t.Fatalf("expected reset source enrichment state, got %+v", source)
		}
	}

	other, err := st.GetSourceByID(ctx, otherID)
	if err != nil {
		t.Fatalf("get other source: %v", err)
	}
	if other.ExtractStatus != "ok" || other.SummaryStatus != "ok" || other.ExtractedText == "" || other.SummaryText == "" {
		t.Fatalf("expected unrelated source unchanged, got %+v", other)
	}
}

func TestResetSourceEnrichmentFiltersByTypeStatusKindAndFailureCount(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	insert := func(sourceKey string, sourceType string, extractStatus string, summaryStatus string, failureKind string, failureCount int) int64 {
		t.Helper()
		result, err := st.db.ExecContext(ctx, `
			INSERT INTO sources (
				source_key, canonical_url, normalized_url, source_type, domain, note_path,
				extracted_text, extract_status, extract_error, extract_failure_kind, extract_failure_count, extract_first_failed_at, extract_last_failed_at,
				summary_text, summary_status, content_hash,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sourceKey,
			"https://x.com/i/article/"+sourceKey,
			"https://x.com/i/article/"+sourceKey,
			sourceType,
			"x.com",
			"sources/"+sourceType+"/"+sourceKey+".md",
			"old extract",
			extractStatus,
			"old error",
			failureKind,
			failureCount,
			now,
			now,
			"old summary",
			summaryStatus,
			"hash",
			now,
			now,
		)
		if err != nil {
			t.Fatalf("insert source %s: %v", sourceKey, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("source id %s: %v", sourceKey, err)
		}
		return id
	}

	matchingID := insert("matching", "x_article", "dead", "error", "x_article_shell", 3)
	wrongTypeID := insert("wrong-type", "web", "dead", "error", "x_article_shell", 3)
	wrongKindID := insert("wrong-kind", "x_article", "dead", "error", "http_access_denied", 3)
	tooFewID := insert("too-few", "x_article", "dead", "error", "x_article_shell", 2)

	dryRun, err := st.ResetSourceEnrichment(ctx, ResetSourceEnrichmentOptions{
		SourceTypes:     []string{"x_article"},
		ExtractStatuses: []string{"dead"},
		SummaryStatuses: []string{"error"},
		FailureKinds:    []string{"x_article_shell"},
		MinFailures:     3,
		DryRun:          true,
	})
	if err != nil {
		t.Fatalf("dry run filtered reset: %v", err)
	}
	if dryRun.Matched != 1 || dryRun.Reset != 0 || !dryRun.DryRun {
		t.Fatalf("unexpected dry-run stats: %+v", dryRun)
	}

	stats, err := st.ResetSourceEnrichment(ctx, ResetSourceEnrichmentOptions{
		SourceTypes:     []string{"x_article"},
		ExtractStatuses: []string{"dead"},
		SummaryStatuses: []string{"error"},
		FailureKinds:    []string{"x_article_shell"},
		MinFailures:     3,
	})
	if err != nil {
		t.Fatalf("filtered reset: %v", err)
	}
	if stats.Matched != 1 || stats.Reset != 1 {
		t.Fatalf("unexpected reset stats: %+v", stats)
	}

	matching, err := st.GetSourceByID(ctx, matchingID)
	if err != nil {
		t.Fatalf("get matching source: %v", err)
	}
	if matching.ExtractStatus != "" || matching.SummaryStatus != "" || matching.ExtractFailureCount != 0 {
		t.Fatalf("expected matching source reset, got %+v", matching)
	}
	for _, sourceID := range []int64{wrongTypeID, wrongKindID, tooFewID} {
		source, err := st.GetSourceByID(ctx, sourceID)
		if err != nil {
			t.Fatalf("get nonmatching source: %v", err)
		}
		if source.ExtractStatus == "" || source.SummaryStatus == "" || source.ExtractFailureCount == 0 {
			t.Fatalf("expected nonmatching source unchanged, got %+v", source)
		}
	}
}

func TestResetSourceEnrichmentCanMarkLinkedXArticlesForRehydration(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	insertSource := func(sourceKey string, sourceType string) int64 {
		t.Helper()
		result, err := st.db.ExecContext(ctx, `
			INSERT INTO sources (
				source_key, canonical_url, normalized_url, source_type, domain, note_path,
				extracted_text, extract_status, extract_error, extract_failure_kind, extract_failure_count,
				summary_text, summary_status, content_hash,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sourceKey,
			"https://x.com/i/article/"+sourceKey,
			"https://x.com/i/article/"+sourceKey,
			sourceType,
			"x.com",
			"sources/"+sourceType+"/"+sourceKey+".md",
			"old extract",
			"dead",
			"x article shell",
			"x_article_shell",
			3,
			"old summary",
			"error",
			"hash",
			now,
			now,
		)
		if err != nil {
			t.Fatalf("insert source %s: %v", sourceKey, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("source id %s: %v", sourceKey, err)
		}
		return id
	}

	insertItem := func(sourceKey string, sourceType string, articleTitle string, articleText string) int64 {
		t.Helper()
		result, err := st.db.ExecContext(ctx, `
			INSERT INTO items (
				source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
				published_at, saved_at, synced_at, language, text, article_title, article_text,
				primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
				like_count, repost_count, reply_count, quote_count, bookmark_count,
				content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at,
				x_post_text, x_post_lang, x_post_json, x_post_fetched_at, x_post_status, x_post_error, link_extract_synced_at,
				summary_text, summary_json, summary_status, summary_model, summary_input_hash, summarized_at
			) VALUES (?, ?, ?, ?, ?, 'author', 'Author', '', '', '', 'en', 'tweet text', ?, ?,
				'', '', '[]', '', '', '', '', 0, 0, 0, 0, 0,
				?, ?, '{}', ?, ?, ?,
				'old hydrated post', 'en', ?, ?, 'ok_graphql', '', ?,
				'old summary', '{}', 'ok', 'model', 'input-hash', ?)`,
			sourceKey,
			sourceType,
			sourceKey,
			"https://x.com/example/status/"+sourceKey,
			sourceKey,
			articleTitle,
			articleText,
			sourceKey+"-hash",
			"items/x/"+sourceKey+".md",
			now,
			now,
			now,
			`{"raw":{"article":{"title":"Old Article","preview_text":"Old preview text from stale hydration cache.","rest_id":"123"}}}`,
			now,
			now,
			now,
		)
		if err != nil {
			t.Fatalf("insert item %s: %v", sourceKey, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("item id %s: %v", sourceKey, err)
		}
		return id
	}

	matchingSourceID := insertSource("matching", "x_article")
	otherSourceID := insertSource("other", "web")
	childItemID := insertItem("x-child", "x_quote", "X Media Transcript", "raw transcript should be preserved")
	parentItemID := insertItem("x-parent", "x_bookmark", "", "")
	otherItemID := insertItem("x-other", "x_bookmark", "", "")

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO item_source_links (item_id, source_id, original_url, created_at)
		VALUES (?, ?, ?, ?)`,
		childItemID,
		matchingSourceID,
		"https://x.com/i/article/matching",
		now,
	); err != nil {
		t.Fatalf("insert matching item source link: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO item_source_links (item_id, source_id, original_url, created_at)
		VALUES (?, ?, ?, ?)`,
		otherItemID,
		otherSourceID,
		"https://example.com/other",
		now,
	); err != nil {
		t.Fatalf("insert unrelated item source link: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO item_item_links (parent_item_id, child_item_id, link_kind, created_at, updated_at)
		VALUES (?, ?, 'quoted_post', ?, ?)`,
		parentItemID,
		childItemID,
		now,
		now,
	); err != nil {
		t.Fatalf("insert quote link: %v", err)
	}

	dryRun, err := st.ResetSourceEnrichment(ctx, ResetSourceEnrichmentOptions{
		SourceTypes:        []string{"x_article"},
		ExtractStatuses:    []string{"dead"},
		FailureKinds:       []string{"x_article_shell"},
		RehydrateXArticles: true,
		DryRun:             true,
	})
	if err != nil {
		t.Fatalf("dry run reset with x article rehydrate: %v", err)
	}
	if dryRun.Matched != 1 || dryRun.XItemsMatched != 2 || dryRun.Reset != 0 || dryRun.XItemsReset != 0 {
		t.Fatalf("unexpected dry-run stats: %+v", dryRun)
	}

	stats, err := st.ResetSourceEnrichment(ctx, ResetSourceEnrichmentOptions{
		SourceTypes:        []string{"x_article"},
		ExtractStatuses:    []string{"dead"},
		FailureKinds:       []string{"x_article_shell"},
		RehydrateXArticles: true,
	})
	if err != nil {
		t.Fatalf("reset with x article rehydrate: %v", err)
	}
	if stats.Matched != 1 || stats.Reset != 1 || stats.XItemsMatched != 2 || stats.XItemsReset != 2 {
		t.Fatalf("unexpected reset stats: %+v", stats)
	}

	source, err := st.GetSourceByID(ctx, matchingSourceID)
	if err != nil {
		t.Fatalf("get reset source: %v", err)
	}
	if source.ExtractStatus != "" || source.SummaryStatus != "" || source.ExtractFailureCount != 0 {
		t.Fatalf("expected matching source reset, got %+v", source)
	}

	for _, itemID := range []int64{childItemID, parentItemID} {
		item, err := st.GetItemByID(ctx, itemID)
		if err != nil {
			t.Fatalf("get reset item %d: %v", itemID, err)
		}
		if item.XPostStatus != "" || item.XPostJSON != "" || item.XPostText != "" || !item.LinkExtractSyncedAt.IsZero() {
			t.Fatalf("expected x hydration fields reset for item %d, got %+v", itemID, item)
		}
		if item.SummaryStatus != "" || item.SummaryText != "" {
			t.Fatalf("expected item summary invalidated for item %d, got %+v", itemID, item)
		}
	}

	child, err := st.GetItemByID(ctx, childItemID)
	if err != nil {
		t.Fatalf("get child item: %v", err)
	}
	if child.ArticleTitle != "X Media Transcript" || child.ArticleText != "raw transcript should be preserved" {
		t.Fatalf("expected raw transcript article fields preserved, got title=%q text=%q", child.ArticleTitle, child.ArticleText)
	}

	other, err := st.GetItemByID(ctx, otherItemID)
	if err != nil {
		t.Fatalf("get unrelated item: %v", err)
	}
	if other.XPostStatus == "" || other.XPostJSON == "" || other.SummaryStatus == "" {
		t.Fatalf("expected unrelated item unchanged, got %+v", other)
	}

	if result, ok, err := st.GetPreferredLocalSourceExtract(ctx, matchingSourceID); err != nil {
		t.Fatalf("local extract after reset: %v", err)
	} else if ok {
		t.Fatalf("expected reset source to have no stale local extract, got %+v", result)
	}
}

func TestListSourcesForEnrichmentQueuesSummaryToolVersionMismatch(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := st.db.ExecContext(ctx, `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, title,
			extracted_text, extract_status, extracted_at,
			summary_text, summary_status, summary_model, summary_content_hash, summary_prompt_version, summary_tool, summary_tool_version, summarized_at,
			content_hash, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:test-stale-summary",
		"https://example.com/post",
		"https://example.com/post",
		"web",
		"example.com",
		"Example",
		"cached content",
		"ok",
		now,
		"cached summary",
		"ok",
		"cli/test/model",
		"hash-1",
		"dbrain-v1",
		"summarize",
		"0.10.0",
		now,
		"hash-1",
		"sources/web/example.md",
		now,
		now,
	)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}

	sources, err := st.ListSourcesForEnrichment(ctx, 10, false, true, "dbrain-v1", "summarize", "0.13.0")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 queued source, got %d", len(sources))
	}
	if sources[0].SourceKey != "src:test-stale-summary" {
		t.Fatalf("unexpected source queued: %s", sources[0].SourceKey)
	}
}

func TestListSourcesForEnrichmentQueuesEmptyExtractCurrentSummaryForRepair(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := st.db.ExecContext(ctx, `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, title,
			extracted_text, extract_status, extracted_at,
			summary_text, summary_status, summary_model, summary_content_hash, summary_prompt_version, summary_tool, summary_tool_version, summarized_at,
			content_hash, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:test-empty-summary-repair",
		"https://example.com/empty",
		"https://example.com/empty",
		"web",
		"example.com",
		"Example",
		"",
		"empty",
		now,
		"metadata-only summary",
		"ok",
		"cli/test/model",
		"",
		"dbrain-v1",
		"summarize",
		"0.13.0",
		now,
		"",
		"sources/web/example.md",
		now,
		now,
	)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}

	sources, err := st.ListSourcesForEnrichment(ctx, 10, false, true, "dbrain-v1", "summarize", "0.13.0")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 queued source, got %d", len(sources))
	}
	if sources[0].SourceKey != "src:test-empty-summary-repair" {
		t.Fatalf("unexpected source queued: %s", sources[0].SourceKey)
	}
}

func TestListSourcesForEnrichmentQueuesPlaceholderSummaryForRepair(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := st.db.ExecContext(ctx, `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, title,
			extracted_text, extract_status, extracted_at,
			summary_text, summary_status, summary_model, summary_content_hash, summary_prompt_version, summary_tool, summary_tool_version, summarized_at,
			content_hash, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:test-placeholder-summary-repair",
		"https://example.com/redirect",
		"https://example.com/redirect",
		"web",
		"example.com",
		"Example",
		"Redirecting to latest/...",
		"ok",
		now,
		"placeholder summary",
		"ok",
		"cli/test/model",
		"hash-1",
		"dbrain-v1",
		"summarize",
		"0.13.0",
		now,
		"hash-1",
		"sources/web/example.md",
		now,
		now,
	)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}

	sources, err := st.ListSourcesForEnrichment(ctx, 10, false, true, "dbrain-v1", "summarize", "0.13.0")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 queued source, got %d", len(sources))
	}
	if sources[0].SourceKey != "src:test-placeholder-summary-repair" {
		t.Fatalf("unexpected source queued: %s", sources[0].SourceKey)
	}
}

func TestListSourcesForEnrichmentQueuesShortWaybackSummaryForRepair(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	content := "The method to Mark Carney's madness\n\nBy Max Fawcett\n\nOpinion\n\nPolitics\n\nShare this article"

	_, err := st.db.ExecContext(ctx, `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, title,
			extracted_text, extract_status, extracted_at, extract_tool, extract_tool_version,
			summary_text, summary_status, summary_model, summary_content_hash, summary_prompt_version, summary_tool, summary_tool_version, summarized_at,
			content_hash, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:test-short-wayback-summary-repair",
		"https://example.com/title-only",
		"https://example.com/title-only",
		"web",
		"example.com",
		"Example",
		content,
		"ok",
		now,
		"wayback",
		"wayback-v1",
		"old plausible summary",
		"ok",
		"cli/test/model",
		testHashText(content),
		"dbrain-v1",
		"summarize",
		"0.13.0",
		now,
		testHashText(content),
		"sources/web/example.md",
		now,
		now,
	)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}

	sources, err := st.ListSourcesForEnrichment(ctx, 10, false, true, "dbrain-v1", "summarize", "0.13.0")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 queued source, got %d", len(sources))
	}
	if sources[0].SourceKey != "src:test-short-wayback-summary-repair" {
		t.Fatalf("unexpected source queued: %s", sources[0].SourceKey)
	}
}

func TestListSourcesForEnrichmentDoesNotQueueSubstantiveSignupTeaser(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	content := "A Social Network for AI Agents Where AI agents share, discuss, and upvote. Humans welcome to observe. Send Your AI Agent to Moltbook. They sign up and send you a claim link. AI Agents Live Activity. Build for Agents. Let AI agents authenticate with your app using their Moltbook identity."

	_, err := st.db.ExecContext(ctx, `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, title,
			extracted_text, extract_status, extracted_at,
			summary_text, summary_status, summary_model, summary_content_hash, summary_prompt_version, summary_tool, summary_tool_version, summarized_at,
			content_hash, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:test-substantive-signup-teaser",
		"https://example.com/moltbook-like",
		"https://example.com/moltbook-like",
		"web",
		"example.com",
		"Example",
		content,
		"ok",
		now,
		"valid summary",
		"ok",
		"cli/test/model",
		testHashText(content),
		"dbrain-v1",
		"summarize",
		"0.13.0",
		now,
		testHashText(content),
		"sources/web/example.md",
		now,
		now,
	)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}

	sources, err := st.ListSourcesForEnrichment(ctx, 10, false, true, "dbrain-v1", "summarize", "0.13.0")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("expected no queued sources, got %d", len(sources))
	}
}

func TestListSourcesForEnrichmentQueuesShortLocalXArticlePreviewForRepair(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	content := strings.Repeat("x", 199)
	_, err := st.db.ExecContext(ctx, `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, title,
			extracted_text, extract_status, extracted_at, extract_tool, extract_tool_version,
			content_hash, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:test-short-x-article-preview",
		"https://x.com/i/article/2040008490929037312",
		"https://x.com/i/article/2040008490929037312",
		"x_article",
		"x.com",
		"Example",
		content,
		"ok",
		now,
		"x-hydration",
		"local-article-preview-cache",
		testHashText(content),
		"sources/x/example.md",
		now,
		now,
	)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}

	sources, err := st.ListSourcesForEnrichment(ctx, 10, false, true, "dbrain-v1", "summarize", "0.13.0")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 queued source, got %d", len(sources))
	}
	if sources[0].SourceKey != "src:test-short-x-article-preview" {
		t.Fatalf("unexpected source queued: %s", sources[0].SourceKey)
	}
}

func TestGetPreferredLocalSourceExtractReturnsLongestCachedArticle(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	sourceInsert, err := st.db.ExecContext(ctx, `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:test-local",
		"https://example.com/post",
		"https://example.com/post",
		"web",
		"example.com",
		"sources/web/example.md",
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}
	sourceID, err := sourceInsert.LastInsertId()
	if err != nil {
		t.Fatalf("source id: %v", err)
	}

	itemOneID := insertTestItem(t, st, "x:item-one", "Short title", "short text", now.Add(-2*time.Hour))
	itemTwoID := insertTestItem(t, st, "x:item-two", "Long title", "this is the longer cached article body", now)

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO item_source_links (item_id, source_id, original_url, created_at)
		VALUES (?, ?, ?, ?), (?, ?, ?, ?)`,
		itemOneID, sourceID, "https://example.com/post", now.Format(time.RFC3339),
		itemTwoID, sourceID, "https://example.com/post", now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert source links: %v", err)
	}

	result, ok, err := st.GetPreferredLocalSourceExtract(ctx, sourceID)
	if err != nil {
		t.Fatalf("GetPreferredLocalSourceExtract: %v", err)
	}
	if !ok {
		t.Fatal("expected local extract to be found")
	}
	if result.Title != "Long title" {
		t.Fatalf("expected longest cached title, got %q", result.Title)
	}
	if result.Content != "this is the longer cached article body" {
		t.Fatalf("unexpected local content: %q", result.Content)
	}
	if result.Tool != "item-cache" || result.ToolVersion != "local-item-cache" {
		t.Fatalf("unexpected tool metadata: %s %s", result.Tool, result.ToolVersion)
	}
}

func TestGetPreferredLocalSourceExtractIgnoresAppleNoteAttachmentText(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	sourceInsert, err := st.db.ExecContext(ctx, `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:note-link",
		"https://example.com/from-note",
		"https://example.com/from-note",
		"web",
		"example.com",
		"sources/web/from-note.md",
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}
	sourceID, err := sourceInsert.LastInsertId()
	if err != nil {
		t.Fatalf("source id: %v", err)
	}
	item := testItem("apple-note:test-note", "apple_note", "apple-notes://default/test-note", now)
	item.ArticleTitle = "Apple Notes Attachment Text"
	item.ArticleText = "attachment text belongs to the note, not every linked URL"
	upserted, err := st.UpsertItem(ctx, item)
	if err != nil {
		t.Fatalf("upsert apple note item: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO item_source_links (item_id, source_id, original_url, created_at)
		VALUES (?, ?, ?, ?)`,
		upserted.ItemID, sourceID, "https://example.com/from-note", now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert source link: %v", err)
	}

	result, ok, err := st.GetPreferredLocalSourceExtract(ctx, sourceID)
	if err != nil {
		t.Fatalf("GetPreferredLocalSourceExtract: %v", err)
	}
	if ok {
		t.Fatalf("expected no local source extract from Apple Note attachment text, got %+v", result)
	}
}

func TestGetPreferredLocalSourceExtractUsesXArticlePreviewFromHydratedItem(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	articleID := "2047376179414421957"

	sourceInsert, err := st.db.ExecContext(ctx, `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:test-x-article-preview",
		"https://x.com/i/article/"+articleID,
		"https://x.com/i/article/"+articleID,
		"x_article",
		"x.com",
		"sources/x/article-preview.md",
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert x article source: %v", err)
	}
	sourceID, err := sourceInsert.LastInsertId()
	if err != nil {
		t.Fatalf("source id: %v", err)
	}

	itemInsert, err := st.db.ExecContext(ctx, `
		INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
			published_at, saved_at, synced_at, language, text, article_title, article_text,
			primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
			like_count, repost_count, reply_count, quote_count, bookmark_count,
			content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at,
			x_post_text, x_post_lang, x_post_json, x_post_fetched_at, x_post_status, x_post_error, link_extract_synced_at
		) VALUES (?, ?, ?, ?, ?, ?, '', '', '', '', '', '', '', '', '', '', '[]', '', '', '', '', 0, 0, 0, 0, 0, ?, '', '{}', ?, ?, ?, '', '', ?, ?, 'ok', '', '')`,
		"x:test-article-preview",
		"x_bookmark",
		"x:test-article-preview",
		"https://x.com/mattshumer_/status/2047377079352877534",
		"linked x item",
		"mattshumer_",
		"x:test-article-preview-hash",
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		`{"raw":{"article":{"title":"Synthetic Minds in the Loop","preview_text":"Preview body from hydrated x article metadata.","rest_id":"2047376179414421957"}}}`,
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert linked x item: %v", err)
	}
	itemID, err := itemInsert.LastInsertId()
	if err != nil {
		t.Fatalf("item id: %v", err)
	}

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO item_source_links (item_id, source_id, original_url, created_at)
		VALUES (?, ?, ?, ?)`,
		itemID,
		sourceID,
		"https://x.com/i/article/"+articleID,
		now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert source link: %v", err)
	}

	result, ok, err := st.GetPreferredLocalSourceExtract(ctx, sourceID)
	if err != nil {
		t.Fatalf("GetPreferredLocalSourceExtract: %v", err)
	}
	if !ok {
		t.Fatal("expected local x article preview extract to be found")
	}
	if result.FinalURL != "https://x.com/mattshumer_/article/"+articleID {
		t.Fatalf("unexpected final url: %q", result.FinalURL)
	}
	if result.Title != "Synthetic Minds in the Loop" {
		t.Fatalf("unexpected title: %q", result.Title)
	}
	if result.Content != "Preview body from hydrated x article metadata." {
		t.Fatalf("unexpected preview content: %q", result.Content)
	}
	if result.Tool != "x-hydration" || result.ToolVersion != "local-article-preview-cache" {
		t.Fatalf("unexpected tool metadata: %s %s", result.Tool, result.ToolVersion)
	}
}

func TestGetPreferredLocalSourceExtractUsesFullXArticleBodyFromHydratedItem(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	articleID := "2028710814601908224"

	sourceInsert, err := st.db.ExecContext(ctx, `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:test-x-article-body",
		"https://x.com/i/article/"+articleID,
		"https://x.com/i/article/"+articleID,
		"x_article",
		"x.com",
		"sources/x/article-body.md",
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert x article source: %v", err)
	}
	sourceID, err := sourceInsert.LastInsertId()
	if err != nil {
		t.Fatalf("source id: %v", err)
	}

	itemInsert, err := st.db.ExecContext(ctx, `
		INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
			published_at, saved_at, synced_at, language, text, article_title, article_text,
			primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
			like_count, repost_count, reply_count, quote_count, bookmark_count,
			content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at,
			x_post_text, x_post_lang, x_post_json, x_post_fetched_at, x_post_status, x_post_error, link_extract_synced_at
		) VALUES (?, ?, ?, ?, ?, ?, '', '', '', '', '', '', '', '', '', '', '[]', '', '', '', '', 0, 0, 0, 0, 0, ?, '', '{}', ?, ?, ?, '', '', ?, ?, 'ok_graphql', '', '')`,
		"x:test-article-body",
		"x_bookmark",
		"2028894099483578872",
		"https://x.com/HamelHusain/status/2028894099483578872",
		"linked x item",
		"HamelHusain",
		"x:test-article-body-hash",
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		`{
			"raw": {
				"data": {
					"tweetResult": {
						"result": {
							"article": {
								"article_results": {
									"result": {
										"title": "Evals Skills for Coding Agents",
										"rest_id": "2028710814601908224",
										"preview_text": "Short preview text.",
										"summary_text": "Summary line one.\nSummary line two.",
										"plain_text": "Full article body line one.\n\nFull article body line two.",
										"content_state": {
											"blocks": [
												{"text": "Block fallback one"},
												{"text": "Block fallback two"}
											]
										}
									}
								}
							}
						}
					}
				}
			}
		}`,
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert linked x item: %v", err)
	}
	itemID, err := itemInsert.LastInsertId()
	if err != nil {
		t.Fatalf("item id: %v", err)
	}

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO item_source_links (item_id, source_id, original_url, created_at)
		VALUES (?, ?, ?, ?)`,
		itemID,
		sourceID,
		"https://x.com/i/article/"+articleID,
		now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert source link: %v", err)
	}

	result, ok, err := st.GetPreferredLocalSourceExtract(ctx, sourceID)
	if err != nil {
		t.Fatalf("GetPreferredLocalSourceExtract: %v", err)
	}
	if !ok {
		t.Fatal("expected local x article body extract to be found")
	}
	if result.FinalURL != "https://x.com/HamelHusain/article/"+articleID {
		t.Fatalf("unexpected final url: %q", result.FinalURL)
	}
	if result.Title != "Evals Skills for Coding Agents" {
		t.Fatalf("unexpected title: %q", result.Title)
	}
	if result.Content != "Full article body line one.\n\nFull article body line two." {
		t.Fatalf("unexpected body content: %q", result.Content)
	}
	if result.Tool != "x-hydration" || result.ToolVersion != "local-article-body-cache" {
		t.Fatalf("unexpected tool metadata: %s %s", result.Tool, result.ToolVersion)
	}
}

func TestGetPreferredLocalSourceExtractFallsBackToXArticleContentState(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	articleID := "2028328572272742401"

	sourceInsert, err := st.db.ExecContext(ctx, `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:test-x-article-content-state",
		"https://x.com/i/article/"+articleID,
		"https://x.com/i/article/"+articleID,
		"x_article",
		"x.com",
		"sources/x/article-content-state.md",
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert x article source: %v", err)
	}
	sourceID, err := sourceInsert.LastInsertId()
	if err != nil {
		t.Fatalf("source id: %v", err)
	}

	itemInsert, err := st.db.ExecContext(ctx, `
		INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
			published_at, saved_at, synced_at, language, text, article_title, article_text,
			primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
			like_count, repost_count, reply_count, quote_count, bookmark_count,
			content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at,
			x_post_text, x_post_lang, x_post_json, x_post_fetched_at, x_post_status, x_post_error, link_extract_synced_at
		) VALUES (?, ?, ?, ?, ?, ?, '', '', '', '', '', '', '', '', '', '', '[]', '', '', '', '', 0, 0, 0, 0, 0, ?, '', '{}', ?, ?, ?, '', '', ?, ?, 'ok_graphql', '', '')`,
		"x:test-article-content-state",
		"x_bookmark",
		"2030439936437170176",
		"https://x.com/example/status/2030439936437170176",
		"linked x item",
		"example",
		"x:test-article-content-state-hash",
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		`{
			"raw": {
				"data": {
					"tweetResult": {
						"result": {
							"article": {
								"article_results": {
									"result": {
										"title": "Grep Is Dead",
										"rest_id": "2028328572272742401",
										"preview_text": "Preview only.",
										"content_state": {
											"blocks": [
												{"text": "First block text."},
												{"text": "Second block text."},
												{"text": ""}
											]
										}
									}
								}
							}
						}
					}
				}
			}
		}`,
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert linked x item: %v", err)
	}
	itemID, err := itemInsert.LastInsertId()
	if err != nil {
		t.Fatalf("item id: %v", err)
	}

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO item_source_links (item_id, source_id, original_url, created_at)
		VALUES (?, ?, ?, ?)`,
		itemID,
		sourceID,
		"https://x.com/i/article/"+articleID,
		now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert source link: %v", err)
	}

	result, ok, err := st.GetPreferredLocalSourceExtract(ctx, sourceID)
	if err != nil {
		t.Fatalf("GetPreferredLocalSourceExtract: %v", err)
	}
	if !ok {
		t.Fatal("expected local x article content_state extract to be found")
	}
	if result.Content != "First block text.\n\nSecond block text." {
		t.Fatalf("unexpected content_state content: %q", result.Content)
	}
	if result.ToolVersion != "local-article-body-cache" {
		t.Fatalf("unexpected tool version: %q", result.ToolVersion)
	}
}

func TestGetPreferredLocalSourceExtractUsesQuotedParentHydrationForStaleQuoteChild(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	articleID := "2013912039379648512"

	sourceInsert, err := st.db.ExecContext(ctx, `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"src:test-x-article-quoted-parent-fallback",
		"https://x.com/i/article/"+articleID,
		"https://x.com/i/article/"+articleID,
		"x_article",
		"x.com",
		"sources/x/article-quoted-parent.md",
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert x article source: %v", err)
	}
	sourceID, err := sourceInsert.LastInsertId()
	if err != nil {
		t.Fatalf("source id: %v", err)
	}

	childInsert, err := st.db.ExecContext(ctx, `
		INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
			published_at, saved_at, synced_at, language, text, article_title, article_text,
			primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
			like_count, repost_count, reply_count, quote_count, bookmark_count,
			content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at,
			x_post_text, x_post_lang, x_post_json, x_post_fetched_at, x_post_status, x_post_error, link_extract_synced_at
		) VALUES (?, ?, ?, ?, ?, ?, '', '', '', '', '', '', '', '', '', '', '[]', '', '', '', '', 0, 0, 0, 0, 0, ?, '', '{}', ?, ?, ?, '', '', ?, ?, 'ok_syndication', '', '')`,
		"x:test-stale-quoted-child",
		"x_quote",
		"2013919970414272669",
		"https://x.com/acoyne/status/2013919970414272669",
		"stale quoted child",
		"acoyne",
		"x:test-stale-quoted-child-hash",
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		`{"fetched_at":"2026-04-20T19:16:46Z","raw":null,"snapshot":{"id":"2013919970414272669","text":"https://t.co/ndNKPLvafJ"}}`,
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert stale quoted child: %v", err)
	}
	childItemID, err := childInsert.LastInsertId()
	if err != nil {
		t.Fatalf("child item id: %v", err)
	}

	parentInsert, err := st.db.ExecContext(ctx, `
		INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
			published_at, saved_at, synced_at, language, text, article_title, article_text,
			primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
			like_count, repost_count, reply_count, quote_count, bookmark_count,
			content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at,
			x_post_text, x_post_lang, x_post_json, x_post_fetched_at, x_post_status, x_post_error, link_extract_synced_at
		) VALUES (?, ?, ?, ?, ?, ?, '', '', '', '', '', '', '', '', '', '', '[]', '', '', '', '', 0, 0, 0, 0, 0, ?, '', '{}', ?, ?, ?, '', '', ?, ?, 'ok_syndication', '', '')`,
		"x:test-quoted-parent",
		"x_bookmark",
		"2018139486333595761",
		"https://x.com/example/status/2018139486333595761",
		"quoted parent",
		"parentauthor",
		"x:test-quoted-parent-hash",
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		`{"source":"syndication","fetched_at":"2026-04-20T19:16:46Z","snapshot":{"id":"2018139486333595761","text":"parent text","quoted_post":{"id":"2013919970414272669","text":"https://t.co/ndNKPLvafJ"}},"raw":{"id_str":"2018139486333595761","text":"parent text","user":{"screen_name":"parentauthor"},"quoted_tweet":{"id_str":"2013919970414272669","text":"https://t.co/ndNKPLvafJ","user":{"screen_name":"acoyne"},"article":{"title":"Quoted article title","preview_text":"Quoted preview from parent raw payload.","rest_id":"2013912039379648512"}}}}`,
		now.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert quoted parent: %v", err)
	}
	parentItemID, err := parentInsert.LastInsertId()
	if err != nil {
		t.Fatalf("parent item id: %v", err)
	}

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO item_source_links (item_id, source_id, original_url, created_at)
		VALUES (?, ?, ?, ?)`,
		childItemID,
		sourceID,
		"https://x.com/i/article/"+articleID,
		now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert source link: %v", err)
	}

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO item_item_links (parent_item_id, child_item_id, link_kind, ordinal, created_at, updated_at)
		VALUES (?, ?, 'quoted_post', 0, ?, ?)`,
		parentItemID,
		childItemID,
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert quoted parent link: %v", err)
	}

	result, ok, err := st.GetPreferredLocalSourceExtract(ctx, sourceID)
	if err != nil {
		t.Fatalf("GetPreferredLocalSourceExtract: %v", err)
	}
	if !ok {
		t.Fatal("expected local x article extract to be recovered from quoted parent hydration")
	}
	if result.FinalURL != "https://x.com/parentauthor/article/"+articleID {
		t.Fatalf("unexpected final url: %q", result.FinalURL)
	}
	if result.Title != "Quoted article title" {
		t.Fatalf("unexpected title: %q", result.Title)
	}
	if result.Content != "Quoted preview from parent raw payload." {
		t.Fatalf("unexpected content: %q", result.Content)
	}
	if result.Tool != "x-hydration" || result.ToolVersion != "local-article-preview-cache" {
		t.Fatalf("unexpected tool metadata: %s %s", result.Tool, result.ToolVersion)
	}
}

func TestSaveSourceExtractionTracksFailureCountsAndResetsOnSuccess(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	sourceID := insertTestSource(t, st, "src:test-failure-counts", "https://example.com/post")

	if _, err := st.SaveSourceExtraction(ctx, sourceID, model.ExtractResult{
		Status:      "error",
		Error:       "Unable to connect. Is the computer able to access the url?",
		Tool:        "summarize",
		ToolVersion: "test-1.0.0",
	}, ""); err != nil {
		t.Fatalf("first SaveSourceExtraction error: %v", err)
	}

	firstFailure, err := st.GetSourceByID(ctx, sourceID)
	if err != nil {
		t.Fatalf("get source after first failure: %v", err)
	}
	if firstFailure.ExtractFailureKind != "connectivity" {
		t.Fatalf("expected connectivity failure kind, got %q", firstFailure.ExtractFailureKind)
	}
	if firstFailure.ExtractFailureCount != 1 {
		t.Fatalf("expected failure count 1, got %d", firstFailure.ExtractFailureCount)
	}
	if firstFailure.ExtractFirstFailedAt.IsZero() || firstFailure.ExtractLastFailedAt.IsZero() {
		t.Fatalf("expected failure timestamps to be set, got %+v", firstFailure)
	}

	if _, err := st.SaveSourceExtraction(ctx, sourceID, model.ExtractResult{
		Status:      "error",
		Error:       "Unable to connect. Is the computer able to access the url?",
		Tool:        "summarize",
		ToolVersion: "test-1.0.0",
	}, ""); err != nil {
		t.Fatalf("second SaveSourceExtraction error: %v", err)
	}

	secondFailure, err := st.GetSourceByID(ctx, sourceID)
	if err != nil {
		t.Fatalf("get source after second failure: %v", err)
	}
	if secondFailure.ExtractFailureCount != 2 {
		t.Fatalf("expected failure count 2, got %d", secondFailure.ExtractFailureCount)
	}
	if !secondFailure.ExtractFirstFailedAt.Equal(firstFailure.ExtractFirstFailedAt) {
		t.Fatalf("expected first failure timestamp to stay fixed, got %s -> %s", firstFailure.ExtractFirstFailedAt, secondFailure.ExtractFirstFailedAt)
	}

	if _, err := st.SaveSourceExtraction(ctx, sourceID, model.ExtractResult{
		Status:      "dead",
		Error:       "marking source dead after 3 consecutive http access denied failures: Failed to fetch HTML document (status 403)",
		Tool:        "summarize",
		ToolVersion: "test-1.0.0",
	}, ""); err != nil {
		t.Fatalf("terminal SaveSourceExtraction error: %v", err)
	}

	terminalFailure, err := st.GetSourceByID(ctx, sourceID)
	if err != nil {
		t.Fatalf("get source after terminal failure: %v", err)
	}
	if terminalFailure.ExtractFailureKind != "http_access_denied" {
		t.Fatalf("expected latest classified failure kind, got %q", terminalFailure.ExtractFailureKind)
	}
	if terminalFailure.ExtractFailureCount != 3 {
		t.Fatalf("expected total consecutive failure count 3, got %d", terminalFailure.ExtractFailureCount)
	}
	if !terminalFailure.ExtractFirstFailedAt.Equal(firstFailure.ExtractFirstFailedAt) {
		t.Fatalf("expected first failure timestamp to stay fixed through kind change, got %s -> %s", firstFailure.ExtractFirstFailedAt, terminalFailure.ExtractFirstFailedAt)
	}

	if _, err := st.SaveSourceExtraction(ctx, sourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/post",
		FinalURL:     "https://example.com/post",
		Title:        "Example",
		SiteName:     "Example",
		Content:      "body",
		Status:       "ok",
		FetchedAt:    time.Now().UTC(),
		Tool:         "summarize",
		ToolVersion:  "test-1.0.0",
	}, "hash-1"); err != nil {
		t.Fatalf("SaveSourceExtraction success: %v", err)
	}

	recovered, err := st.GetSourceByID(ctx, sourceID)
	if err != nil {
		t.Fatalf("get source after success: %v", err)
	}
	if recovered.ExtractFailureKind != "" || recovered.ExtractFailureCount != 0 {
		t.Fatalf("expected failure metadata reset, got kind=%q count=%d", recovered.ExtractFailureKind, recovered.ExtractFailureCount)
	}
	if !recovered.ExtractFirstFailedAt.IsZero() || !recovered.ExtractLastFailedAt.IsZero() {
		t.Fatalf("expected failure timestamps reset, got first=%s last=%s", recovered.ExtractFirstFailedAt, recovered.ExtractLastFailedAt)
	}
}

func TestSaveSourceUserTagsPropagatesFTSDeleteError(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	sourceID := insertTestSource(t, st, "src:test-fts-delete-error", "https://example.com/fts-delete-error")

	if _, err := st.db.ExecContext(ctx, `DROP TABLE sources_fts`); err != nil {
		t.Fatalf("drop sources_fts: %v", err)
	}

	err := st.SaveSourceUserTags(ctx, sourceID, "source-tag")
	if err == nil {
		t.Fatal("expected SaveSourceUserTags to return FTS delete error")
	}
	if !strings.Contains(err.Error(), "delete source fts") {
		t.Fatalf("expected delete source fts error, got %v", err)
	}
}

func TestSaveSourceUserTagsPropagatesFTSInsertError(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	sourceID := insertTestSource(t, st, "src:test-fts-insert-error", "https://example.com/fts-insert-error")

	if _, err := st.db.ExecContext(ctx, `DROP TABLE sources_fts`); err != nil {
		t.Fatalf("drop sources_fts: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `CREATE TABLE sources_fts(rowid INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create incompatible sources_fts: %v", err)
	}

	err := st.SaveSourceUserTags(ctx, sourceID, "source-tag")
	if err == nil {
		t.Fatal("expected SaveSourceUserTags to return FTS insert error")
	}
	if !strings.Contains(err.Error(), "insert source fts") {
		t.Fatalf("expected insert source fts error, got %v", err)
	}
}

func TestListSourcesForEnrichmentSkipsRecentErrorsAndOrdersOldRetries(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	recentFailure := now.Format(time.RFC3339)
	oldFailure := now.Add(-13 * time.Hour).Format(time.RFC3339)

	insertTestSourceRow(t, st, "src:fresh", "https://example.com/fresh", "", "", 0, "", "")
	insertTestSourceRow(t, st, "src:recent-error", "https://example.com/recent", "error", "connectivity", 1, recentFailure, recentFailure)
	insertTestSourceRow(t, st, "src:retry-low", "https://example.com/retry-low", "error", "tls_certificate", 1, oldFailure, oldFailure)
	insertTestSourceRow(t, st, "src:retry-high", "https://example.com/retry-high", "error", "tls_certificate", 3, oldFailure, oldFailure)
	insertTestSourceRow(t, st, "src:recent-final", "https://example.com/recent-final", "error", "unknown", 4, recentFailure, recentFailure)

	sources, err := st.ListSourcesForEnrichment(ctx, 10, false, false, "", "", "")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(sources) != 4 {
		t.Fatalf("expected 4 queued sources, got %d", len(sources))
	}
	if sources[0].SourceKey != "src:fresh" {
		t.Fatalf("expected fresh source first, got %s", sources[0].SourceKey)
	}
	if sources[1].SourceKey != "src:retry-low" {
		t.Fatalf("expected low retry count second, got %s", sources[1].SourceKey)
	}
	if sources[2].SourceKey != "src:retry-high" {
		t.Fatalf("expected high retry count last, got %s", sources[2].SourceKey)
	}
	if sources[3].SourceKey != "src:recent-final" {
		t.Fatalf("expected recent final-attempt source last, got %s", sources[3].SourceKey)
	}
}

func TestBacklogSourceCountsMatchEnrichmentSelectors(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	oldFailure := now.Add(-13 * time.Hour).Format(time.RFC3339)
	recentFailure := now.Format(time.RFC3339)
	extractedAt := now.Add(-time.Hour).Format(time.RFC3339)

	type sourceFixture struct {
		key                  string
		extractStatus        string
		failureKind          string
		failureCount         int
		firstFailedAt        string
		lastFailedAt         string
		extractedText        string
		extractedAt          string
		summaryStatus        string
		summaryContentHash   string
		summaryPromptVersion string
		summaryTool          string
		summaryToolVersion   string
	}
	insert := func(f sourceFixture) {
		t.Helper()

		contentHash := "hash-" + f.key
		if f.summaryContentHash == "current" {
			f.summaryContentHash = contentHash
		}
		if _, err := st.db.ExecContext(ctx, `
			INSERT INTO sources (
				source_key, canonical_url, normalized_url, source_type, domain, note_path,
				extracted_text, extract_status, extract_failure_kind, extract_failure_count,
				extract_first_failed_at, extract_last_failed_at, extracted_at,
				summary_status, summary_content_hash, summary_prompt_version,
				summary_tool, summary_tool_version, content_hash, created_at, updated_at
			) VALUES (?, ?, ?, 'web', 'example.com', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			f.key,
			"https://example.com/"+strings.TrimPrefix(f.key, "src:"),
			"https://example.com/"+strings.TrimPrefix(f.key, "src:"),
			"sources/web/"+strings.TrimPrefix(f.key, "src:")+".md",
			f.extractedText,
			f.extractStatus,
			f.failureKind,
			f.failureCount,
			f.firstFailedAt,
			f.lastFailedAt,
			f.extractedAt,
			f.summaryStatus,
			f.summaryContentHash,
			f.summaryPromptVersion,
			f.summaryTool,
			f.summaryToolVersion,
			contentHash,
			now.Format(time.RFC3339),
			now.Format(time.RFC3339),
		); err != nil {
			t.Fatalf("insert source fixture %s: %v", f.key, err)
		}
	}

	insert(sourceFixture{key: "src:extract-new"})
	insert(sourceFixture{key: "src:extract-old-error", extractStatus: "error", failureKind: "tls_certificate", failureCount: 1, firstFailedAt: oldFailure, lastFailedAt: oldFailure})
	insert(sourceFixture{key: "src:extract-recent-error", extractStatus: "error", failureKind: "connectivity", failureCount: 1, firstFailedAt: recentFailure, lastFailedAt: recentFailure})
	insert(sourceFixture{key: "src:extract-dead", extractStatus: "dead", failureKind: "http_access_denied", failureCount: 3, firstFailedAt: oldFailure, lastFailedAt: oldFailure})
	insert(sourceFixture{key: "src:summary-new", extractStatus: "ok", extractedText: "summary candidate", extractedAt: extractedAt})
	insert(sourceFixture{key: "src:summary-error", extractStatus: "ok", extractedText: "summary error candidate", extractedAt: extractedAt, summaryStatus: "error", summaryContentHash: "current", summaryPromptVersion: "dbrain-v1", summaryTool: "summarize", summaryToolVersion: "0.2.0"})
	insert(sourceFixture{key: "src:summary-stale-prompt", extractStatus: "ok", extractedText: "summary stale candidate", extractedAt: extractedAt, summaryStatus: "ok", summaryContentHash: "current", summaryPromptVersion: "old-prompt", summaryTool: "summarize", summaryToolVersion: "0.2.0"})
	insert(sourceFixture{key: "src:summary-current", extractStatus: "ok", extractedText: "summary current", extractedAt: extractedAt, summaryStatus: "ok", summaryContentHash: "current", summaryPromptVersion: "dbrain-v1", summaryTool: "summarize", summaryToolVersion: "0.2.0"})

	backlog, err := st.Backlog(ctx, "dbrain-v1", "summarize", "0.2.0")
	if err != nil {
		t.Fatalf("Backlog: %v", err)
	}
	if backlog.SourceExtractionPending != 2 {
		t.Fatalf("expected 2 extraction-pending sources, got %+v", backlog)
	}
	if backlog.SourceSummaryPending != 3 {
		t.Fatalf("expected 3 summary-pending sources, got %+v", backlog)
	}

	extractionOnly, err := st.ListSourcesForEnrichment(ctx, 50, false, false, "dbrain-v1", "summarize", "0.2.0")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment extraction-only: %v", err)
	}
	if got, want := sourceKeys(extractionOnly), []string{"src:extract-new", "src:extract-old-error"}; !sameStringSet(got, want) {
		t.Fatalf("expected extraction selector %v, got %v", want, got)
	}

	withSummary, err := st.ListSourcesForEnrichment(ctx, 50, false, true, "dbrain-v1", "summarize", "0.2.0")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment summarize: %v", err)
	}
	wantSummary := []string{
		"src:extract-new",
		"src:extract-old-error",
		"src:summary-new",
		"src:summary-error",
		"src:summary-stale-prompt",
	}
	if got := sourceKeys(withSummary); !sameStringSet(got, wantSummary) {
		t.Fatalf("expected summarize selector %v, got %v", wantSummary, got)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "brain.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func insertTestSource(t *testing.T, st *Store, sourceKey string, canonicalURL string) int64 {
	t.Helper()

	now := time.Now().UTC().Format(time.RFC3339)
	result, err := st.db.ExecContext(context.Background(), `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, note_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sourceKey,
		canonicalURL,
		canonicalURL,
		"web",
		"example.com",
		"sources/web/example.md",
		now,
		now,
	)
	if err != nil {
		t.Fatalf("insert source %s: %v", sourceKey, err)
	}
	sourceID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("source id %s: %v", sourceKey, err)
	}
	return sourceID
}

func insertTestSourceRow(t *testing.T, st *Store, sourceKey string, canonicalURL string, extractStatus string, failureKind string, failureCount int, firstFailedAt string, lastFailedAt string) {
	t.Helper()

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := st.db.ExecContext(context.Background(), `
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, domain, note_path,
			extract_status, extract_failure_kind, extract_failure_count, extract_first_failed_at, extract_last_failed_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sourceKey,
		canonicalURL,
		canonicalURL,
		"web",
		"example.com",
		"sources/web/example.md",
		extractStatus,
		failureKind,
		failureCount,
		firstFailedAt,
		lastFailedAt,
		now,
		now,
	); err != nil {
		t.Fatalf("insert source row %s: %v", sourceKey, err)
	}
}

func insertTestItem(t *testing.T, st *Store, sourceKey string, articleTitle string, articleText string, updatedAt time.Time) int64 {
	t.Helper()

	result, err := st.db.ExecContext(context.Background(), `
		INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
			published_at, saved_at, synced_at, language, text, article_title, article_text,
			primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
			like_count, repost_count, reply_count, quote_count, bookmark_count,
			content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at,
			x_post_text, x_post_lang, x_post_json, x_post_fetched_at, x_post_status, x_post_error, link_extract_synced_at
		) VALUES (?, ?, ?, ?, ?, '', '', '', '', '', '', '', ?, ?, '', '', '[]', '', '', '', '', 0, 0, 0, 0, 0, ?, '', '{}', ?, ?, ?, '', '', '', '', '', '', '')`,
		sourceKey,
		"x_bookmark",
		sourceKey,
		"https://x.com/example/status/"+sourceKey,
		sourceKey,
		articleTitle,
		articleText,
		sourceKey+"-hash",
		updatedAt.Format(time.RFC3339),
		updatedAt.Format(time.RFC3339),
		updatedAt.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert item %s: %v", sourceKey, err)
	}
	itemID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("item id %s: %v", sourceKey, err)
	}
	return itemID
}

func sourceKeys(sources []model.SourceDocument) []string {
	keys := make([]string, 0, len(sources))
	for _, source := range sources {
		keys = append(keys, source.SourceKey)
	}
	return keys
}

func sameStringSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, value := range left {
		seen[value]++
	}
	for _, value := range right {
		seen[value]--
		if seen[value] < 0 {
			return false
		}
	}
	return true
}
