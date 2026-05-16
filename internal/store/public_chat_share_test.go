package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicChatShareStableSlugAndOwnerList(t *testing.T) {
	t.Parallel()

	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()

	ctx := t.Context()
	input := PublicChatShareInput{
		OwnerProvider:    "github",
		OwnerSubject:     "12345",
		OwnerUsername:    "darron",
		Title:            "Agent memory",
		Summary:          "A short summary about agent memory.",
		Categories:       []string{"ai", "software"},
		SanitizedContent: "Agent memory answer with https://example.com/source.",
		OriginalURLs:     []string{"https://example.com/source"},
		MetadataJSON:     `{"question":"What do I know about agent memory?"}`,
	}
	first, err := st.SavePublicChatShare(ctx, input)
	if err != nil {
		t.Fatalf("SavePublicChatShare first: %v", err)
	}
	if first.Slug == "" || strings.Contains(first.Slug, "/") {
		t.Fatalf("expected URL-safe slug, got %q", first.Slug)
	}
	if first.ContentHash == "" || first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() {
		t.Fatalf("expected share metadata, got %+v", first)
	}

	input.Summary = "Updated deterministic summary for the same content."
	second, err := st.SavePublicChatShare(ctx, input)
	if err != nil {
		t.Fatalf("SavePublicChatShare second: %v", err)
	}
	if second.Slug != first.Slug {
		t.Fatalf("identical owner/content should reuse slug: first=%q second=%q", first.Slug, second.Slug)
	}
	if second.Summary != input.Summary {
		t.Fatalf("expected existing share metadata to update, got %q", second.Summary)
	}

	shares, err := st.ListPublicChatSharesByOwner(ctx, "github", "12345", 10)
	if err != nil {
		t.Fatalf("ListPublicChatSharesByOwner: %v", err)
	}
	if len(shares) != 1 || shares[0].Slug != first.Slug {
		t.Fatalf("expected one share for owner, got %+v", shares)
	}
	if len(shares[0].OriginalURLs) != 1 || shares[0].OriginalURLs[0] != "https://example.com/source" {
		t.Fatalf("expected original URL to round-trip, got %+v", shares[0].OriginalURLs)
	}

	otherOwner, err := st.SavePublicChatShare(ctx, PublicChatShareInput{
		OwnerProvider:    "github",
		OwnerSubject:     "67890",
		Summary:          "A short summary about agent memory.",
		SanitizedContent: input.SanitizedContent,
	})
	if err != nil {
		t.Fatalf("SavePublicChatShare other owner: %v", err)
	}
	if otherOwner.Slug == first.Slug {
		t.Fatalf("different owners should not share slugs")
	}
}

func TestOpenRepairsPublicChatShareSchemaOnExistingDB(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS public_chat_shares`); err != nil {
		t.Fatalf("drop public_chat_shares: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS public_share_config`); err != nil {
		t.Fatalf("drop public_share_config: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version >= ?`, 9); err != nil {
		t.Fatalf("remove public share migration metadata: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 8`); err != nil {
		t.Fatalf("set old user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite directly: %v", err)
	}

	st = openStoreAtPath(t, path)
	defer func() {
		_ = st.Close()
	}()

	for _, table := range []string{"public_chat_shares", "public_share_config"} {
		var tableName string
		if err := st.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&tableName); err != nil {
			t.Fatalf("expected %s repair migration to create table: %v", table, err)
		}
	}
	if _, err := st.SavePublicChatShare(t.Context(), PublicChatShareInput{
		OwnerProvider:    "local",
		OwnerSubject:     "local",
		Summary:          "A repaired database can store public chat shares.",
		SanitizedContent: "Public share content after migration repair.",
	}); err != nil {
		t.Fatalf("SavePublicChatShare after repair migration: %v", err)
	}
	assertCurrentSchemaMigration(t, st.db)
}
