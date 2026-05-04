package store

import (
	"context"
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
