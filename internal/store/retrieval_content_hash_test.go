package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/retrievalchunk"
)

func TestProjectionDirtyTriggerV17HistoricalFingerprints(t *testing.T) {
	// These hashes freeze migration 17's normalized trigger SQL even though its
	// definitions still share construction helpers with the current migration.
	// A future helper change must fork the historical definitions instead of
	// silently changing schema identity for databases that already applied v17.
	want := map[string]string{
		"trg_retrieval_items_dirty_insert":            "f7c4df8dbe8886a56ca20dd8824c8992d9d3ab8e6d5131e29a9d4917818ae774",
		"trg_retrieval_items_dirty_update":            "b00fd79c061d52400e746cc3dcc2c9cd0fb79dca5b7344312b75fa940c1d2ca5",
		"trg_retrieval_items_dirty_delete":            "24d42c0570e9b0b70baa0434c6202034d58ef6da23488eb2ca9df354e491b417",
		"trg_retrieval_sources_dirty_insert":          "2a636afe397f8f98a6e58e5c9ba3fa351f91540baf345fdd331933a6b00ec721",
		"trg_retrieval_sources_dirty_update":          "da029ceab159800f0bf9584415afc52af8e3c30e73ac30eec2f0ede480825cf6",
		"trg_retrieval_sources_dirty_delete":          "bfeafe88a5adbba6e3740bbc189afa9a6609f8eefba7443847f980d372f2ba2d",
		"trg_retrieval_item_enrichments_dirty_insert": "450b7604d4c92bc1cd8bfc2717de57573855f1cdc2c9b814b5dc2f201c914522",
		"trg_retrieval_item_enrichments_dirty_update": "13f06bba5a28a3b59a7e4c295bcc06f2ac8b13cd0b222860306615f82c5c52f6",
		"trg_retrieval_item_enrichments_dirty_delete": "f7e2102cae4c5ce55c7545a64b23f83f03e186a1fa88f2d06f90701d4d26d368",
	}
	if len(semanticProjectionDirtyTriggersV17) != len(want) {
		t.Fatalf("historical v17 trigger count=%d want %d", len(semanticProjectionDirtyTriggersV17), len(want))
	}
	for _, trigger := range semanticProjectionDirtyTriggersV17 {
		got := fmt.Sprintf("%x", sha256.Sum256([]byte(normalizeSQLiteTriggerSQL(trigger.sql))))
		if want[trigger.name] != got {
			t.Errorf("historical v17 trigger %s fingerprint=%s", trigger.name, got)
		}
	}
}

