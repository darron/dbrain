package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestSearchMatchesAndReturnsUserTags(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	result, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-search-tags",
		SourceType:   "x_bookmark",
		ExternalID:   "test-search-tags",
		CanonicalURL: "https://x.com/example/status/test-search-tags",
		Title:        "Tagged Search Item",
		Text:         "body without the tag query",
		ContentHash:  "test-search-tags-hash",
		NotePath:     "items/x/2026/test-search-tags.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if err := st.SaveItemUserTags(ctx, result.ItemID, "researchtag, local-memory"); err != nil {
		t.Fatalf("save user tags: %v", err)
	}

	results, err := st.Search(ctx, "researchtag", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected tag-matched search result")
	}
	if results[0].SourceKey != "x:test-search-tags" {
		t.Fatalf("expected tagged item first, got %+v", results[0])
	}
	if results[0].UserTags != "researchtag, local-memory" {
		t.Fatalf("expected user tags in search result, got %q", results[0].UserTags)
	}

	tagResults, err := st.SearchUserTags(ctx, "local-memory", 5)
	if err != nil {
		t.Fatalf("SearchUserTags: %v", err)
	}
	if len(tagResults) == 0 {
		t.Fatal("expected direct tag search result")
	}
	if tagResults[0].SourceKey != "x:test-search-tags" {
		t.Fatalf("expected direct tagged item first, got %+v", tagResults[0])
	}
}

func TestSearchExactUserTagsOrdersByLastSeenAt(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	saveTaggedItem := func(key string, seenAt time.Time) {
		t.Helper()
		result, err := st.UpsertItem(ctx, model.Item{
			SourceKey:    key,
			SourceType:   "x_bookmark",
			ExternalID:   strings.TrimPrefix(key, "x:"),
			CanonicalURL: "https://x.com/example/status/" + strings.TrimPrefix(key, "x:"),
			Title:        "Tagged item " + key,
			Text:         "body without the tag query",
			ContentHash:  key + "-hash",
			NotePath:     "items/x/2026/" + strings.TrimPrefix(key, "x:") + ".md",
			RawJSON:      `{}`,
			ImportedAt:   seenAt,
			UpdatedAt:    seenAt,
			LastSeenAt:   seenAt,
		})
		if err != nil {
			t.Fatalf("upsert item %s: %v", key, err)
		}
		if err := st.SaveItemUserTags(ctx, result.ItemID, "demo-video, screen-recording"); err != nil {
			t.Fatalf("save user tags %s: %v", key, err)
		}
	}

	saveTaggedItem("x:z-old-tagged", now.Add(-time.Hour))
	saveTaggedItem("x:a-new-tagged", now)

	results, err := st.SearchExactUserTag(ctx, "screen-recording", 5)
	if err != nil {
		t.Fatalf("SearchExactUserTag: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected two exact tag results, got %#v", results)
	}
	if results[0].SourceKey != "x:a-new-tagged" || results[1].SourceKey != "x:z-old-tagged" {
		t.Fatalf("expected exact tag results in last_seen_at order, got %#v", results)
	}

	results, err = st.SearchUserTags(ctx, "screen-recording", 5)
	if err != nil {
		t.Fatalf("SearchUserTags: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected two fuzzy tag results, got %#v", results)
	}
	if results[0].SourceKey != "x:a-new-tagged" || results[1].SourceKey != "x:z-old-tagged" {
		t.Fatalf("expected fuzzy tag results in last_seen_at order, got %#v", results)
	}
}

