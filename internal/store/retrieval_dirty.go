package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/retrievalchunk"
)

const retrievalDirtyTimestampSQL = `strftime('%Y-%m-%dT%H:%M:%SZ', 'now')`

// Migration 17 is immutable history. It included the raw content_hash columns
// in the item/source UPDATE triggers before those columns were classified as
// provenance-only. Migration 18 repairs existing v17 databases to the current
// definitions below.
var semanticProjectionDirtyTriggersV17 = []retrievalConstraintTrigger{
	projectionDirtyInsertTrigger("trg_retrieval_items_dirty_insert", "items", "item", "NEW.source_key", "1"),
	projectionDirtyUpdateTrigger(
		"trg_retrieval_items_dirty_update", "items", "item",
		"source_key, content_hash, title, source_type, author_name, author_handle, text, x_post_text, ocr_text, article_title, article_text, summary_text, note_path",
		"OLD.source_key", "NEW.source_key",
		`OLD.source_key IS NOT NEW.source_key
			OR OLD.content_hash IS NOT NEW.content_hash
			OR OLD.title IS NOT NEW.title
			OR OLD.source_type IS NOT NEW.source_type
			OR OLD.author_name IS NOT NEW.author_name
			OR OLD.author_handle IS NOT NEW.author_handle
			OR OLD.text IS NOT NEW.text
			OR OLD.x_post_text IS NOT NEW.x_post_text
			OR OLD.ocr_text IS NOT NEW.ocr_text
			OR OLD.article_title IS NOT NEW.article_title
			OR OLD.article_text IS NOT NEW.article_text
			OR OLD.summary_text IS NOT NEW.summary_text
			OR OLD.note_path IS NOT NEW.note_path`,
	),
	projectionDirtyDeleteTrigger("trg_retrieval_items_dirty_delete", "items", "item", "OLD.source_key", "1"),
	projectionDirtyInsertTrigger("trg_retrieval_sources_dirty_insert", "sources", "source", "NEW.source_key", "1"),
	projectionDirtyUpdateTrigger(
		"trg_retrieval_sources_dirty_update", "sources", "source",
		"source_key, content_hash, title, source_type, domain, extracted_text, summary_text, note_path",
		"OLD.source_key", "NEW.source_key",
		`OLD.source_key IS NOT NEW.source_key
			OR OLD.content_hash IS NOT NEW.content_hash
			OR OLD.title IS NOT NEW.title
			OR OLD.source_type IS NOT NEW.source_type
			OR OLD.domain IS NOT NEW.domain
			OR OLD.extracted_text IS NOT NEW.extracted_text
			OR OLD.summary_text IS NOT NEW.summary_text
			OR OLD.note_path IS NOT NEW.note_path`,
	),
	projectionDirtyDeleteTrigger("trg_retrieval_sources_dirty_delete", "sources", "source", "OLD.source_key", "1"),
	projectionDirtyInsertTrigger(
		"trg_retrieval_item_enrichments_dirty_insert", "item_enrichments", "item",
		"(SELECT source_key FROM items WHERE id = NEW.item_id)", projectedEnrichmentRoleSQL("NEW.role"),
	),
	projectionDirtyEnrichmentUpdateTrigger(),
	projectionDirtyDeleteTrigger(
		"trg_retrieval_item_enrichments_dirty_delete", "item_enrichments", "item",
		"(SELECT source_key FROM items WHERE id = OLD.item_id)", projectedEnrichmentRoleSQL("OLD.role"),
	),
}

