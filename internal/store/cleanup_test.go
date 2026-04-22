package store

import (
	"context"
	"testing"
	"time"

	"dbrain/internal/model"
)

func TestDeleteItemsBySourceType(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "yt:history:test-delete",
		SourceType:   "youtube_history",
		ExternalID:   "test-delete",
		CanonicalURL: "https://www.youtube.com/watch?v=test-delete",
		Title:        "Delete me",
		ContentHash:  "delete-item-hash",
		NotePath:     "items/youtube/history/2026/test-delete.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-delete",
		OriginalURL:   "https://www.youtube.com/watch?v=test-delete",
		CanonicalURL:  "https://www.youtube.com/watch?v=test-delete",
		NormalizedURL: "https://www.youtube.com/watch?v=test-delete",
		SourceType:    "youtube",
		Domain:        "youtube.com",
		NotePath:      "sources/youtube/test-delete.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}

	result, err := st.DeleteItemsBySourceType(context.Background(), "youtube_history")
	if err != nil {
		t.Fatalf("DeleteItemsBySourceType: %v", err)
	}
	if result.Count != 1 {
		t.Fatalf("expected 1 deleted item, got %+v", result)
	}
	if len(result.NotePaths) != 1 || result.NotePaths[0] != "items/youtube/history/2026/test-delete.md" {
		t.Fatalf("unexpected note paths: %+v", result.NotePaths)
	}
	if len(result.SourceIDs) != 1 || result.SourceIDs[0] != link.SourceID {
		t.Fatalf("unexpected linked source ids: %+v", result.SourceIDs)
	}
	if _, err := st.GetItem(context.Background(), "yt:history:test-delete"); err == nil {
		t.Fatal("expected item lookup to fail after deletion")
	}
}

func TestDeleteOrphanSources(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "yt:watch_later:test-orphan",
		SourceType:   "youtube_watch_later",
		ExternalID:   "test-orphan",
		CanonicalURL: "https://www.youtube.com/watch?v=test-orphan",
		Title:        "Keep me",
		ContentHash:  "orphan-item-hash",
		NotePath:     "items/youtube/watch_later/2026/test-orphan.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-linked",
		OriginalURL:   "https://www.youtube.com/watch?v=test-linked",
		CanonicalURL:  "https://www.youtube.com/watch?v=test-linked",
		NormalizedURL: "https://www.youtube.com/watch?v=test-linked",
		SourceType:    "youtube",
		Domain:        "youtube.com",
		NotePath:      "sources/youtube/test-linked.md",
	}); err != nil {
		t.Fatalf("insert linked source: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-orphan-temp",
		OriginalURL:   "https://www.youtube.com/watch?v=test-orphan-temp",
		CanonicalURL:  "https://www.youtube.com/watch?v=test-orphan-temp",
		NormalizedURL: "https://www.youtube.com/watch?v=test-orphan-temp",
		SourceType:    "youtube",
		Domain:        "youtube.com",
		NotePath:      "sources/youtube/test-orphan-temp.md",
	}); err != nil {
		t.Fatalf("insert temporary source: %v", err)
	}
	if _, err := st.DeleteItemsBySourceType(context.Background(), "youtube_watch_later"); err != nil {
		t.Fatalf("delete items to create orphan sources: %v", err)
	}

	result, err := st.DeleteOrphanSources(context.Background(), "youtube")
	if err != nil {
		t.Fatalf("DeleteOrphanSources: %v", err)
	}
	if result.Count != 2 {
		t.Fatalf("expected 2 deleted orphan sources, got %+v", result)
	}
	if _, err := st.GetSource(context.Background(), "src:test-linked"); err == nil {
		t.Fatal("expected linked source to be gone after orphan cleanup")
	}
	if _, err := st.GetSource(context.Background(), "src:test-orphan-temp"); err == nil {
		t.Fatal("expected orphan source to be gone after cleanup")
	}
}
