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
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version >= ?`, 3); err != nil {
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
	if err := st.SaveXMediaTranscription(ctx, upsert.ItemID, XMediaTranscriptionState{
		Status:      model.XMediaTranscriptStatusOK,
		RawJSON:     `[{"backend":"whisper.cpp","model":"ggml-base.en","language":"en","vad_enabled":true}]`,
		Model:       "ggml-base.en",
		Tool:        "whisper.cpp",
		ToolVersion: "xmediatranscribe-v1",
		InputHash:   "sha256:transcript-input",
		CompletedAt: now,
	}); err != nil {
		t.Fatalf("save transcript state: %v", err)
	}
	oldUpdatedAt := now.Add(-24 * time.Hour).Format(time.RFC3339)
	if _, err := st.db.ExecContext(ctx, `
		UPDATE item_enrichments
		SET updated_at = ?
		WHERE item_id = ? AND role = ?`,
		oldUpdatedAt,
		upsert.ItemID,
		model.ItemEnrichmentRoleXMediaTranscript,
	); err != nil {
		t.Fatalf("set stable transcript enrichment timestamp: %v", err)
	}

	assertItemEnrichmentText(t, st, upsert.ItemID, model.ItemEnrichmentRoleSummary, "derived summary text")
	assertItemEnrichmentText(t, st, upsert.ItemID, model.ItemEnrichmentRoleOCR, "raw OCR text")
	assertItemEnrichmentText(t, st, upsert.ItemID, model.ItemEnrichmentRoleXMediaTranscript, "raw transcript text")
	assertItemEnrichmentStatus(t, st, upsert.ItemID, model.ItemEnrichmentRoleXMediaTranscript, model.XMediaTranscriptStatusOK)
	assertTranscriptProvenance(t, st, upsert.ItemID, oldUpdatedAt)

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
	assertTranscriptProvenance(t, st, upsert.ItemID, oldUpdatedAt)

	if err := st.InvalidateItemSummary(ctx, upsert.ItemID); err != nil {
		t.Fatalf("invalidate item summary: %v", err)
	}
	if _, err := st.GetItemEnrichment(ctx, upsert.ItemID, model.ItemEnrichmentRoleSummary); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected summary enrichment deleted, got err=%v", err)
	}
	assertItemEnrichmentText(t, st, upsert.ItemID, model.ItemEnrichmentRoleOCR, "raw OCR text")
	assertItemEnrichmentText(t, st, upsert.ItemID, model.ItemEnrichmentRoleXMediaTranscript, "raw transcript text")
}

func TestProjectedMutationItemEnrichmentRolesDirtyOnlyForProjectedText(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedPurgeItem(t, st, "item:enrichment-dirty")
	var itemID int64
	if err := st.db.QueryRow(`SELECT id FROM items WHERE source_key='item:enrichment-dirty'`).Scan(&itemID); err != nil {
		t.Fatal(err)
	}

	for _, role := range []string{model.ItemEnrichmentRoleSummary, model.ItemEnrichmentRoleOCR, model.ItemEnrichmentRoleXMediaTranscript} {
		markProjectionCurrentForTest(t, st, "item", "item:enrichment-dirty")
		before := projectionRevisionForTest(t, st)
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := st.db.ExecContext(ctx, `INSERT INTO item_enrichments (item_id,role,status,text,created_at,updated_at) VALUES (?,?,'ok','first',?,?)`, itemID, role, now, now); err != nil {
			t.Fatalf("insert %s: %v", role, err)
		}
		if got := projectionRevisionForTest(t, st); got != before+1 {
			t.Fatalf("insert %s revision=%d want %d", role, got, before+1)
		}

		if _, err := st.db.ExecContext(ctx, `UPDATE item_enrichments SET status='error', raw_json='{"ignored":true}', updated_at=? WHERE item_id=? AND role=?`, now, itemID, role); err != nil {
			t.Fatal(err)
		}
		if got := projectionRevisionForTest(t, st); got != before+1 {
			t.Fatalf("metadata update %s dirtied revision=%d want %d", role, got, before+1)
		}

		if _, err := st.db.ExecContext(ctx, `UPDATE item_enrichments SET text='second' WHERE item_id=? AND role=?`, itemID, role); err != nil {
			t.Fatal(err)
		}
		if got := projectionRevisionForTest(t, st); got != before+2 {
			t.Fatalf("text update %s revision=%d want %d", role, got, before+2)
		}
		if _, err := st.db.ExecContext(ctx, `DELETE FROM item_enrichments WHERE item_id=? AND role=?`, itemID, role); err != nil {
			t.Fatal(err)
		}
		if got := projectionRevisionForTest(t, st); got != before+3 {
			t.Fatalf("delete %s revision=%d want %d", role, got, before+3)
		}
	}

	before := projectionRevisionForTest(t, st)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := st.db.ExecContext(ctx, `INSERT INTO item_enrichments (item_id,role,status,text,created_at,updated_at) VALUES (?,'nonprojected','ok','ignored',?,?)`, itemID, now, now); err != nil {
		t.Fatal(err)
	}
	if got := projectionRevisionForTest(t, st); got != before {
		t.Fatalf("nonprojected role dirtied revision=%d want %d", got, before)
	}
}

func TestProjectedMutationUpsertItemWithSummaryUsesNewestTriggeredRevision(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	now := time.Now().UTC()
	before := projectionRevisionForTest(t, st)
	_, err := st.UpsertItem(ctx, model.Item{
		SourceKey: "item:triggered-mirror", SourceType: "test", ExternalID: "triggered-mirror",
		Title: "title", Text: "body", ContentHash: "hash", NotePath: "item.md", RawJSON: "{}",
		SummaryText: "summary", SummaryStatus: model.ItemSummaryStatusOK,
		ImportedAt: now, UpdatedAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The authoritative item insert and authoritative summary-enrichment insert
	// are two mutations in one transaction. Each trigger allocates once; the
	// ledger must retain the newer revision from the enrichment write.
	if got := projectionRevisionForTest(t, st); got != before+2 {
		t.Fatalf("mirrored item insert revision=%d want %d", got, before+2)
	}
	assertProjectionPendingAtRevision(t, st, "item", "item:triggered-mirror", before+2)
}

func assertTranscriptProvenance(t *testing.T, st *Store, itemID int64, wantUpdatedAt string) {
	t.Helper()

	enrichment, err := st.GetItemEnrichment(t.Context(), itemID, model.ItemEnrichmentRoleXMediaTranscript)
	if err != nil {
		t.Fatalf("load transcript enrichment provenance: %v", err)
	}
	if enrichment.RawJSON != `[{"backend":"whisper.cpp","model":"ggml-base.en","language":"en","vad_enabled":true}]` ||
		enrichment.Model != "ggml-base.en" || enrichment.Tool != "whisper.cpp" ||
		enrichment.ToolVersion != "xmediatranscribe-v1" || enrichment.InputHash != "sha256:transcript-input" ||
		!enrichment.CompletedAt.Equal(time.Date(2026, 5, 5, 18, 0, 0, 0, time.UTC)) {
		t.Fatalf("transcript enrichment provenance was not preserved: %+v", enrichment)
	}
	if enrichment.UpdatedAt.Format(time.RFC3339) != wantUpdatedAt {
		t.Fatalf("semantic no-op advanced updated_at to %s, want %s", enrichment.UpdatedAt.Format(time.RFC3339), wantUpdatedAt)
	}
}

func TestGetItemByIDPrefersItemEnrichmentMirror(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()

	now := time.Date(2026, 5, 5, 19, 0, 0, 0, time.UTC)
	upsert, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-enrichment-read-mirror",
		SourceType:   "x_bookmark",
		ExternalID:   "test-enrichment-read-mirror",
		CanonicalURL: "https://x.com/test/status/3",
		Title:        "Read mirror item",
		ContentHash:  "hash-read-mirror",
		NotePath:     "items/x/test-enrichment-read-mirror.md",
		RawJSON:      `{"raw":true}`,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if _, err := st.SaveItemSummary(ctx, upsert.ItemID, model.SummaryResult{
		Text:          "mirror summary",
		RawJSON:       `{"summary":"mirror"}`,
		Model:         "ollama/mirror",
		PromptVersion: "prompt-mirror",
		Status:        model.ItemSummaryStatusOK,
		FetchedAt:     now,
		Tool:          "ollama-direct",
		ToolVersion:   "tool-mirror",
	}, "summary-mirror-input"); err != nil {
		t.Fatalf("save item summary: %v", err)
	}
	if _, err := st.SaveItemOCR(ctx, upsert.ItemID, model.OCRResult{
		Text:        "mirror ocr",
		RawJSON:     `{"ocr":"mirror"}`,
		Model:       "vision/mirror",
		Status:      model.ItemOCRStatusOK,
		FetchedAt:   now,
		Tool:        "openrouter-vision",
		ToolVersion: "vision-mirror",
	}, "ocr-mirror-input"); err != nil {
		t.Fatalf("save item ocr: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET article_title = ?, article_text = ?
		WHERE id = ?`,
		model.XMediaTranscriptArticleTitle,
		"mirror transcript",
		upsert.ItemID,
	); err != nil {
		t.Fatalf("save transcript text fixture: %v", err)
	}
	if err := st.SaveXMediaTranscriptionState(ctx, upsert.ItemID, model.XMediaTranscriptStatusOK, "", now); err != nil {
		t.Fatalf("save transcript state: %v", err)
	}

	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET summary_text = ?,
			summary_json = ?,
			summary_status = ?,
			summary_error = ?,
			summary_model = ?,
			summary_prompt_version = ?,
			summary_tool = ?,
			summary_tool_version = ?,
			summary_input_hash = ?,
			summarized_at = ?,
			ocr_text = ?,
			ocr_json = ?,
			ocr_status = ?,
			ocr_error = ?,
			ocr_model = ?,
			ocr_tool = ?,
			ocr_tool_version = ?,
			ocr_input_hash = ?,
			ocr_at = ?,
			article_title = ?,
			article_text = ?,
			x_media_transcript_status = ?,
			x_media_transcript_error = ?,
			x_media_transcript_at = ?
		WHERE id = ?`,
		"stale summary",
		`{"summary":"stale"}`,
		model.ItemSummaryStatusError,
		"stale summary error",
		"ollama/stale",
		"prompt-stale",
		"summary-stale-tool",
		"summary-stale-version",
		"summary-stale-input",
		now.Add(-time.Hour).Format(time.RFC3339),
		"stale ocr",
		`{"ocr":"stale"}`,
		model.ItemOCRStatusError,
		"stale ocr error",
		"vision/stale",
		"ocr-stale-tool",
		"ocr-stale-version",
		"ocr-stale-input",
		now.Add(-time.Hour).Format(time.RFC3339),
		model.XMediaTranscriptArticleTitle,
		"stale transcript",
		model.XMediaTranscriptStatusError,
		"stale transcript error",
		now.Add(-time.Hour).Format(time.RFC3339),
		upsert.ItemID,
	); err != nil {
		t.Fatalf("stale compatibility columns: %v", err)
	}

	item, err := st.GetItemByID(ctx, upsert.ItemID)
	if err != nil {
		t.Fatalf("get item by id: %v", err)
	}
	if item.SummaryText != "mirror summary" || item.SummaryStatus != model.ItemSummaryStatusOK || item.SummaryModel != "ollama/mirror" {
		t.Fatalf("expected summary mirror values, got text=%q status=%q model=%q", item.SummaryText, item.SummaryStatus, item.SummaryModel)
	}
	if item.OCRText != "mirror ocr" || item.OCRStatus != model.ItemOCRStatusOK || item.OCRModel != "vision/mirror" {
		t.Fatalf("expected OCR mirror values, got text=%q status=%q model=%q", item.OCRText, item.OCRStatus, item.OCRModel)
	}
	if item.ArticleTitle != model.XMediaTranscriptArticleTitle || item.ArticleText != "mirror transcript" || item.XMediaTranscriptStatus != model.XMediaTranscriptStatusOK {
		t.Fatalf("expected transcript mirror values, got title=%q text=%q status=%q", item.ArticleTitle, item.ArticleText, item.XMediaTranscriptStatus)
	}

	lookupItem, err := st.GetItem(ctx, "x:test-enrichment-read-mirror")
	if err != nil {
		t.Fatalf("get item by source key: %v", err)
	}
	if lookupItem.SummaryText != "mirror summary" || lookupItem.OCRText != "mirror ocr" || lookupItem.ArticleText != "mirror transcript" {
		t.Fatalf("expected source-key lookup to use mirror values, got summary=%q ocr=%q transcript=%q", lookupItem.SummaryText, lookupItem.OCRText, lookupItem.ArticleText)
	}
}

func TestGetItemByIDFallsBackToCompatibilityColumns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() {
		_ = st.Close()
	}()

	now := time.Date(2026, 5, 5, 19, 30, 0, 0, time.UTC)
	upsert, err := st.UpsertItem(ctx, model.Item{
		SourceKey:              "x:test-enrichment-read-fallback",
		SourceType:             "x_bookmark",
		ExternalID:             "test-enrichment-read-fallback",
		CanonicalURL:           "https://x.com/test/status/4",
		Title:                  "Read fallback item",
		ArticleTitle:           model.XMediaTranscriptArticleTitle,
		ArticleText:            "column transcript",
		ContentHash:            "hash-read-fallback",
		NotePath:               "items/x/test-enrichment-read-fallback.md",
		RawJSON:                `{"raw":true}`,
		UpdatedAt:              now,
		LastSeenAt:             now,
		SummaryText:            "column summary",
		SummaryStatus:          model.ItemSummaryStatusOK,
		SummaryModel:           "ollama/column",
		SummarizedAt:           now,
		OCRText:                "column ocr",
		OCRStatus:              model.ItemOCRStatusOK,
		OCRModel:               "vision/column",
		OCRAt:                  now,
		XMediaTranscriptStatus: model.XMediaTranscriptStatusOK,
		XMediaTranscriptAt:     now,
	})
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		UPDATE items
		SET x_media_transcript_status = ?,
			x_media_transcript_at = ?
		WHERE id = ?`,
		model.XMediaTranscriptStatusOK,
		now.Format(time.RFC3339),
		upsert.ItemID,
	); err != nil {
		t.Fatalf("set transcript compatibility columns: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM item_enrichments WHERE item_id = ?`, upsert.ItemID); err != nil {
		t.Fatalf("delete mirror rows: %v", err)
	}

	item, err := st.GetItemByID(ctx, upsert.ItemID)
	if err != nil {
		t.Fatalf("get item by id: %v", err)
	}
	if item.SummaryText != "column summary" || item.SummaryStatus != model.ItemSummaryStatusOK || item.SummaryModel != "ollama/column" {
		t.Fatalf("expected summary compatibility values, got text=%q status=%q model=%q", item.SummaryText, item.SummaryStatus, item.SummaryModel)
	}
	if item.OCRText != "column ocr" || item.OCRStatus != model.ItemOCRStatusOK || item.OCRModel != "vision/column" {
		t.Fatalf("expected OCR compatibility values, got text=%q status=%q model=%q", item.OCRText, item.OCRStatus, item.OCRModel)
	}
	if item.ArticleTitle != model.XMediaTranscriptArticleTitle || item.ArticleText != "column transcript" || item.XMediaTranscriptStatus != model.XMediaTranscriptStatusOK {
		t.Fatalf("expected transcript compatibility values, got title=%q text=%q status=%q", item.ArticleTitle, item.ArticleText, item.XMediaTranscriptStatus)
	}
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