var semanticProjectionDirtyTriggers = []retrievalConstraintTrigger{
	projectionDirtyInsertTrigger("trg_retrieval_items_dirty_insert", "items", "item", "NEW.source_key", "1"),
	projectionDirtyUpdateTrigger(
		"trg_retrieval_items_dirty_update", "items", "item",
		"source_key, title, source_type, author_name, author_handle, text, x_post_text, ocr_text, article_title, article_text, summary_text, note_path",
		"OLD.source_key", "NEW.source_key",
		`OLD.source_key IS NOT NEW.source_key
			OR OLD.title IS NOT NEW.title
			OR OLD.source_type IS NOT NEW.source_type
			OR OLD.author_name IS NOT NEW.author_name
			OR OLD.author_handle IS NOT NEW.author_handle
			OR OLD.text IS NOT NEW.text
			OR OLD.x_post_text IS NOT NEW.x_post_text
			OR OLD.ocr_text IS NOT NEW.ocr_text
			OR OLD.article_title IS NOT NEW.article_title
			OR OLD.article_text IS NOT NEW.article_text
			OR OLD.summary_text IS NOT NEW.summary_text
			OR OLD.note_path IS NOT NEW.note_path`,
	),
	projectionDirtyDeleteTrigger("trg_retrieval_items_dirty_delete", "items", "item", "OLD.source_key", "1"),
	projectionDirtyInsertTrigger("trg_retrieval_sources_dirty_insert", "sources", "source", "NEW.source_key", "1"),
	projectionDirtyUpdateTrigger(
		"trg_retrieval_sources_dirty_update", "sources", "source",
		"source_key, title, source_type, domain, extracted_text, summary_text, note_path",
		"OLD.source_key", "NEW.source_key",
		`OLD.source_key IS NOT NEW.source_key
			OR OLD.title IS NOT NEW.title
			OR OLD.source_type IS NOT NEW.source_type
			OR OLD.domain IS NOT NEW.domain
			OR OLD.extracted_text IS NOT NEW.extracted_text
			OR OLD.summary_text IS NOT NEW.summary_text
			OR OLD.note_path IS NOT NEW.note_path`,
	),
	projectionDirtyDeleteTrigger("trg_retrieval_sources_dirty_delete", "sources", "source", "OLD.source_key", "1"),
	projectionDirtyInsertTrigger(
		"trg_retrieval_item_enrichments_dirty_insert", "item_enrichments", "item",
		"(SELECT source_key FROM items WHERE id = NEW.item_id)", projectedEnrichmentRoleSQL("NEW.role"),
	),
	projectionDirtyEnrichmentUpdateTrigger(),
	projectionDirtyDeleteTrigger(
		"trg_retrieval_item_enrichments_dirty_delete", "item_enrichments", "item",
		"(SELECT source_key FROM items WHERE id = OLD.item_id)", projectedEnrichmentRoleSQL("OLD.role"),
	),
}

func (s *Store) ensureSemanticProjectionDirtyTriggers() error {
	return s.ensureSemanticProjectionDirtyTriggerDefinitions(semanticProjectionDirtyTriggers)
}

func (s *Store) ensureSemanticProjectionDirtyTriggersV17() error {
	return s.ensureSemanticProjectionDirtyTriggerDefinitions(semanticProjectionDirtyTriggersV17)
}

func (s *Store) repairSemanticProjectionDirtyTriggerProvenance() error {
	if err := s.ensureSemanticProjectionDirtyTriggers(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin semantic projection provenance repair: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var parentCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM retrieval_parent_projections`).Scan(&parentCount); err != nil {
		return fmt.Errorf("count semantic parents for provenance repair: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM retrieval_projection_staging`); err != nil {
		return fmt.Errorf("clear semantic projection staging for provenance repair: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if parentCount > 0 {
		if _, err := tx.Exec(`
			UPDATE retrieval_state
			SET projection_work_revision=projection_work_revision+1, updated_at=?
			WHERE singleton=1`, now); err != nil {
			return fmt.Errorf("allocate semantic projection provenance repair revision: %w", err)
		}
		var revision int64
		if err := tx.QueryRow(`SELECT projection_work_revision FROM retrieval_state WHERE singleton=1`).Scan(&revision); err != nil {
			return fmt.Errorf("read semantic projection provenance repair revision: %w", err)
		}
		if _, err := tx.Exec(`
			UPDATE retrieval_parent_projections
			SET status='pending', reason='', dirty_at=?, dirty_revision=?, updated_at=?`,
			now, revision, now); err != nil {
			return fmt.Errorf("dirty semantic parents for provenance repair: %w", err)
		}
	}
	if _, err := tx.Exec(`
		UPDATE retrieval_index_generations
		SET build_status='stale', active=0, activated_at='', updated_at=?`, now); err != nil {
		return fmt.Errorf("stale semantic generations for provenance repair: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit semantic projection provenance repair: %w", err)
	}
	return nil
}

func (s *Store) ensureSemanticProjectionDirtyTriggerDefinitions(triggers []retrievalConstraintTrigger) error {
	for _, trigger := range triggers {
		if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
			return fmt.Errorf("drop semantic projection dirty trigger %s: %w", trigger.name, err)
		}
		if _, err := s.db.Exec(trigger.sql); err != nil {
			return fmt.Errorf("create semantic projection dirty trigger %s: %w", trigger.name, err)
		}
	}
	return nil
}