func TestSearchMatchesAndReturnsSourceUserTags(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	sourceResult, err := st.UpsertSource(ctx, model.SourceCandidate{
		SourceKey:     "src:test-source-tags",
		CanonicalURL:  "https://example.com/tagged-source",
		NormalizedURL: "https://example.com/tagged-source",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/example-com-test-source-tags.md",
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, sourceResult.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/tagged-source",
		Title:        "Tagged Source",
		Description:  "body without the tag query",
		Content:      "source body without the tag query",
		Status:       "ok",
		FetchedAt:    time.Now().UTC(),
		Tool:         "test",
	}, "test-source-tags-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	if err := st.SaveSourceUserTags(ctx, sourceResult.SourceID, "source-researchtag, compact-mag"); err != nil {
		t.Fatalf("save source user tags: %v", err)
	}

	results, err := st.Search(ctx, "source-researchtag", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected source tag-matched search result")
	}
	if results[0].SourceKey != "src:test-source-tags" {
		t.Fatalf("expected tagged source first, got %+v", results[0])
	}
	if results[0].UserTags != "source-researchtag, compact-mag" {
		t.Fatalf("expected source user tags in search result, got %q", results[0].UserTags)
	}

	tagResults, err := st.SearchUserTags(ctx, "compact-mag", 5)
	if err != nil {
		t.Fatalf("SearchUserTags: %v", err)
	}
	if len(tagResults) == 0 {
		t.Fatal("expected direct source tag search result")
	}
	if tagResults[0].SourceKey != "src:test-source-tags" {
		t.Fatalf("expected direct tagged source first, got %+v", tagResults[0])
	}

	count, err := st.CountExactUserTag(ctx, "compact-mag", nil)
	if err != nil {
		t.Fatalf("CountExactUserTag: %v", err)
	}
	if count != 1 {
		t.Fatalf("CountExactUserTag = %d, want 1", count)
	}

	sourceTextCount, err := st.CountSourceTextMatches(ctx, "source body", nil)
	if err != nil {
		t.Fatalf("CountSourceTextMatches: %v", err)
	}
	if sourceTextCount != 1 {
		t.Fatalf("CountSourceTextMatches = %d, want 1", sourceTextCount)
	}

	filteredSourceTextCount, err := st.CountSourceTextMatches(ctx, "source body", []string{"github"})
	if err != nil {
		t.Fatalf("filtered CountSourceTextMatches: %v", err)
	}
	if filteredSourceTextCount != 0 {
		t.Fatalf("filtered CountSourceTextMatches = %d, want 0", filteredSourceTextCount)
	}
}

func TestSearchFindsExactSourceKey(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	sourceResult, err := st.UpsertSource(ctx, model.SourceCandidate{
		SourceKey:     "src:test-exact-source-key",
		CanonicalURL:  "https://example.com/exact-source-key",
		NormalizedURL: "https://example.com/exact-source-key",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/example-com-exact-source-key.md",
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, sourceResult.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/exact-source-key",
		Title:        "Exact Source Key Fixture",
		Content:      "the source_key column is intentionally not indexed by FTS",
		Status:       "ok",
		FetchedAt:    time.Now().UTC(),
		Tool:         "test",
	}, "exact-source-key-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	results, err := st.Search(ctx, "src:test-exact-source-key", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one exact source result, got %+v", results)
	}
	if results[0].SourceKey != "src:test-exact-source-key" {
		t.Fatalf("expected exact source result, got %+v", results[0])
	}
}

func TestSearchFindsExactItemSourceKey(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "safari-tab:test-exact-item-key",
		SourceType:   "safari_tab",
		ExternalID:   "test-exact-item-key",
		CanonicalURL: "https://example.com/exact-item-key",
		Title:        "Exact Item Key Fixture",
		Text:         "the item source key is an operator-facing lookup value",
		ContentHash:  "test-exact-item-key-hash",
		NotePath:     "items/safari-tabs/2026/test-exact-item-key.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	results, err := st.Search(ctx, "safari-tab:test-exact-item-key", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one exact item result, got %+v", results)
	}
	if results[0].SourceKey != "safari-tab:test-exact-item-key" {
		t.Fatalf("expected exact item result, got %+v", results[0])
	}
}

func TestSearchRelaxesMultiTermQueryWhenStrictSearchMisses(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	sourceResult, err := st.UpsertSource(ctx, model.SourceCandidate{
		SourceKey:     "src:test-x-files-conspiracy",
		CanonicalURL:  "https://example.com/x-files-conspiracy",
		NormalizedURL: "https://example.com/x-files-conspiracy",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/example-com-x-files-conspiracy.md",
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, sourceResult.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/x-files-conspiracy",
		Title:        "The X-Files main conspiracy guide",
		Content:      "A mythology episode list for The X-Files main conspiracy arc.",
		Status:       "ok",
		FetchedAt:    time.Now().UTC(),
		Tool:         "test",
	}, "x-files-conspiracy-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	strayResult, err := st.UpsertSource(ctx, model.SourceCandidate{
		SourceKey:     "src:test-file-watcher",
		CanonicalURL:  "https://example.com/file-watcher",
		NormalizedURL: "https://example.com/file-watcher",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/example-com-file-watcher.md",
	})
	if err != nil {
		t.Fatalf("upsert stray source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, strayResult.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/file-watcher",
		Title:        "File watcher",
		Content:      "A utility that watches files for local filesystem changes.",
		Status:       "ok",
		FetchedAt:    time.Now().UTC(),
		Tool:         "test",
	}, "file-watcher-hash"); err != nil {
		t.Fatalf("save stray source extraction: %v", err)
	}

	results, err := st.Search(ctx, "X-Files season 5 conspiracy", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected relaxed search result")
	}
	if results[0].SourceKey != "src:test-x-files-conspiracy" {
		t.Fatalf("expected relaxed X-Files source first, got %+v", results)
	}
	for _, result := range results {
		if result.SourceKey == "src:test-file-watcher" {
			t.Fatalf("expected relaxed fallback to filter weak one-term match, got %+v", results)
		}
	}
}

