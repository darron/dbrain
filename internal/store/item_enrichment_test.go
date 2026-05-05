package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestMigrationBackfillsItemEnrichments(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	now := time.Date(2026, 5, 5, 17, 30, 0, 0, time.UTC)
	nowText := now.Format(time.RFC3339)
	result, err := st.db.Exec(`
		INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title,
			article_title, article_text,
			content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at,
			summary_text, summary_json, summary_status, summary_error, summary_model,
			summary_prompt_version, summary_tool, summary_tool_version, summary_input_hash, summarized_at,
			ocr_text, ocr_json, ocr_status, ocr_error, ocr_model, ocr_tool, ocr_tool_version, ocr_input_hash, ocr_at,
			x_media_transcript_status, x_media_transcript_error, x_media_transcript_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"x:test-enrichment-backfill",
		"x_bookmark",
		"test-enrichment-backfill",
		"https://x.com/test/status/1",
		"Backfill item",
		model.XMediaTranscriptArticleTitle,
		"raw transcript evidence",
		"hash-backfill",
		"items/x/test-enrichment-backfill.md",
		`{"raw":true}`,
		nowText,
		nowText,
		nowText,
		"summary evidence",
		`{"summary":true}`,
		model.ItemSummaryStatusOK,
		"",
		"ollama/test",
		"prompt-v1",
		"ollama-direct",
		"tool-v1",
		"summary-input",
		nowText,
		"ocr evidence",
		`{"ocr":true}`,
		model.ItemOCRStatusOK,
		"",
		"vision/test",
		"openrouter-vision",
		"vision-v1",
		"ocr-input",
		nowText,
		model.XMediaTranscriptStatusOK,
		"",
		nowText,
	)
	if err != nil {
		t.Fatalf("insert backfill fixture: %v", err)
	}
	itemID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("fixture item id: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, currentSchemaVersion); err != nil {
		t.Fatalf("simulate pre-v3 migration metadata: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatalf("set old user_version: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE item_enrichments`); err != nil {
		t.Fatalf("drop item_enrichments: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite directly: %v", err)
	}

	st = openStoreAtPath(t, path)
	defer func() {
		_ = st.Close()
	}()

	assertItemEnrichmentText(t, st, itemID, model.ItemEnrichmentRoleSummary, "summary evidence")
	assertItemEnrichmentText(t, st, itemID, model.ItemEnrichmentRoleOCR, "ocr evidence")
	assertItemEnrichmentText(t, st, itemID, model.ItemEnrichmentRoleXMediaTranscript, "raw transcript evidence")
	assertCurrentSchemaMigration(t, st.db)
}

func TestItemEnrichmentMirrorPreservesRawRoles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()

	now := time.Date(2026, 5, 5, 18, 0, 0, 0, time.UTC)
	upsert, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-enrichment-mirror",
		SourceType:   "x_bookmark",
		ExternalID:   "test-enrichment-mirror",
		CanonicalURL: "https://x.com/test/status/2",
		Title:        "Mirror item",
		ContentHash:  "hash-mirror",
		NotePath:     "items/x/test-enrichment-mirror.md",
		RawJSON:      `{"raw":true}`,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}

	if _, err := st.SaveItemOCR(ctx, upsert.ItemID, model.OCRResult{
		Text:        "raw OCR text",
		RawJSON:     `{"ocr":true}`,
		Model:       "vision/test",
		Status:      model.ItemOCRStatusOK,
		FetchedAt:   now,
		Tool:        "openrouter-vision",
		ToolVersion: "vision-v1",
	}, "ocr-input"); err != nil {
		t.Fatalf("save item ocr: %v", err)
	}
	if _, err := st.SaveItemSummary(ctx, upsert.ItemID, model.SummaryResult{
		Text:          "derived summary text",
		RawJSON:       `{"summary":true}`,
		Model:         "ollama/test",
		PromptVersion: "prompt-v1",
		Status:        model.ItemSummaryStatusOK,
		FetchedAt:     now,
		Tool:          "ollama-direct",
		ToolVersion:   "tool-v1",
	}, "summary-input"); err != nil {
		t.Fatalf("save item summary: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET article_title = ?, article_text = ?
		WHERE id = ?`,
		model.XMediaTranscriptArticleTitle,
		"raw transcript text",
		upsert.ItemID,
	); err != nil {
		t.Fatalf("save transcript text fixture: %v", err)
	}
	if err := st.SaveXMediaTranscriptionState(ctx, upsert.ItemID, model.XMediaTranscriptStatusOK, "", now); err != nil {
		t.Fatalf("save transcript state: %v", err)
	}

	assertItemEnrichmentText(t, st, upsert.ItemID, model.ItemEnrichmentRoleSummary, "derived summary text")
	assertItemEnrichmentText(t, st, upsert.ItemID, model.ItemEnrichmentRoleOCR, "raw OCR text")
	assertItemEnrichmentText(t, st, upsert.ItemID, model.ItemEnrichmentRoleXMediaTranscript, "raw transcript text")
	assertItemEnrichmentStatus(t, st, upsert.ItemID, model.ItemEnrichmentRoleXMediaTranscript, model.XMediaTranscriptStatusOK)

	if _, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-enrichment-mirror",
		SourceType:   "x_bookmark",
		ExternalID:   "test-enrichment-mirror",
		CanonicalURL: "https://x.com/test/status/2",
		Title:        "Mirror item updated",
		ContentHash:  "hash-mirror-updated",
		NotePath:     "items/x/test-enrichment-mirror.md",
		RawJSON:      `{"raw":"updated"}`,
		UpdatedAt:    now.Add(time.Minute),
		LastSeenAt:   now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("ordinary item upsert: %v", err)
	}
	assertItemEnrichmentText(t, st, upsert.ItemID, model.ItemEnrichmentRoleXMediaTranscript, "raw transcript text")
	assertItemEnrichmentStatus(t, st, upsert.ItemID, model.ItemEnrichmentRoleXMediaTranscript, model.XMediaTranscriptStatusOK)

	if err := st.InvalidateItemSummary(ctx, upsert.ItemID); err != nil {
		t.Fatalf("invalidate item summary: %v", err)
	}
	if _, err := st.GetItemEnrichment(ctx, upsert.ItemID, model.ItemEnrichmentRoleSummary); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected summary enrichment deleted, got err=%v", err)
	}
	assertItemEnrichmentText(t, st, upsert.ItemID, model.ItemEnrichmentRoleOCR, "raw OCR text")
	assertItemEnrichmentText(t, st, upsert.ItemID, model.ItemEnrichmentRoleXMediaTranscript, "raw transcript text")
}

func assertItemEnrichmentText(t *testing.T, st *Store, itemID int64, role string, want string) {
	t.Helper()

	enrichment, err := st.GetItemEnrichment(context.Background(), itemID, role)
	if err != nil {
		t.Fatalf("load item enrichment %s: %v", role, err)
	}
	if enrichment.Text != want {
		t.Fatalf("expected %s text %q, got %q", role, want, enrichment.Text)
	}
}

func assertItemEnrichmentStatus(t *testing.T, st *Store, itemID int64, role string, want string) {
	t.Helper()

	enrichment, err := st.GetItemEnrichment(context.Background(), itemID, role)
	if err != nil {
		t.Fatalf("load item enrichment %s: %v", role, err)
	}
	if enrichment.Status != want {
		t.Fatalf("expected %s status %q, got %q", role, want, enrichment.Status)
	}
}
