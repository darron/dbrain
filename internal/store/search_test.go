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