func TestCountItemTextMatchesUsesIndexedDerivedText(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-count-derived",
		SourceType:   "x_bookmark",
		ExternalID:   "test-count-derived",
		CanonicalURL: "https://x.com/example/status/test-count-derived",
		Title:        "Derived Count Item",
		Text:         "body without the target phrase",
		SummaryText:  "Calgary police charged a father after two children were found dead.",
		ContentHash:  "test-count-derived-hash",
		NotePath:     "items/x/2026/test-count-derived.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	count, err := st.CountItemTextMatches(ctx, "father two children", nil)
	if err != nil {
		t.Fatalf("CountItemTextMatches: %v", err)
	}
	if count != 1 {
		t.Fatalf("CountItemTextMatches = %d, want 1", count)
	}

	filteredCount, err := st.CountItemTextMatches(ctx, "father two children", []string{"apple_note"})
	if err != nil {
		t.Fatalf("filtered CountItemTextMatches: %v", err)
	}
	if filteredCount != 0 {
		t.Fatalf("filtered CountItemTextMatches = %d, want 0", filteredCount)
	}
}

func TestRebuildFTSUsesItemEnrichmentMirror(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	if !st.HasFTS() {
		t.Skip("FTS is not available")
	}
	ctx := context.Background()
	now := time.Now().UTC()
	result, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-fts-enrichment-mirror",
		SourceType:   "x_bookmark",
		ExternalID:   "test-fts-enrichment-mirror",
		CanonicalURL: "https://x.com/example/status/test-fts-enrichment-mirror",
		Title:        "FTS Mirror Item",
		Text:         "body without mirror-only terms",
		ContentHash:  "test-fts-enrichment-mirror-hash",
		NotePath:     "items/x/2026/test-fts-enrichment-mirror.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.SaveItemSummary(ctx, result.ItemID, model.SummaryResult{
		Text:      "current mirror summary alphaftsmirror",
		Status:    model.ItemSummaryStatusOK,
		FetchedAt: now,
	}, "summary-fts-mirror-input"); err != nil {
		t.Fatalf("save item summary: %v", err)
	}
	if _, err := st.SaveItemOCR(ctx, result.ItemID, model.OCRResult{
		Text:      "current mirror ocr betaftsmirror",
		Status:    model.ItemOCRStatusOK,
		FetchedAt: now,
	}, "ocr-fts-mirror-input"); err != nil {
		t.Fatalf("save item ocr: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET article_title = ?, article_text = ?
		WHERE id = ?`,
		model.XMediaTranscriptArticleTitle,
		"current mirror transcript gammaftsmirror",
		result.ItemID,
	); err != nil {
		t.Fatalf("save transcript text fixture: %v", err)
	}
	if err := st.SaveXMediaTranscriptionState(ctx, result.ItemID, model.XMediaTranscriptStatusOK, "", now); err != nil {
		t.Fatalf("save transcript state: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET summary_text = 'stale summary',
			ocr_text = 'stale ocr',
			article_title = ?,
			article_text = 'stale transcript'
		WHERE id = ?`,
		model.XMediaTranscriptArticleTitle,
		result.ItemID,
	); err != nil {
		t.Fatalf("stale compatibility columns: %v", err)
	}

	stats, err := st.RebuildFTS(ctx)
	if err != nil {
		t.Fatalf("rebuild fts: %v", err)
	}
	if stats.Errors != 0 {
		t.Fatalf("expected no rebuild errors, got %+v", stats)
	}

	results, err := st.Search(ctx, "alphaftsmirror", 5)
	if err != nil {
		t.Fatalf("search summary mirror term: %v", err)
	}
	if len(results) == 0 || results[0].SourceKey != "x:test-fts-enrichment-mirror" {
		t.Fatalf("expected summary mirror search result, got %+v", results)
	}
	if !strings.Contains(results[0].Snippet, "alphaftsmirror") {
		t.Fatalf("expected search snippet from mirror summary, got %q", results[0].Snippet)
	}

	count, err := st.CountItemTextMatches(ctx, "betaftsmirror", nil)
	if err != nil {
		t.Fatalf("count OCR mirror term: %v", err)
	}
	if count != 1 {
		t.Fatalf("CountItemTextMatches for OCR mirror term = %d, want 1", count)
	}

	results, err = st.Search(ctx, "gammaftsmirror", 5)
	if err != nil {
		t.Fatalf("search transcript mirror term: %v", err)
	}
	if len(results) == 0 || results[0].SourceKey != "x:test-fts-enrichment-mirror" {
		t.Fatalf("expected transcript mirror search result, got %+v", results)
	}
}