func TestProjectionDirtyTriggerV18RepairsGenuineV17ContentHashTriggers(t *testing.T) {
	path, v17Revision := projectionDirtyTriggerV17DatabaseForContentHashTest(t)
	if err := ValidateRestorableDatabase(t.Context(), path); err != nil {
		t.Fatalf("validate genuine v17 database before repair: %v", err)
	}

	st := openStoreAtPath(t, path)
	defer func() { _ = st.Close() }()

	var migrationCount int
	if err := st.db.QueryRow(`
		SELECT COUNT(*)
		FROM schema_migrations
		WHERE version = ? AND name = ?`, semanticProjectionDirtyRepairVersion, semanticProjectionDirtyRepairName).Scan(&migrationCount); err != nil {
		t.Fatalf("read migration 18 metadata: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("migration 18 row count=%d want 1", migrationCount)
	}
	for _, trigger := range semanticProjectionDirtyTriggers {
		var table, definition string
		if err := st.db.QueryRow(`
			SELECT tbl_name, sql FROM sqlite_master
			WHERE type='trigger' AND name=?`, trigger.name).Scan(&table, &definition); err != nil {
			t.Fatalf("read repaired trigger %s: %v", trigger.name, err)
		}
		if table != trigger.table || normalizeSQLiteTriggerSQL(definition) != normalizeSQLiteTriggerSQL(trigger.sql) {
			t.Fatalf("migration 18 did not install canonical trigger %s", trigger.name)
		}
	}
	var status string
	var dirtyRevision, projectedRevision int64
	if err := st.db.QueryRow(`
		SELECT status, dirty_revision, projected_revision
		FROM retrieval_parent_projections
		WHERE parent_kind='source' AND parent_source_key='source:v17-content-hash'`).Scan(
		&status, &dirtyRevision, &projectedRevision); err != nil {
		t.Fatalf("read repaired projection ledger: %v", err)
	}
	if status != string(RetrievalProjectionPending) || dirtyRevision <= v17Revision || projectedRevision != v17Revision {
		t.Fatalf("repaired projection state=%s dirty=%d projected=%d want pending dirty>%d projected=%d",
			status, dirtyRevision, projectedRevision, v17Revision, v17Revision)
	}
	var staging, chunks int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_projection_staging`).Scan(&staging); err != nil {
		t.Fatalf("count repaired staging rows: %v", err)
	}
	if err := st.db.QueryRow(`
		SELECT COUNT(*) FROM retrieval_chunks
		WHERE parent_kind='source' AND parent_source_key='source:v17-content-hash'`).Scan(&chunks); err != nil {
		t.Fatalf("count preserved chunks: %v", err)
	}
	var evidence string
	if err := st.db.QueryRow(`SELECT extracted_text FROM sources WHERE source_key='source:v17-content-hash'`).Scan(&evidence); err != nil {
		t.Fatalf("read preserved evidence: %v", err)
	}
	if staging != 0 || chunks != 1 || evidence != "body" {
		t.Fatalf("migration repair staging=%d chunks=%d evidence=%q want 0/1/body", staging, chunks, evidence)
	}
	var generationStatus string
	var generationActive int
	var activatedAt string
	if err := st.db.QueryRow(`
		SELECT build_status, active, activated_at
		FROM retrieval_index_generations
		WHERE generation_id='generation:v17-content-hash'`).Scan(
		&generationStatus, &generationActive, &activatedAt); err != nil {
		t.Fatalf("read repaired generation: %v", err)
	}
	if generationStatus != string(RetrievalGenerationStale) || generationActive != 0 || activatedAt != "" {
		t.Fatalf("generation state=%s active=%d activated_at=%q want stale/0/empty", generationStatus, generationActive, activatedAt)
	}

	before := projectionRevisionForTest(t, st)
	if _, err := st.db.Exec(`UPDATE sources SET content_hash='raw-hash-v2' WHERE source_key='source:v17-content-hash'`); err != nil {
		t.Fatalf("update provenance hash after v18 repair: %v", err)
	}
	if got := projectionRevisionForTest(t, st); got != before {
		t.Fatalf("v18 content-hash-only update revision=%d want unchanged %d", got, before)
	}
}

func TestProjectionDirtyTriggerV18BackfillsParentCreatedWhileTriggersAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open database directly: %v", err)
	}
	for _, trigger := range semanticProjectionDirtyTriggers {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
			_ = db.Close()
			t.Fatalf("drop projection dirty trigger %s: %v", trigger.name, err)
		}
	}
	if _, err := db.Exec(`
		DELETE FROM schema_migrations WHERE version > 16;
		PRAGMA user_version = 16`); err != nil {
		_ = db.Close()
		t.Fatalf("stamp database at migration 16: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, title,
			extracted_text, content_hash, note_path, created_at, updated_at
		) VALUES ('source:trigger-gap', 'https://example.com/trigger-gap',
			'https://example.com/trigger-gap', 'article', 'gap', 'body',
			'raw-hash', 'sources/trigger-gap.md', ?, ?)`, now, now); err != nil {
		_ = db.Close()
		t.Fatalf("insert source while projection triggers are absent: %v", err)
	}
	var before int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM retrieval_parent_projections
		WHERE parent_kind='source' AND parent_source_key='source:trigger-gap'`).Scan(&before); err != nil {
		_ = db.Close()
		t.Fatalf("count pre-repair projection ledger rows: %v", err)
	}
	if before != 0 {
		_ = db.Close()
		t.Fatalf("pre-repair projection ledger rows=%d want 0", before)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close migration-16 database: %v", err)
	}

	st = openStoreAtPath(t, path)
	defer func() { _ = st.Close() }()
	var status string
	var dirtyRevision int64
	if err := st.db.QueryRow(`
		SELECT status, dirty_revision
		FROM retrieval_parent_projections
		WHERE parent_kind='source' AND parent_source_key='source:trigger-gap'`).Scan(
		&status, &dirtyRevision); err != nil {
		t.Fatalf("read repaired trigger-gap projection ledger: %v", err)
	}
	if status != string(RetrievalProjectionPending) || dirtyRevision <= 0 {
		t.Fatalf("repaired trigger-gap projection state=%s dirty=%d want pending positive revision", status, dirtyRevision)
	}
}

func projectionDirtyTriggerV17DatabaseForContentHashTest(t *testing.T) (string, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := st.db.Exec(`
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, title,
			extracted_text, content_hash, note_path, created_at, updated_at
		) VALUES ('source:v17-content-hash', 'https://example.com/v17-content-hash',
			'https://example.com/v17-content-hash', 'article', 'title', 'body',
			'raw-hash-v1', 'source-v17-content-hash.md', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed current source before v17 downgrade: %v", err)
	}
	v17Revision := markProjectionCurrentForTest(t, st, "source", "source:v17-content-hash")
	chunk := testRetrievalChunk("chunk:v17-content-hash", "source", "source:v17-content-hash", 0, "chunk-text-hash", "body")
	if _, err := st.ReplaceRetrievalChunks(t.Context(), "source", "source:v17-content-hash", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("seed current chunk before v17 downgrade: %v", err)
	}
	if _, err := st.db.Exec(`
		INSERT INTO retrieval_projection_staging (
			work_id, dirty_revision, parent_kind, parent_source_key, projection_hash,
			section_key, next_boundary, chunk_id, chunk_json, occurrence_json, created_at, updated_at
		) VALUES ('work:v17-content-hash', ?, 'source', 'source:v17-content-hash',
			'old-parent-hash', 'source:extract', 4, '', '', '', ?, ?)`, v17Revision, now, now); err != nil {
		t.Fatalf("seed v17 staging row: %v", err)
	}
	if err := st.PutRetrievalIndexGeneration(t.Context(), RetrievalIndexGenerationRow{
		GenerationID: "generation:v17-content-hash", ProfileID: "profile:v17-content-hash",
		Backend: "exact", BackendVersion: "v1", Dimensions: 2, DistanceMetric: "cosine",
		BuildStatus: RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatalf("seed v17 active generation: %v", err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_index_generations SET active=1,activated_at=? WHERE generation_id='generation:v17-content-hash'`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed explicit v17 active generation state: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open database directly: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, trigger := range semanticProjectionDirtyTriggersV17 {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
			t.Fatalf("drop current projection dirty trigger %s: %v", trigger.name, err)
		}
		if _, err := db.Exec(trigger.sql); err != nil {
			t.Fatalf("install historical v17 projection dirty trigger %s: %v", trigger.name, err)
		}
	}
	if _, err := db.Exec(`
		DELETE FROM schema_migrations WHERE version > 17;
		PRAGMA user_version = 17`); err != nil {
		t.Fatalf("stamp genuine v17 database: %v", err)
	}
	return path, v17Revision
}

func TestRawContentHashOnlyUpdateDoesNotDirtySemanticProjection(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	tests := []struct {
		kind       string
		sourceKey  string
		seed       func(*testing.T, *Store, string)
		hashUpdate string
		projected  string
	}{
		{
			kind: "item", sourceKey: "item:content-hash-provenance", seed: seedPurgeItem,
			hashUpdate: `UPDATE items SET content_hash='item-content-hash-v2' WHERE source_key=?`,
			projected:  `UPDATE items SET title='Actually changed title' WHERE source_key=?`,
		},
		{
			kind: "source", sourceKey: "source:content-hash-provenance", seed: seedRetrievalSource,
			hashUpdate: `UPDATE sources SET content_hash='source-content-hash-v2' WHERE source_key=?`,
			projected:  `UPDATE sources SET title='Actually changed title' WHERE source_key=?`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			tt.seed(t, st, tt.sourceKey)
			markProjectionCurrentForTest(t, st, tt.kind, tt.sourceKey)
			before := readProjectionRevisionStatusForContentHashTest(t, st, tt.kind, tt.sourceKey)

			if _, err := st.db.ExecContext(ctx, tt.hashUpdate, tt.sourceKey); err != nil {
				t.Fatalf("update raw content_hash: %v", err)
			}
			afterHash := readProjectionRevisionStatusForContentHashTest(t, st, tt.kind, tt.sourceKey)
			if afterHash != before {
				t.Fatalf("content-hash-only update changed projection state: before=%+v after=%+v", before, afterHash)
			}

			if _, err := st.db.ExecContext(ctx, tt.projected, tt.sourceKey); err != nil {
				t.Fatalf("update projected field: %v", err)
			}
			afterProjected := readProjectionRevisionStatusForContentHashTest(t, st, tt.kind, tt.sourceKey)
			if afterProjected.WorkRevision != before.WorkRevision+1 ||
				afterProjected.DirtyRevision != before.DirtyRevision+1 ||
				afterProjected.ProjectedRevision != before.ProjectedRevision ||
				afterProjected.Status != string(RetrievalProjectionPending) {
				t.Fatalf("projected-field update state=%+v want newer pending revision after %+v", afterProjected, before)
			}
		})
	}
}

type contentHashProjectionState struct {
	WorkRevision      int64
	Status            string
	DirtyRevision     int64
	ProjectedRevision int64
}

func readProjectionRevisionStatusForContentHashTest(t *testing.T, st *Store, kind, sourceKey string) contentHashProjectionState {
	t.Helper()
	var got contentHashProjectionState
	if err := st.db.QueryRow(`SELECT projection_work_revision FROM retrieval_state WHERE singleton=1`).Scan(&got.WorkRevision); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`
		SELECT status, dirty_revision, projected_revision
		FROM retrieval_parent_projections
		WHERE parent_kind=? AND parent_source_key=?`, kind, sourceKey).Scan(
		&got.Status, &got.DirtyRevision, &got.ProjectedRevision); err != nil {
		t.Fatal(err)
	}
	return got
}