// MarkRetrievalParentDirtyTx allocates a durable work revision and invalidates
// legacy generations containing the parent's old chunks in the caller's
// transaction. Authoritative item, source, and enrichment writes are covered
// independently by database triggers; callers should use this named seam only
// when they need the allocated revision without performing such a write.
func MarkRetrievalParentDirtyTx(ctx context.Context, tx *sql.Tx, kind, sourceKey string) (int64, error) {
	kind = strings.TrimSpace(kind)
	sourceKey = strings.TrimSpace(sourceKey)
	revision, err := allocateRetrievalParentDirtyTx(ctx, tx, kind, sourceKey)
	if err != nil {
		return 0, err
	}
	if err := markRetrievalParentGenerationsStaleTx(ctx, tx, kind, sourceKey); err != nil {
		return 0, err
	}
	return revision, nil
}

func markRetrievalParentGenerationsStaleTx(ctx context.Context, tx *sql.Tx, kind, sourceKey string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		UPDATE retrieval_index_generations
		SET build_status = 'stale', active = 0, activated_at = '', updated_at = ?
		WHERE build_status != 'stale'
			AND profile_id IN (
				SELECT DISTINCT embedding.profile_id
				FROM retrieval_embeddings embedding
				JOIN retrieval_chunks chunk ON chunk.chunk_id = embedding.chunk_id
				WHERE chunk.parent_kind = ? AND chunk.parent_source_key = ?
			)`, now, kind, sourceKey); err != nil {
		return fmt.Errorf("mark retrieval generations stale for parent %s %s: %w", kind, sourceKey, err)
	}
	return nil
}

func projectedEnrichmentRoleSQL(role string) string {
	return role + ` IN ('summary', 'ocr', 'x_media_transcript')`
}

func projectionDirtyInsertTrigger(name, table, kind, key, when string) retrievalConstraintTrigger {
	return retrievalConstraintTrigger{
		name:  name,
		table: table,
		sql: fmt.Sprintf(`CREATE TRIGGER %s
			AFTER INSERT ON %s
			WHEN (%s) AND trim(COALESCE(%s, '')) != ''
			BEGIN
				%s
			END`, name, table, when, key, projectionDirtySQL(kind, key)),
	}
}

func projectionDirtyDeleteTrigger(name, table, kind, key, when string) retrievalConstraintTrigger {
	return retrievalConstraintTrigger{
		name:  name,
		table: table,
		sql: fmt.Sprintf(`CREATE TRIGGER %s
			AFTER DELETE ON %s
			WHEN (%s) AND trim(COALESCE(%s, '')) != ''
			BEGIN
				%s
			END`, name, table, when, key, projectionDirtySQL(kind, key)),
	}
}

func projectionDirtyUpdateTrigger(name, table, kind, columns, oldKey, newKey, when string) retrievalConstraintTrigger {
	return retrievalConstraintTrigger{
		name:  name,
		table: table,
		sql: fmt.Sprintf(`CREATE TRIGGER %s
			AFTER UPDATE OF %s ON %s
			WHEN %s
			BEGIN
				UPDATE retrieval_state
				SET projection_work_revision = projection_work_revision + 1, updated_at = %s
				WHERE singleton = 1;
				%s
				%s
				%s
				%s
			END`,
			name, columns, table, when, retrievalDirtyTimestampSQL,
			projectionGenerationInvalidationSQL(kind, oldKey, fmt.Sprintf("%s IS NOT %s", oldKey, newKey)),
			projectionGenerationInvalidationSQL(kind, newKey, "1"),
			projectionLedgerUpsertSQL(kind, oldKey, fmt.Sprintf("%s IS NOT %s", oldKey, newKey)),
			projectionLedgerUpsertSQL(kind, newKey, "1")),
	}
}

func projectionDirtyEnrichmentUpdateTrigger() retrievalConstraintTrigger {
	const oldKey = "(SELECT source_key FROM items WHERE id = OLD.item_id)"
	const newKey = "(SELECT source_key FROM items WHERE id = NEW.item_id)"
	oldProjected := projectedEnrichmentRoleSQL("OLD.role")
	newProjected := projectedEnrichmentRoleSQL("NEW.role")
	return retrievalConstraintTrigger{
		name:  "trg_retrieval_item_enrichments_dirty_update",
		table: "item_enrichments",
		sql: fmt.Sprintf(`CREATE TRIGGER trg_retrieval_item_enrichments_dirty_update
			AFTER UPDATE OF item_id, role, text ON item_enrichments
			WHEN (OLD.item_id IS NOT NEW.item_id OR OLD.role IS NOT NEW.role OR OLD.text IS NOT NEW.text)
				AND ((%s) OR (%s))
			BEGIN
				UPDATE retrieval_state
				SET projection_work_revision = projection_work_revision + 1, updated_at = %s
				WHERE singleton = 1;
				%s
				%s
				%s
				%s
			END`,
			oldProjected, newProjected, retrievalDirtyTimestampSQL,
			projectionGenerationInvalidationSQL("item", oldKey, oldProjected),
			projectionGenerationInvalidationSQL("item", newKey, newProjected),
			projectionLedgerUpsertSQL("item", oldKey, oldProjected),
			projectionLedgerUpsertSQL("item", newKey, newProjected)),
	}
}

func projectionDirtySQL(kind, key string) string {
	return fmt.Sprintf(`UPDATE retrieval_state
				SET projection_work_revision = projection_work_revision + 1, updated_at = %s
				WHERE singleton = 1;
				%s
				%s`,
		retrievalDirtyTimestampSQL,
		projectionGenerationInvalidationSQL(kind, key, "1"),
		projectionLedgerUpsertSQL(kind, key, "1"))
}

func projectionGenerationInvalidationSQL(kind, key, when string) string {
	return fmt.Sprintf(`UPDATE retrieval_index_generations
				SET build_status = 'stale', active = 0, activated_at = '', updated_at = %s
				WHERE (%s)
					AND build_status != 'stale'
					AND profile_id IN (
						SELECT DISTINCT embedding.profile_id
						FROM retrieval_embeddings embedding
						JOIN retrieval_chunks chunk ON chunk.chunk_id = embedding.chunk_id
						WHERE chunk.parent_kind = '%s' AND chunk.parent_source_key = %s
					);`, retrievalDirtyTimestampSQL, when, kind, key)
}

func projectionLedgerUpsertSQL(kind, key, when string) string {
	return fmt.Sprintf(`INSERT INTO retrieval_parent_projections (
					parent_kind, parent_source_key, projection_version, chunker_version,
					status, reason, dirty_at, dirty_revision, updated_at
				)
				SELECT '%s', %s, '%s', '%s',
					'pending', '', %s, projection_work_revision, %s
				FROM retrieval_state
				WHERE singleton = 1 AND (%s) AND trim(COALESCE(%s, '')) != ''
				ON CONFLICT(parent_kind, parent_source_key) DO UPDATE SET
					projection_version = excluded.projection_version,
					chunker_version = excluded.chunker_version,
					status = excluded.status,
					reason = '',
					dirty_at = excluded.dirty_at,
					dirty_revision = excluded.dirty_revision,
					updated_at = excluded.updated_at;`,
		kind, key, retrievalchunk.ProjectionVersion, retrievalchunk.Version,
		retrievalDirtyTimestampSQL, retrievalDirtyTimestampSQL, when, key)
}