func TestSearchSnippetUsesTranscriptMatchText(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	if !st.HasFTS() {
		t.Skip("FTS is not available")
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-search-transcript-snippet",
		SourceType:   "x_bookmark",
		ExternalID:   "test-search-transcript-snippet",
		CanonicalURL: "https://x.com/example/status/test-search-transcript-snippet",
		Title:        "Transcript Snippet Item",
		Text:         "body without the quote",
		ArticleTitle: model.XMediaTranscriptArticleTitle,
		ArticleText:  "Transcript:\n\nThe recording says the red balloon promise out loud.",
		SummaryText:  "Generic summary that does not include the quoted recording phrase.",
		ContentHash:  "test-search-transcript-snippet-hash",
		NotePath:     "items/x/2026/test-search-transcript-snippet.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	results, err := st.Search(ctx, "red balloon promise", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 || results[0].SourceKey != "x:test-search-transcript-snippet" {
		t.Fatalf("expected transcript-backed search result, got %+v", results)
	}
	if !strings.Contains(results[0].Snippet, "red balloon promise") {
		t.Fatalf("expected snippet to expose transcript match, got %q", results[0].Snippet)
	}
}

func TestSyncItemFTSByIDUsesItemEnrichmentMirror(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	if !st.HasFTS() {
		t.Skip("FTS is not available")
	}
	ctx := context.Background()
	now := time.Now().UTC()
	result, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-fts-enrichment-incremental",
		SourceType:   "x_bookmark",
		ExternalID:   "test-fts-enrichment-incremental",
		CanonicalURL: "https://x.com/example/status/test-fts-enrichment-incremental",
		Title:        "FTS Incremental Mirror Item",
		Text:         "body without incremental mirror-only terms",
		ContentHash:  "test-fts-enrichment-incremental-hash",
		NotePath:     "items/x/2026/test-fts-enrichment-incremental.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.SaveItemSummary(ctx, result.ItemID, model.SummaryResult{
		Text:      "incremental mirror summary deltaftsmirror",
		Status:    model.ItemSummaryStatusOK,
		FetchedAt: now,
	}, "summary-fts-incremental-input"); err != nil {
		t.Fatalf("save item summary: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET summary_text = 'stale incremental summary'
		WHERE id = ?`,
		result.ItemID,
	); err != nil {
		t.Fatalf("stale compatibility columns: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM items_fts WHERE rowid = ?`, result.ItemID); err != nil {
		t.Fatalf("clear fts row: %v", err)
	}

	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := st.syncItemFTSByIDTx(ctx, tx, result.ItemID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("sync item fts by id: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	results, err := st.Search(ctx, "deltaftsmirror", 5)
	if err != nil {
		t.Fatalf("search incremental mirror term: %v", err)
	}
	if len(results) == 0 || results[0].SourceKey != "x:test-fts-enrichment-incremental" {
		t.Fatalf("expected incremental mirror search result, got %+v", results)
	}
}

func TestOpenReadOnlyCanSearchExistingStore(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/brain.db"
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open writable store: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	result, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-read-only-search",
		SourceType:   "x_bookmark",
		ExternalID:   "test-read-only-search",
		CanonicalURL: "https://x.com/example/status/test-read-only-search",
		Title:        "Read Only Search Item",
		Text:         "read only mcp search",
		ContentHash:  "read-only-search-hash",
		NotePath:     "items/x/2026/test-read-only-search.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if err := st.SaveItemUserTags(ctx, result.ItemID, "read-only-tag"); err != nil {
		t.Fatalf("save user tags: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close writable store: %v", err)
	}

	readOnly, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open read-only store: %v", err)
	}
	defer func() {
		_ = readOnly.Close()
	}()

	results, err := readOnly.SearchUserTags(ctx, "read-only-tag", 5)
	if err != nil {
		t.Fatalf("read-only tag search: %v", err)
	}
	if len(results) != 1 || results[0].SourceKey != "x:test-read-only-search" {
		t.Fatalf("unexpected read-only search results: %+v", results)
	}
	if err := readOnly.SaveItemUserTags(ctx, result.ItemID, "should-fail"); err == nil {
		t.Fatal("expected read-only store write to fail")
	}
}
