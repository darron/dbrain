package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
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
	if result.Tool != "ft-bookmarks" || result.ToolVersion != "local-item-cache" {
		t.Fatalf("unexpected tool metadata: %s %s", result.Tool, result.ToolVersion)
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
