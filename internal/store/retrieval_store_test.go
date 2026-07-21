package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrievalchunk"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestReplaceRetrievalChunksReusesUnchangedEmbeddings(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	initial := []retrievalchunk.Chunk{
		testRetrievalChunk("chunk-a", "item", "item:one", 0, "hash-a", "alpha"),
		testRetrievalChunk("chunk-b", "item", "item:one", 1, "hash-b", "bravo"),
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", initial); err != nil {
		t.Fatalf("initial replacement: %v", err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:one")
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding("chunk-a", "profile-a", "hash-a")); err != nil {
		t.Fatalf("put unchanged embedding: %v", err)
	}
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding("chunk-b", "profile-a", "hash-b")); err != nil {
		t.Fatalf("put replaced embedding: %v", err)
	}

	replacement := []retrievalchunk.Chunk{
		initial[0],
		testRetrievalChunk("chunk-c", "item", "item:one", 1, "hash-c", "charlie"),
	}
	result, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", replacement)
	if err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
	if result.Reused != 1 || result.Created != 1 || result.Deleted != 1 {
		t.Fatalf("replace result = %+v, want reused=1 created=1 deleted=1", result)
	}

	ready, err := st.ListReadyEmbeddings(ctx, "profile-a", 10)
	if err != nil {
		t.Fatalf("list ready embeddings: %v", err)
	}
	if len(ready) != 1 || ready[0].ChunkID != "chunk-a" {
		t.Fatalf("ready embeddings = %+v, want unchanged chunk-a only", ready)
	}
}

func TestReplaceRetrievalChunksReportsMetadataUpdateAndKeepsEmbedding(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunk := testRetrievalChunk("chunk-a", "item", "item:one", 0, "hash-a", "alpha")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:one")
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding("chunk-a", "profile-a", "hash-a")); err != nil {
		t.Fatal(err)
	}
	chunk.Heading = "Updated heading"
	result, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", []retrievalchunk.Chunk{chunk})
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated != 1 || result.Reused != 0 || result.Created != 0 || result.Deleted != 0 {
		t.Fatalf("replace result=%+v", result)
	}
	stored, err := st.GetRetrievalChunk(ctx, chunk.ID)
	if err != nil || stored.Heading != chunk.Heading {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	ready, err := st.ListReadyEmbeddings(ctx, "profile-a", 10)
	if err != nil || len(ready) != 1 || ready[0].ChunkID != chunk.ID {
		t.Fatalf("ready=%+v err=%v", ready, err)
	}
}

func TestListReadyEmbeddingsProjectsCurrentSourceTypeAndSectionOrdinal(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedPurgeItem(t, st, "item:projection")
	seedRetrievalSource(t, st, "src:projection")
	item := testRetrievalChunk("item-projection", "item", "item:projection", 0, "item-hash", "item")
	item.SectionOrdinal = 3
	source := testRetrievalChunk("source-projection", "source", "src:projection", 0, "source-hash", "source")
	source.SectionOrdinal = 7
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:projection", []retrievalchunk.Chunk{item}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "source", "src:projection", []retrievalchunk.Chunk{source}); err != nil {
		t.Fatal(err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:projection")
	markProjectionCurrentForTest(t, st, "source", "src:projection")
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding(item.ID, "projection-profile", item.TextHash)); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding(source.ID, "projection-profile", source.TextHash)); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListReadyEmbeddings(ctx, "projection-profile", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]RetrievalEmbeddingRow{}
	for _, row := range rows {
		got[row.ChunkID] = row
	}
	if got[item.ID].SourceType != "apple_note" || got[item.ID].SectionOrdinal != 3 || got[source.ID].SourceType != "article" || got[source.ID].SectionOrdinal != 7 ||
		got[item.ID].ProjectionVersion != retrievalchunk.ProjectionVersion || got[item.ID].ChunkerVersion != retrievalchunk.Version ||
		got[source.ID].ProjectionVersion != retrievalchunk.ProjectionVersion || got[source.ID].ChunkerVersion != retrievalchunk.Version {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestReplaceRetrievalChunksRollsBackWholeParent(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	original := testRetrievalChunk("original", "item", "item:one", 0, "old-hash", "old text")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", []retrievalchunk.Chunk{original}); err != nil {
		t.Fatalf("seed original chunks: %v", err)
	}
	collision := testRetrievalChunk("collision", "source", "source:other", 0, "collision-hash", "other")
	if _, err := st.ReplaceRetrievalChunks(ctx, "source", "source:other", []retrievalchunk.Chunk{collision}); err != nil {
		t.Fatalf("seed colliding chunk: %v", err)
	}

	_, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", []retrievalchunk.Chunk{
		testRetrievalChunk("new", "item", "item:one", 0, "new-hash", "new text"),
		testRetrievalChunk("collision", "item", "item:one", 1, "collision-hash", "collision"),
	})
	if err == nil {
		t.Fatal("replacement with a cross-parent chunk ID collision unexpectedly succeeded")
	}

	rows, err := st.db.QueryContext(ctx, `SELECT chunk_id, text FROM retrieval_chunks WHERE parent_kind = ? AND parent_source_key = ? ORDER BY ordinal`, "item", "item:one")
	if err != nil {
		t.Fatalf("query chunks after rollback: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatal("original chunk disappeared after failed replacement")
	}
	var chunkID, text string
	if err := rows.Scan(&chunkID, &text); err != nil {
		t.Fatalf("scan original chunk: %v", err)
	}
	if chunkID != "original" || text != "old text" || rows.Next() {
		t.Fatalf("chunks after rollback = first (%q, %q), extra=%v", chunkID, text, rows.Next())
	}
}

func TestRetrievalProfilesCoexist(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	chunk := testRetrievalChunk("chunk-a", "item", "item:one", 0, "hash-a", "alpha")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:one")
	for _, profile := range []string{"profile-a", "profile-b"} {
		if err := st.PutRetrievalEmbedding(ctx, testEmbedding("chunk-a", profile, "hash-a")); err != nil {
			t.Fatalf("put embedding %s: %v", profile, err)
		}
	}
	for _, profile := range []string{"profile-a", "profile-b"} {
		rows, err := st.ListReadyEmbeddings(ctx, profile, 10)
		if err != nil {
			t.Fatalf("list profile %s: %v", profile, err)
		}
		if len(rows) != 1 || rows[0].ProfileID != profile {
			t.Fatalf("profile %s rows = %+v", profile, rows)
		}
	}
}

func TestOnlyCompletedGenerationCanActivate(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "building", ProfileID: "profile-a", Backend: "hnsw", BackendVersion: "1",
		Dimensions: 2, DistanceMetric: "cosine", BuildStatus: RetrievalGenerationBuilding,
	}); err != nil {
		t.Fatalf("put generation: %v", err)
	}
	if err := st.ActivateRetrievalIndexGeneration(ctx, "building"); err == nil {
		t.Fatal("activated an incomplete generation")
	}
	var active int
	if err := st.db.QueryRow(`SELECT active FROM retrieval_index_generations WHERE generation_id = 'building'`).Scan(&active); err != nil {
		t.Fatalf("read generation: %v", err)
	}
	if active != 0 {
		t.Fatalf("building generation active = %d, want 0", active)
	}
}

func TestCompletedGenerationActivationFailsClosedWithoutMembershipProvenance(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunk := testRetrievalChunk("generation-chunk", "item", "item:generations", 0, "generation-hash", "alpha")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:generations", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding(chunk.ID, "profile-a", chunk.TextHash)); err != nil {
		t.Fatal(err)
	}

	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "generation-a", ProfileID: "profile-a", Backend: "exact", BackendVersion: "v1",
		Dimensions: 2, DistanceMetric: "cosine", IndexedChunkCount: 1, BuildStatus: RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatalf("put generation: %v", err)
	}
	err := st.ActivateRetrievalIndexGeneration(ctx, "generation-a")
	if !errors.Is(err, ErrRetrievalGenerationMembershipUnproven) {
		t.Fatalf("activate generation error=%v, want ErrRetrievalGenerationMembershipUnproven", err)
	}
	var active int
	if err := st.db.QueryRow(`SELECT active FROM retrieval_index_generations WHERE generation_id='generation-a'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("generation active=%d after rejected activation", active)
	}
	assertProfileAggregatesForTest(t, st, "profile-a", "", 0, 1, 0)
	err = st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "generation-direct-active", ProfileID: "profile-a", Backend: "exact", BackendVersion: "v1",
		Dimensions: 2, DistanceMetric: "cosine", IndexedChunkCount: 1,
		BuildStatus: RetrievalGenerationCompleted, Active: true,
	})
	if !errors.Is(err, ErrRetrievalGenerationMembershipUnproven) {
		t.Fatalf("direct active generation put error=%v, want ErrRetrievalGenerationMembershipUnproven", err)
	}
}

func TestEmbeddingWriteAndChunkInvalidationStaleAffectedGenerations(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunk := testRetrievalChunk("chunk-a", "item", "item:one", 0, "hash-a", "alpha")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding("chunk-a", "profile-a", "hash-a")); err != nil {
		t.Fatalf("put initial embedding: %v", err)
	}
	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "generation-a", ProfileID: "profile-a", Backend: "hnsw", BackendVersion: "1",
		Dimensions: 2, DistanceMetric: "cosine", BuildStatus: RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatalf("put generation: %v", err)
	}
	seedActiveRetrievalGenerationForTest(t, st, "generation-a")
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding("chunk-a", "profile-a", "hash-a")); err != nil {
		t.Fatalf("rewrite unchanged embedding: %v", err)
	}
	var unchangedStatus string
	var unchangedActive int
	if err := st.db.QueryRow(`SELECT build_status, active FROM retrieval_index_generations WHERE generation_id = 'generation-a'`).Scan(&unchangedStatus, &unchangedActive); err != nil {
		t.Fatalf("read generation after unchanged embedding: %v", err)
	}
	if unchangedStatus != string(RetrievalGenerationCompleted) || unchangedActive != 1 {
		t.Fatalf("unchanged embedding invalidated generation: status %q active %d", unchangedStatus, unchangedActive)
	}
	changedEmbedding := testEmbedding("chunk-a", "profile-a", "hash-a")
	changedEmbedding.VectorBytes = embedding.EncodeDenseF32([]float32{0, 1})
	if err := st.PutRetrievalEmbedding(ctx, changedEmbedding); err != nil {
		t.Fatalf("change embedding: %v", err)
	}
	assertGenerationActiveForTest(t, st, "generation-a")
	assertProfileAggregatesForTest(t, st, "profile-a", "generation-a", 1, 1, 1)

	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "generation-b", ProfileID: "profile-a", Backend: "hnsw", BackendVersion: "1",
		Dimensions: 2, DistanceMetric: "cosine", BuildStatus: RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatalf("put replacement generation: %v", err)
	}
	seedActiveRetrievalGenerationForTest(t, st, "generation-b")
	changed := testRetrievalChunk("chunk-b", "item", "item:one", 0, "hash-b", "bravo")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", []retrievalchunk.Chunk{changed}); err != nil {
		t.Fatalf("replace embedded chunk: %v", err)
	}
	assertRetrievalGenerationStale(t, st, "generation-b")
}

func TestOpenReadOnlyPreRetrievalSchemaDoesNotWrite(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "brain.db")
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, source_key TEXT NOT NULL)`); err != nil {
		t.Fatalf("create legacy items table: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 12`); err != nil {
		t.Fatalf("set legacy user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open legacy store read-only: %v", err)
	}
	available, err := ro.RetrievalAvailable(context.Background())
	if err != nil {
		t.Fatalf("check retrieval availability: %v", err)
	}
	if available {
		t.Fatal("legacy store unexpectedly reports retrieval tables available")
	}
	if err := ro.Close(); err != nil {
		t.Fatalf("close read-only store: %v", err)
	}

	db, err = sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("reopen legacy db: %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'retrieval_%'`).Scan(&count); err != nil {
		t.Fatalf("count retrieval objects: %v", err)
	}
	if count != 0 {
		t.Fatalf("read-only open created %d retrieval objects", count)
	}
}

func TestOpenReadOnlyPreSemanticFoundationDoesNotWrite(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "brain.db")
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open pre-semantic-foundation database: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE items (id INTEGER PRIMARY KEY, source_key TEXT NOT NULL);
		CREATE TABLE sources (id INTEGER PRIMARY KEY, source_key TEXT NOT NULL);
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL);
		PRAGMA user_version = 15`); err != nil {
		_ = db.Close()
		t.Fatalf("create pre-semantic-foundation schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close pre-semantic-foundation database: %v", err)
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open pre-semantic-foundation database read-only: %v", err)
	}
	if err := ro.Close(); err != nil {
		t.Fatalf("close read-only database: %v", err)
	}

	db, err = sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("reopen pre-semantic-foundation database: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, table := range []string{
		"retrieval_state",
		"retrieval_parent_projections",
		"retrieval_chunk_occurrences",
		"retrieval_projection_staging",
		"retrieval_embedding_profiles",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("look up %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("read-only open created %s", table)
		}
	}
	var userVersion int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read user version: %v", err)
	}
	if userVersion != 15 {
		t.Fatalf("user version = %d, want 15", userVersion)
	}
}

func TestPurgeItemIndexedContentDeletesRetrievalState(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedPurgeItem(t, st, "apple-note:one")
	var itemID int64
	if err := st.db.QueryRow(`SELECT id FROM items WHERE source_key = 'apple-note:one'`).Scan(&itemID); err != nil {
		t.Fatalf("load purge item ID: %v", err)
	}
	seedItemEnrichmentRows(t, st, itemID, map[string]string{
		model.ItemEnrichmentRoleSummary:          "private mirror summary",
		model.ItemEnrichmentRoleOCR:              "private mirror OCR",
		model.ItemEnrichmentRoleXMediaTranscript: "private mirror transcript",
	})
	chunk := testRetrievalChunk("chunk-a", "item", "apple-note:one", 0, "hash-a", "private")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "apple-note:one", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding("chunk-a", "profile-a", "hash-a")); err != nil {
		t.Fatalf("put embedding: %v", err)
	}
	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "generation-a", ProfileID: "profile-a", Backend: "hnsw", BackendVersion: "1",
		Dimensions: 2, DistanceMetric: "cosine", BuildStatus: RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatalf("put generation: %v", err)
	}
	seedActiveRetrievalGenerationForTest(t, st, "generation-a")

	purged, err := st.PurgeItemIndexedContent(ctx, "apple-note:one", `{"purged":true}`)
	if err != nil {
		t.Fatalf("purge item: %v", err)
	}
	if !purged {
		t.Fatal("purge returned false")
	}
	for _, table := range []string{"retrieval_chunks", "retrieval_embeddings"} {
		var count int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", table, count)
		}
	}
	var status string
	var active int
	if err := st.db.QueryRow(`SELECT build_status, active FROM retrieval_index_generations WHERE generation_id = 'generation-a'`).Scan(&status, &active); err != nil {
		t.Fatalf("read affected generation: %v", err)
	}
	if status != string(RetrievalGenerationStale) || active != 0 {
		t.Fatalf("affected generation = status %q active %d, want stale inactive", status, active)
	}
	assertProfileAggregatesForTest(t, st, "profile-a", "", 0, 0, 0)
	var enrichmentCount int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM item_enrichments WHERE item_id = ?`, itemID).Scan(&enrichmentCount); err != nil {
		t.Fatalf("count purged item enrichments: %v", err)
	}
	if enrichmentCount != 0 {
		t.Fatalf("purge left %d authoritative item enrichment rows", enrichmentCount)
	}
	parents, err := st.ListRetrievalParents(ctx, "", 10)
	if err != nil {
		t.Fatalf("project parents after purge: %v", err)
	}
	if len(parents) != 1 || len(parents[0].Sections) != 0 {
		t.Fatalf("purged item remains projectable: %+v", parents)
	}
}

func TestRetrievalProjectionPagesParentsInOneQuery(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedPurgeItem(t, st, "item:a")
	seedPurgeItem(t, st, "item:c")

	parents, err := st.ListRetrievalParents(context.Background(), "", 1)
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(parents) != 1 || parents[0].SourceKey != "item:a" || parents[0].Kind != "item" {
		t.Fatalf("first page = %+v", parents)
	}
	parents, err = st.ListRetrievalParents(context.Background(), parents[0].SourceKey, 2)
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(parents) != 1 || parents[0].SourceKey != "item:c" {
		t.Fatalf("second page = %+v", parents)
	}
}

func TestRetrievalProjectionKeyPageReturnsBothParentKindsForSharedSourceKey(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedPurgeItem(t, st, "shared:key")
	seedPurgeItem(t, st, "later:key")
	seedRetrievalSource(t, st, "shared:key")

	parents, err := st.ListRetrievalParents(context.Background(), "", 1)
	if err != nil {
		t.Fatalf("list shared-key page: %v", err)
	}
	if len(parents) != 1 || parents[0].SourceKey != "later:key" {
		t.Fatalf("first key page = %+v, want later:key", parents)
	}
	parents, err = st.ListRetrievalParents(context.Background(), "later:key", 1)
	if err != nil {
		t.Fatalf("list collision key page: %v", err)
	}
	if len(parents) != 2 || parents[0].SourceKey != "shared:key" || parents[1].SourceKey != "shared:key" || parents[0].Kind == parents[1].Kind {
		t.Fatalf("shared-key page = %+v, want both item and source", parents)
	}
}

func TestRetrievalProjectionClosesPageRowsBeforeChunkWrites(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	st.db.SetMaxOpenConns(1)
	seedPurgeItem(t, st, "shared:key")
	seedRetrievalSource(t, st, "shared:key")

	parents, err := st.ListRetrievalParents(context.Background(), "", 1)
	if err != nil {
		t.Fatalf("list parent page: %v", err)
	}
	if len(parents) != 2 {
		t.Fatalf("parent page length = %d, want atomic item/source pair", len(parents))
	}
	chunks, err := retrievalchunk.Build(parents[0], retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatalf("build first parent: %v", err)
	}
	writeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := st.ReplaceRetrievalChunks(writeCtx, parents[0].Kind, parents[0].SourceKey, chunks); err != nil {
		t.Fatalf("write after parent page rows closed: %v", err)
	}
}

func TestDirtyRevisionAllocationIsMonotonicAndTransactional(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedPurgeItem(t, st, "item:revision")
	ctx := context.Background()

	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := allocateRetrievalParentDirtyTx(ctx, tx, "item", "item:revision")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx, err = st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := allocateRetrievalParentDirtyTx(ctx, tx, "item", "item:revision")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if rolledBack != first+1 {
		t.Fatalf("rolled-back allocation=%d want %d", rolledBack, first+1)
	}
	watermark, err := st.ProjectionWorkRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if watermark != first {
		t.Fatalf("watermark after rollback=%d want %d", watermark, first)
	}

	tx, err = st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := allocateRetrievalParentDirtyTx(ctx, tx, "item", "item:revision")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if second != first+1 {
		t.Fatalf("second committed revision=%d want %d", second, first+1)
	}
	var status string
	var dirty, projected int64
	if err := st.db.QueryRow(`SELECT status, dirty_revision, projected_revision FROM retrieval_parent_projections WHERE parent_kind='item' AND parent_source_key='item:revision'`).Scan(&status, &dirty, &projected); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || dirty != second || projected != 0 {
		t.Fatalf("parent state=(%s,%d,%d)", status, dirty, projected)
	}
}

func TestListDirtyRetrievalParentsIsDeterministicThroughWatermarkAndIncludesCleanup(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:deleted")
	seedRetrievalSource(t, st, "source:ineligible")
	seedRetrievalSource(t, st, "source:later")
	deletedRevision := dirtyRetrievalParentForTest(t, st, "source", "source:deleted", func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM sources WHERE source_key='source:deleted'`)
		return err
	})
	ineligibleRevision := dirtyRetrievalParentForTest(t, st, "source", "source:ineligible", func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sources SET note_path='' WHERE source_key='source:ineligible'`)
		return err
	})
	watermark, err := st.ProjectionWorkRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if watermark != ineligibleRevision {
		t.Fatalf("watermark=%d want %d", watermark, ineligibleRevision)
	}
	dirtyRetrievalParentForTest(t, st, "source", "source:later", nil)

	work, err := st.ListDirtyRetrievalParents(ctx, watermark, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(work) != 2 || work[0].DirtyRevision != deletedRevision || work[0].Parent.SourceKey != "source:deleted" || work[1].DirtyRevision != ineligibleRevision || work[1].Parent.SourceKey != "source:ineligible" {
		t.Fatalf("work=%+v", work)
	}
	for _, selected := range work {
		if len(selected.Parent.Sections) != 0 {
			t.Fatalf("cleanup parent unexpectedly contains projected sections: %+v", selected)
		}
	}
}

func TestApplyRetrievalProjectionAtomicallyReplacesOccurrencesAndOnlyObsoleteEmbeddings(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:apply")
	if _, err := st.db.Exec(`UPDATE sources SET summary_text='stable summary' WHERE source_key='source:apply'`); err != nil {
		t.Fatal(err)
	}
	dirtyRetrievalParentForTest(t, st, "source", "source:apply", nil)
	initialWork := oneDirtyProjectionWork(t, st)
	initialProjection, err := retrievalchunk.BuildProjection(initialWork.Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(initialProjection.Chunks) != 2 {
		t.Fatalf("initial chunks=%d want 2", len(initialProjection.Chunks))
	}
	if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{
		ParentKind: "source", ParentSourceKey: "source:apply", DirtyRevision: initialWork.DirtyRevision,
		Projection: initialProjection, Status: RetrievalProjectionCurrent,
	}); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range initialProjection.Chunks {
		if err := st.PutRetrievalEmbedding(ctx, testEmbedding(chunk.ID, "profile-a", chunk.TextHash)); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "generation-apply", ProfileID: "profile-a", Backend: "hnsw", BackendVersion: "1",
		Dimensions: 2, DistanceMetric: "cosine", IndexedChunkCount: 2, BuildStatus: RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatal(err)
	}
	seedActiveRetrievalGenerationForTest(t, st, "generation-apply")

	dirtyRetrievalParentForTest(t, st, "source", "source:apply", func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sources SET extracted_text='changed source text', content_hash='changed-hash' WHERE source_key='source:apply'`)
		return err
	})
	nextWork := oneDirtyProjectionWork(t, st)
	nextProjection, err := retrievalchunk.BuildProjection(nextWork.Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	result, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{
		ParentKind: "source", ParentSourceKey: "source:apply", DirtyRevision: nextWork.DirtyRevision,
		Projection: nextProjection, Status: RetrievalProjectionCurrent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated != 1 || result.Created != 1 || result.Deleted != 1 {
		t.Fatalf("replace result=%+v", result)
	}
	var occurrenceCount int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunk_occurrences WHERE parent_kind='source' AND parent_source_key='source:apply'`).Scan(&occurrenceCount); err != nil {
		t.Fatal(err)
	}
	if occurrenceCount != len(nextProjection.Occurrences) {
		t.Fatalf("occurrences=%d want %d", occurrenceCount, len(nextProjection.Occurrences))
	}
	ready, err := st.ListReadyEmbeddings(ctx, "profile-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].ChunkID != sharedProjectionChunkID(t, initialProjection, nextProjection) {
		t.Fatalf("ready embeddings=%+v", ready)
	}
	assertRetrievalGenerationStale(t, st, "generation-apply")
	var projectionHash, status, reason string
	var dirtyRevision, projectedRevision int64
	if err := st.db.QueryRow(`SELECT projection_hash,status,reason,dirty_revision,projected_revision FROM retrieval_parent_projections WHERE parent_kind='source' AND parent_source_key='source:apply'`).Scan(&projectionHash, &status, &reason, &dirtyRevision, &projectedRevision); err != nil {
		t.Fatal(err)
	}
	if projectionHash != nextProjection.ParentHash || status != "current" || reason != "" || projectedRevision != dirtyRevision || projectedRevision != nextWork.DirtyRevision {
		t.Fatalf("projection state=(%q,%q,%q,%d,%d)", projectionHash, status, reason, dirtyRevision, projectedRevision)
	}
}

func TestApplyRetrievalProjectionRollsBackChunksOccurrencesAndState(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:rollback")
	dirtyRetrievalParentForTest(t, st, "source", "source:rollback", nil)
	initialWork := oneDirtyProjectionWork(t, st)
	initialProjection, err := retrievalchunk.BuildProjection(initialWork.Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: "source", ParentSourceKey: "source:rollback", DirtyRevision: initialWork.DirtyRevision, Projection: initialProjection, Status: RetrievalProjectionCurrent}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`CREATE TRIGGER fail_projection_occurrence BEFORE INSERT ON retrieval_chunk_occurrences BEGIN SELECT RAISE(ABORT, 'injected occurrence failure'); END`); err != nil {
		t.Fatal(err)
	}
	dirtyRetrievalParentForTest(t, st, "source", "source:rollback", func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sources SET extracted_text='replacement text' WHERE source_key='source:rollback'`)
		return err
	})
	nextWork := oneDirtyProjectionWork(t, st)
	nextProjection, err := retrievalchunk.BuildProjection(nextWork.Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: "source", ParentSourceKey: "source:rollback", DirtyRevision: nextWork.DirtyRevision, Projection: nextProjection, Status: RetrievalProjectionCurrent}); err == nil || !strings.Contains(err.Error(), "injected occurrence failure") {
		t.Fatalf("apply error=%v", err)
	}
	var oldChunks, oldOccurrences int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunks WHERE parent_kind='source' AND parent_source_key='source:rollback' AND input_content_hash='source-hash'`).Scan(&oldChunks); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunk_occurrences WHERE parent_kind='source' AND parent_source_key='source:rollback'`).Scan(&oldOccurrences); err != nil {
		t.Fatal(err)
	}
	var status string
	var projected int64
	if err := st.db.QueryRow(`SELECT status,projected_revision FROM retrieval_parent_projections WHERE parent_kind='source' AND parent_source_key='source:rollback'`).Scan(&status, &projected); err != nil {
		t.Fatal(err)
	}
	if oldChunks != len(initialProjection.Chunks) || oldOccurrences != len(initialProjection.Occurrences) || status != "pending" || projected != initialWork.DirtyRevision {
		t.Fatalf("rollback chunks=%d occurrences=%d status=%q projected=%d", oldChunks, oldOccurrences, status, projected)
	}
}

func TestApplyRetrievalProjectionRejectsSameTimestampNewerDirtyRevision(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:race")
	dirtyRetrievalParentForTest(t, st, "source", "source:race", nil)
	oldWork := oneDirtyProjectionWork(t, st)
	oldProjection, err := retrievalchunk.BuildProjection(oldWork.Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	var dirtyAt string
	if err := st.db.QueryRow(`SELECT dirty_at FROM retrieval_parent_projections WHERE parent_kind='source' AND parent_source_key='source:race'`).Scan(&dirtyAt); err != nil {
		t.Fatal(err)
	}
	newRevision := dirtyRetrievalParentForTest(t, st, "source", "source:race", func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sources SET extracted_text='newer text' WHERE source_key='source:race'`)
		return err
	})
	if _, err := st.db.Exec(`UPDATE retrieval_parent_projections SET dirty_at=? WHERE parent_kind='source' AND parent_source_key='source:race'`, dirtyAt); err != nil {
		t.Fatal(err)
	}
	_, err = st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: "source", ParentSourceKey: "source:race", DirtyRevision: oldWork.DirtyRevision, Projection: oldProjection, Status: RetrievalProjectionCurrent})
	var stale *RetrievalProjectionStaleWorkError
	if !errors.As(err, &stale) {
		t.Fatalf("apply error=%v want typed stale-work error", err)
	}
	var status string
	var dirty, projected int64
	if err := st.db.QueryRow(`SELECT status,dirty_revision,projected_revision FROM retrieval_parent_projections WHERE parent_kind='source' AND parent_source_key='source:race'`).Scan(&status, &dirty, &projected); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || dirty != newRevision || projected != 0 {
		t.Fatalf("newer work cleared: status=%q dirty=%d projected=%d", status, dirty, projected)
	}
}

func TestApplyRetrievalProjectionRejectsProjectedMutationWithNewDirtyRevision(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:hash-race")
	dirtyRetrievalParentForTest(t, st, "source", "source:hash-race", nil)
	work := oneDirtyProjectionWork(t, st)
	projection, err := retrievalchunk.BuildProjection(work.Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE sources SET extracted_text='changed without a matching dirty write' WHERE source_key='source:hash-race'`); err != nil {
		t.Fatal(err)
	}
	_, err = st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: "source", ParentSourceKey: "source:hash-race", DirtyRevision: work.DirtyRevision, Projection: projection, Status: RetrievalProjectionCurrent})
	var stale *RetrievalProjectionStaleWorkError
	if !errors.As(err, &stale) || stale.Reason != "dirty revision no longer matches" {
		t.Fatalf("apply error=%v stale=%+v", err, stale)
	}
	var status string
	var projected int64
	if err := st.db.QueryRow(`SELECT status,projected_revision FROM retrieval_parent_projections WHERE parent_kind='source' AND parent_source_key='source:hash-race'`).Scan(&status, &projected); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || projected != 0 {
		t.Fatalf("stale hash apply changed state: status=%q projected=%d", status, projected)
	}
}

func TestApplyRetrievalProjectionRejectsUnsupportedStatusWithoutDeletingValidChunks(t *testing.T) {
	for _, status := range []RetrievalProjectionStatus{RetrievalProjectionBlocked, RetrievalProjectionError} {
		t.Run(string(status), func(t *testing.T) {
			st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
			defer func() { _ = st.Close() }()
			ctx := context.Background()
			seedRetrievalSource(t, st, "source:status")
			dirtyRetrievalParentForTest(t, st, "source", "source:status", nil)
			initial := oneDirtyProjectionWork(t, st)
			projection, err := retrievalchunk.BuildProjection(initial.Parent, retrievalchunk.DefaultOptions())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: "source", ParentSourceKey: "source:status", DirtyRevision: initial.DirtyRevision, Projection: projection, Status: RetrievalProjectionCurrent}); err != nil {
				t.Fatal(err)
			}
			newRevision := dirtyRetrievalParentForTest(t, st, "source", "source:status", nil)
			_, err = st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{
				ParentKind: "source", ParentSourceKey: "source:status", DirtyRevision: newRevision,
				Projection: retrievalchunk.Projection{ParentHash: projection.ParentHash, Chunks: make([]retrievalchunk.Chunk, 0), Occurrences: make([]retrievalchunk.Occurrence, 0)},
				Status:     status, Reason: "unsupported terminal state",
			})
			if err == nil || !strings.Contains(err.Error(), "only current and empty retrieval projection applies are supported") {
				t.Fatalf("apply status %q error=%v", status, err)
			}
			var chunks int
			var state string
			var dirty, projected int64
			if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunks WHERE parent_kind='source' AND parent_source_key='source:status'`).Scan(&chunks); err != nil {
				t.Fatal(err)
			}
			if err := st.db.QueryRow(`SELECT status,dirty_revision,projected_revision FROM retrieval_parent_projections WHERE parent_kind='source' AND parent_source_key='source:status'`).Scan(&state, &dirty, &projected); err != nil {
				t.Fatal(err)
			}
			if chunks != len(projection.Chunks) || state != "pending" || dirty != newRevision || projected != initial.DirtyRevision {
				t.Fatalf("malformed status changed DB: chunks=%d state=%q dirty=%d projected=%d", chunks, state, dirty, projected)
			}
		})
	}
}

func TestApplyRetrievalProjectionRejectsMalformedEmptyPayloadWithoutDeletingValidChunks(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:malformed-empty")
	dirtyRetrievalParentForTest(t, st, "source", "source:malformed-empty", nil)
	initial := oneDirtyProjectionWork(t, st)
	projection, err := retrievalchunk.BuildProjection(initial.Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: "source", ParentSourceKey: "source:malformed-empty", DirtyRevision: initial.DirtyRevision, Projection: projection, Status: RetrievalProjectionCurrent}); err != nil {
		t.Fatal(err)
	}
	newRevision := dirtyRetrievalParentForTest(t, st, "source", "source:malformed-empty", nil)
	malformed := retrievalchunk.Projection{
		ParentHash:  projection.ParentHash,
		Chunks:      make([]retrievalchunk.Chunk, 0),
		Occurrences: make([]retrievalchunk.Occurrence, 0),
	}
	_, err = st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{
		ParentKind: "source", ParentSourceKey: "source:malformed-empty", DirtyRevision: newRevision,
		Projection: malformed, Status: RetrievalProjectionEmpty, Reason: "no_chunkable_content",
	})
	if err == nil || !strings.Contains(err.Error(), "payload does not match freshly computed parent projection") {
		t.Fatalf("malformed empty apply error=%v", err)
	}
	_, err = st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{
		ParentKind: "source", ParentSourceKey: "source:malformed-empty", DirtyRevision: newRevision,
		Projection: projection, Status: RetrievalProjectionCurrent, Reason: "unexpected current reason",
	})
	if err == nil || !strings.Contains(err.Error(), "current retrieval projection must not contain a reason") {
		t.Fatalf("malformed current apply error=%v", err)
	}
	var chunks int
	var status string
	var dirty, projected int64
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunks WHERE parent_kind='source' AND parent_source_key='source:malformed-empty'`).Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT status,dirty_revision,projected_revision FROM retrieval_parent_projections WHERE parent_kind='source' AND parent_source_key='source:malformed-empty'`).Scan(&status, &dirty, &projected); err != nil {
		t.Fatal(err)
	}
	if chunks != len(projection.Chunks) || status != "pending" || dirty != newRevision || projected != initial.DirtyRevision {
		t.Fatalf("malformed payload changed DB: chunks=%d status=%q dirty=%d projected=%d", chunks, status, dirty, projected)
	}
}

func TestApplyRetrievalProjectionDirtyWriterReservationRejectsThenReturnsStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	applyStore := openStoreAtPath(t, path)
	defer func() { _ = applyStore.Close() }()
	dirtyStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dirtyStore.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	seedRetrievalSource(t, applyStore, "source:dirty-wins")
	dirtyRetrievalParentForTest(t, applyStore, "source", "source:dirty-wins", nil)
	oldWork := oneDirtyProjectionWork(t, applyStore)
	oldProjection, err := retrievalchunk.BuildProjection(oldWork.Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}

	dirtyTx, err := dirtyStore.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dirtyTx.Rollback() }()
	if _, err := dirtyTx.ExecContext(ctx, `UPDATE sources SET extracted_text='dirty writer won' WHERE source_key='source:dirty-wins'`); err != nil {
		t.Fatal(err)
	}
	newRevision, err := allocateRetrievalParentDirtyTx(ctx, dirtyTx, "source", "source:dirty-wins")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyStore.db.ExecContext(ctx, `PRAGMA busy_timeout = 0`); err != nil {
		t.Fatal(err)
	}
	_, err = applyStore.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: "source", ParentSourceKey: "source:dirty-wins", DirtyRevision: oldWork.DirtyRevision, Projection: oldProjection, Status: RetrievalProjectionCurrent})
	requireSQLiteBusy(t, err)
	if err := dirtyTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := applyStore.db.ExecContext(ctx, `PRAGMA busy_timeout = 60000`); err != nil {
		t.Fatal(err)
	}
	_, err = applyStore.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: "source", ParentSourceKey: "source:dirty-wins", DirtyRevision: oldWork.DirtyRevision, Projection: oldProjection, Status: RetrievalProjectionCurrent})
	var stale *RetrievalProjectionStaleWorkError
	if !errors.As(err, &stale) || stale.CurrentRevision != newRevision {
		t.Fatalf("apply error=%v stale=%+v", err, stale)
	}
	var status string
	var dirty, projected int64
	var chunks int
	if err := applyStore.db.QueryRow(`SELECT status,dirty_revision,projected_revision FROM retrieval_parent_projections WHERE parent_kind='source' AND parent_source_key='source:dirty-wins'`).Scan(&status, &dirty, &projected); err != nil {
		t.Fatal(err)
	}
	if err := applyStore.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunks WHERE parent_kind='source' AND parent_source_key='source:dirty-wins'`).Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || dirty != newRevision || projected != 0 || chunks != 0 {
		t.Fatalf("final state status=%q dirty=%d projected=%d chunks=%d", status, dirty, projected, chunks)
	}
}

func TestApplyRetrievalProjectionReservationWinsThenDirtyWriterRemainsPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	applyStore := openStoreAtPath(t, path)
	defer func() { _ = applyStore.Close() }()
	dirtyStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dirtyStore.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	seedRetrievalSource(t, applyStore, "source:apply-wins")
	dirtyRetrievalParentForTest(t, applyStore, "source", "source:apply-wins", nil)
	work := oneDirtyProjectionWork(t, applyStore)
	projection, err := retrievalchunk.BuildProjection(work.Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	input := ApplyRetrievalProjectionInput{ParentKind: "source", ParentSourceKey: "source:apply-wins", DirtyRevision: work.DirtyRevision, Projection: projection, Status: RetrievalProjectionCurrent}

	applyTx, err := applyStore.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = applyTx.Rollback() }()
	if err := reserveRetrievalProjectionApplyTx(ctx, applyTx); err != nil {
		t.Fatal(err)
	}
	if _, err := dirtyStore.db.ExecContext(ctx, `PRAGMA busy_timeout = 0`); err != nil {
		t.Fatal(err)
	}
	blockedDirtyTx, err := dirtyStore.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = allocateRetrievalParentDirtyTx(ctx, blockedDirtyTx, "source", "source:apply-wins")
	requireSQLiteBusy(t, err)
	if err := blockedDirtyTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	result, err := applyRetrievalProjectionReservedTx(ctx, applyTx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := dirtyStore.db.ExecContext(ctx, `PRAGMA busy_timeout = 60000`); err != nil {
		t.Fatal(err)
	}
	dirtyTx, err := dirtyStore.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dirtyTx.Rollback() }()
	if _, err := dirtyTx.ExecContext(ctx, `UPDATE sources SET extracted_text='dirty writer followed apply' WHERE source_key='source:apply-wins'`); err != nil {
		t.Fatal(err)
	}
	newRevision, err := allocateRetrievalParentDirtyTx(ctx, dirtyTx, "source", "source:apply-wins")
	if err != nil {
		t.Fatal(err)
	}
	if err := dirtyTx.Commit(); err != nil {
		t.Fatal(err)
	}
	var status string
	var dirty, projected int64
	var chunks int
	if err := applyStore.db.QueryRow(`SELECT status,dirty_revision,projected_revision FROM retrieval_parent_projections WHERE parent_kind='source' AND parent_source_key='source:apply-wins'`).Scan(&status, &dirty, &projected); err != nil {
		t.Fatal(err)
	}
	if err := applyStore.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunks WHERE parent_kind='source' AND parent_source_key='source:apply-wins'`).Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if result.Created != len(projection.Chunks) || status != "pending" || dirty != newRevision || dirty <= work.DirtyRevision || projected != work.DirtyRevision || chunks != len(projection.Chunks) {
		t.Fatalf("result=%+v status=%q dirty=%d projected=%d chunks=%d new_revision=%d", result, status, dirty, projected, chunks, newRevision)
	}
}

func requireSQLiteBusy(t *testing.T, err error) {
	t.Helper()
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) {
		t.Fatalf("error=%v, want wrapped *sqlite.Error", err)
	}
	if primaryCode := sqliteErr.Code() & 0xff; primaryCode != sqlite3.SQLITE_BUSY {
		t.Fatalf("sqlite error=%v code=%d primary=%d, want SQLITE_BUSY=%d", sqliteErr, sqliteErr.Code(), primaryCode, sqlite3.SQLITE_BUSY)
	}
}

func TestApplyRetrievalProjectionPersistsEmptyParentTerminalState(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := st.db.Exec(`INSERT INTO items (source_key,source_type,external_id,canonical_url,title,text,content_hash,raw_json,imported_at,updated_at,last_seen_at,note_path) VALUES ('item:empty','test','item:empty','','','','hash','{}',?,?,?,'empty.md')`, now, now, now); err != nil {
		t.Fatal(err)
	}
	dirtyRetrievalParentForTest(t, st, "item", "item:empty", nil)
	work := oneDirtyProjectionWork(t, st)
	projection, err := retrievalchunk.BuildProjection(work.Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: "item", ParentSourceKey: "item:empty", DirtyRevision: work.DirtyRevision, Projection: projection, Status: RetrievalProjectionEmpty, Reason: "no_chunkable_content"}); err != nil {
		t.Fatal(err)
	}
	remaining, err := st.ListDirtyRetrievalParents(ctx, work.DirtyRevision, 10)
	if err != nil {
		t.Fatal(err)
	}
	var status, reason string
	if err := st.db.QueryRow(`SELECT status,reason FROM retrieval_parent_projections WHERE parent_kind='item' AND parent_source_key='item:empty'`).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 || status != "empty" || reason != "no_chunkable_content" {
		t.Fatalf("remaining=%+v state=(%q,%q)", remaining, status, reason)
	}
}

func TestApplyRetrievalProjectionCleansDeletedParentAndRemovesLedger(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:deleted-cleanup")
	dirtyRetrievalParentForTest(t, st, "source", "source:deleted-cleanup", nil)
	initial := oneDirtyProjectionWork(t, st)
	projection, err := retrievalchunk.BuildProjection(initial.Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: "source", ParentSourceKey: "source:deleted-cleanup", DirtyRevision: initial.DirtyRevision, Projection: projection, Status: RetrievalProjectionCurrent}); err != nil {
		t.Fatal(err)
	}
	dirtyRetrievalParentForTest(t, st, "source", "source:deleted-cleanup", func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM sources WHERE source_key='source:deleted-cleanup'`)
		return err
	})
	cleanup := oneDirtyProjectionWork(t, st)
	empty, err := retrievalchunk.BuildProjection(cleanup.Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyRetrievalProjection(ctx, ApplyRetrievalProjectionInput{ParentKind: "source", ParentSourceKey: "source:deleted-cleanup", DirtyRevision: cleanup.DirtyRevision, Projection: empty, Status: RetrievalProjectionEmpty, Reason: "no_chunkable_content"}); err != nil {
		t.Fatal(err)
	}
	var chunks, occurrences, ledger int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunks WHERE parent_kind='source' AND parent_source_key='source:deleted-cleanup'`).Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunk_occurrences WHERE parent_kind='source' AND parent_source_key='source:deleted-cleanup'`).Scan(&occurrences); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_parent_projections WHERE parent_kind='source' AND parent_source_key='source:deleted-cleanup'`).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if chunks != 0 || occurrences != 0 || ledger != 0 {
		t.Fatalf("deleted cleanup left chunks=%d occurrences=%d ledger=%d", chunks, occurrences, ledger)
	}
}

func dirtyRetrievalParentForTest(t *testing.T, st *Store, kind, sourceKey string, mutate func(*sql.Tx) error) int64 {
	t.Helper()
	before := projectionRevisionForTest(t, st)
	tx, err := st.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if mutate != nil {
		if err := mutate(tx); err != nil {
			t.Fatal(err)
		}
	}
	var revision int64
	if err := tx.QueryRow(`SELECT projection_work_revision FROM retrieval_state WHERE singleton=1`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision == before {
		revision, err = MarkRetrievalParentDirtyTx(context.Background(), tx, kind, sourceKey)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return revision
}

func oneDirtyProjectionWork(t *testing.T, st *Store) RetrievalParentWork {
	t.Helper()
	watermark, err := st.ProjectionWorkRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	work, err := st.ListDirtyRetrievalParents(context.Background(), watermark, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(work) != 1 {
		t.Fatalf("dirty work=%+v want one row", work)
	}
	return work[0]
}

func sharedProjectionChunkID(t *testing.T, left, right retrievalchunk.Projection) string {
	t.Helper()
	leftIDs := make(map[string]struct{}, len(left.Chunks))
	for _, chunk := range left.Chunks {
		leftIDs[chunk.ID] = struct{}{}
	}
	for _, chunk := range right.Chunks {
		if _, ok := leftIDs[chunk.ID]; ok {
			return chunk.ID
		}
	}
	t.Fatal("projections have no shared chunk identity")
	return ""
}

func TestEmbeddingDuePredicateMatchesStatusAndCarriesAttemptCount(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"due", "future"} {
		chunk := testRetrievalChunk(id, "item", "item:"+id, 0, "hash-"+id, id)
		if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:"+id, []retrievalchunk.Chunk{chunk}); err != nil {
			t.Fatalf("seed %s chunk: %v", id, err)
		}
		markProjectionCurrentForTest(t, st, "item", "item:"+id)
		row := testEmbedding(id, "profile-a", "hash-"+id)
		row.Status = RetrievalEmbeddingError
		row.AttemptCount = 3
		if id == "due" {
			row.NextAttemptAt = now.Add(-time.Second)
		} else {
			row.NextAttemptAt = now.Add(time.Hour)
		}
		if err := st.PutRetrievalEmbedding(ctx, row); err != nil {
			t.Fatalf("seed %s embedding: %v", id, err)
		}
	}

	candidates, err := st.ListChunksNeedingEmbeddingAt(ctx, "profile-a", "", 10, now)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].ChunkID != "due" || candidates[0].AttemptCount != 3 {
		t.Fatalf("candidates = %+v, want due with attempt_count=3", candidates)
	}
	count, err := st.CountChunksNeedingEmbeddingAt(ctx, "profile-a", now)
	if err != nil || count != 1 {
		t.Fatalf("candidate count=%d err=%v", count, err)
	}
	status, err := st.RetrievalStatusAt(ctx, "profile-a", now)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.FailedEmbeddings != 2 || status.EmbeddingCandidates != 1 {
		t.Fatalf("status = %+v, want failed=2 candidates=1", status)
	}
}

func TestEmbeddingProfileCandidateSelectorRejectsStaleChunkProvenance(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	profile := embedding.Profile{
		Provider: "fake", Model: "m", Dimensions: 2,
		ProjectionVersion: retrievalchunk.ProjectionVersion,
		ChunkerVersion:    retrievalchunk.Version,
		Representation:    embedding.RepresentationDenseF32,
		Normalization:     embedding.NormalizationL2,
	}
	chunk := testRetrievalChunk("stale", "item", "item:stale", 0, "hash-stale", "stale")
	chunk.ProjectionVersion = ""
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:stale", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("seed stale chunk: %v", err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:stale")
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding(chunk.ID, profileID, chunk.TextHash)); err != nil {
		t.Fatalf("seed historically mislabeled embedding: %v", err)
	}

	if _, err := st.CountChunksNeedingEmbeddingForProfileAt(ctx, profile, now); err == nil || !strings.Contains(err.Error(), "run semantic chunk") {
		t.Fatalf("count stale chunks error = %v, want semantic chunk instruction", err)
	}
	if _, err := st.ListChunksNeedingEmbeddingForProfileAt(ctx, profile, "", 10, now); err == nil || !strings.Contains(err.Error(), "run semantic chunk") {
		t.Fatalf("list stale chunks error = %v, want semantic chunk instruction", err)
	}

	chunk.ProjectionVersion = retrievalchunk.ProjectionVersion
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:stale", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("replace with current chunk: %v", err)
	}
	rows, err := st.ListChunksNeedingEmbeddingForProfileAt(ctx, profile, "", 10, now)
	if err != nil || len(rows) != 1 || rows[0].ChunkID != chunk.ID || rows[0].ProjectionVersion != retrievalchunk.ProjectionVersion || rows[0].ChunkerVersion != retrievalchunk.Version {
		t.Fatalf("current candidates=%+v err=%v", rows, err)
	}
	ready, err := st.ListReadyEmbeddings(ctx, profileID, 10)
	if err != nil || len(ready) != 0 {
		t.Fatalf("ready embeddings after provenance rewrite=%+v err=%v, want invalidated", ready, err)
	}
	count, err := st.CountChunksNeedingEmbeddingForProfileAt(ctx, profile, now)
	if err != nil || count != 1 {
		t.Fatalf("current candidate count=%d err=%v", count, err)
	}
}

func TestRetrievalProjectionUsesAuthoritativeItemEnrichmentMirror(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedPurgeItem(t, st, "item:mirror")
	var itemID int64
	if err := st.db.QueryRow(`SELECT id FROM items WHERE source_key = 'item:mirror'`).Scan(&itemID); err != nil {
		t.Fatalf("load mirror item ID: %v", err)
	}
	if _, err := st.db.Exec(`
		UPDATE items SET summary_text = 'legacy summary', ocr_text = 'legacy OCR',
			article_title = ?, article_text = 'legacy transcript'
		WHERE id = ?`, model.XMediaTranscriptArticleTitle, itemID); err != nil {
		t.Fatalf("seed legacy enrichment columns: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, fixture := range []struct{ role, text string }{
		{model.ItemEnrichmentRoleSummary, "mirror summary"},
		{model.ItemEnrichmentRoleOCR, "mirror OCR"},
		{model.ItemEnrichmentRoleXMediaTranscript, "mirror transcript"},
	} {
		if _, err := st.db.Exec(`
			INSERT INTO item_enrichments (
				item_id, role, status, text, raw_json, error, model, prompt_version,
				tool, tool_version, input_hash, completed_at, created_at, updated_at
			) VALUES (?, ?, 'ok', ?, '{}', '', 'test', 'v1', 'test', 'v1', 'hash', ?, ?, ?)`,
			itemID, fixture.role, fixture.text, now, now, now); err != nil {
			t.Fatalf("seed %s enrichment mirror: %v", fixture.role, err)
		}
	}

	parents, err := st.ListRetrievalParents(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("list retrieval parents: %v", err)
	}
	if len(parents) != 1 {
		t.Fatalf("retrieval parents = %+v, want one", parents)
	}
	sections := make(map[string]string)
	for _, section := range parents[0].Sections {
		sections[section.Role] = section.Text
	}
	for role, want := range map[string]string{
		"summary": "mirror summary", "ocr": "mirror OCR", "transcript": "mirror transcript",
	} {
		if sections[role] != want {
			t.Fatalf("projected %s = %q, want %q (sections=%+v)", role, sections[role], want, parents[0].Sections)
		}
	}
}

func TestRetrievalProjectionAuthoritativeEmptyTranscriptSuppressesLegacyTranscript(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedPurgeItem(t, st, "item:empty-transcript")
	var itemID int64
	if err := st.db.QueryRow(`SELECT id FROM items WHERE source_key = 'item:empty-transcript'`).Scan(&itemID); err != nil {
		t.Fatalf("load transcript item ID: %v", err)
	}
	if _, err := st.db.Exec(`UPDATE items SET article_title = ?, article_text = 'stale legacy transcript' WHERE id = ?`, model.XMediaTranscriptArticleTitle, itemID); err != nil {
		t.Fatalf("seed stale legacy transcript: %v", err)
	}
	seedItemEnrichmentRows(t, st, itemID, map[string]string{model.ItemEnrichmentRoleXMediaTranscript: ""})

	parents, err := st.ListRetrievalParents(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("list parents: %v", err)
	}
	if len(parents) != 1 {
		t.Fatalf("parents = %+v, want one", parents)
	}
	for _, section := range parents[0].Sections {
		if section.Role == "transcript" || strings.Contains(section.Text, "stale legacy transcript") {
			t.Fatalf("authoritative empty transcript exposed stale legacy content: %+v", parents[0].Sections)
		}
	}
}

func TestRetrievalProjectionEmptyTranscriptMirrorPreservesOrdinaryArticle(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedPurgeItem(t, st, "item:ordinary-article")
	var itemID int64
	if err := st.db.QueryRow(`SELECT id FROM items WHERE source_key = 'item:ordinary-article'`).Scan(&itemID); err != nil {
		t.Fatalf("load ordinary article item ID: %v", err)
	}
	if _, err := st.db.Exec(`UPDATE items SET article_title = 'Original article', article_text = 'Original body' WHERE id = ?`, itemID); err != nil {
		t.Fatalf("seed ordinary article: %v", err)
	}
	if err := st.SaveXMediaTranscriptionState(
		context.Background(), itemID, model.XMediaTranscriptStatusError,
		"transcription failed", time.Now().UTC(),
	); err != nil {
		t.Fatalf("save empty transcript mirror: %v", err)
	}

	parents, err := st.ListRetrievalParents(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("list parents: %v", err)
	}
	if len(parents) != 1 {
		t.Fatalf("parents = %+v, want one", parents)
	}
	for _, section := range parents[0].Sections {
		if section.Role == "raw" && section.Heading == "Original article" && section.Text == "Original body" {
			return
		}
	}
	t.Fatalf("ordinary article missing after empty transcript mirror: %+v", parents[0].Sections)
}

func TestPutRetrievalEmbeddingRejectsCorruptReadyVector(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*RetrievalEmbeddingRow)
	}{
		{name: "wrong byte length", mutate: func(row *RetrievalEmbeddingRow) { row.VectorBytes = row.VectorBytes[:4] }},
		{name: "non finite", mutate: func(row *RetrievalEmbeddingRow) {
			row.VectorBytes = embedding.EncodeDenseF32([]float32{float32(math.NaN()), 0})
		}},
		{name: "zero L2 norm", mutate: func(row *RetrievalEmbeddingRow) {
			row.VectorBytes = embedding.EncodeDenseF32([]float32{0, 0})
		}},
		{name: "stale chunk hash", mutate: func(row *RetrievalEmbeddingRow) { row.ChunkTextHash = "stale-hash" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
			defer func() { _ = st.Close() }()
			chunk := testRetrievalChunk("corrupt-chunk", "item", "item:corrupt", 0, "current-hash", "alpha")
			if _, err := st.ReplaceRetrievalChunks(context.Background(), "item", "item:corrupt", []retrievalchunk.Chunk{chunk}); err != nil {
				t.Fatalf("replace chunks: %v", err)
			}
			row := testEmbedding(chunk.ID, "corrupt-profile", chunk.TextHash)
			tc.mutate(&row)
			if err := st.PutRetrievalEmbedding(context.Background(), row); err == nil {
				t.Fatal("corrupt ready vector unexpectedly stored")
			}
			var count int
			if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_embeddings`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("stored %d corrupt embeddings, want zero", count)
			}
		})
	}
}

func TestPutRetrievalEmbeddingAllowsNonReadyRowsWithoutVectorBytes(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunks := []retrievalchunk.Chunk{
		testRetrievalChunk("pending-chunk", "item", "item:states", 0, "pending-hash", "pending"),
		testRetrievalChunk("blocked-chunk", "item", "item:states", 1, "blocked-hash", "blocked"),
		testRetrievalChunk("error-chunk", "item", "item:states", 2, "error-hash", "error"),
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:states", chunks); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
	statuses := []RetrievalEmbeddingStatus{RetrievalEmbeddingPending, RetrievalEmbeddingBlocked, RetrievalEmbeddingError}
	for i, status := range statuses {
		row := testEmbedding(chunks[i].ID, "state-profile", chunks[i].TextHash)
		row.Status = status
		row.VectorBytes = nil
		if err := st.PutRetrievalEmbedding(ctx, row); err != nil {
			t.Fatalf("put %s embedding without bytes: %v", status, err)
		}
	}
}

func TestEmbeddingBatchUsesOneRevisionAndVectorHashes(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunks := []retrievalchunk.Chunk{
		testRetrievalChunk("batch-a", "item", "item:batch", 0, "hash-a", "alpha"),
		testRetrievalChunk("batch-b", "item", "item:batch", 1, "hash-b", "bravo"),
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:batch", chunks); err != nil {
		t.Fatal(err)
	}
	profile := embedding.Profile{Provider: "fake", Model: "fake-v1", Dimensions: 2, ProjectionVersion: retrievalchunk.ProjectionVersion, ChunkerVersion: retrievalchunk.Version, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2}
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	rows := []RetrievalEmbeddingRow{testEmbedding(chunks[0].ID, profileID, chunks[0].TextHash), testEmbedding(chunks[1].ID, profileID, chunks[1].TextHash)}
	revision, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: rows, ExpectedPurgeEpoch: 0})
	if err != nil {
		t.Fatal(err)
	}
	if revision != 1 {
		t.Fatalf("revision=%d want 1", revision)
	}
	var distinctRevisions, vectorHashes int
	var storedVectorHash string
	if err := st.db.QueryRow(`SELECT COUNT(DISTINCT revision), COUNT(DISTINCT vector_hash), MIN(vector_hash) FROM retrieval_embeddings WHERE profile_id=?`, profileID).Scan(&distinctRevisions, &vectorHashes, &storedVectorHash); err != nil {
		t.Fatal(err)
	}
	if distinctRevisions != 1 || vectorHashes != 1 || storedVectorHash != retrievalVectorHash(rows[0].VectorBytes) {
		t.Fatalf("distinct revisions=%d hashes=%d stored_hash=%q", distinctRevisions, vectorHashes, storedVectorHash)
	}
	profileRow, err := st.RetrievalEmbeddingProfile(ctx, profileID)
	if err != nil {
		t.Fatal(err)
	}
	if profileRow.LatestRevision != revision || profileRow.L0ReadyCount != 2 || profileRow.PurgeEpoch != 0 {
		t.Fatalf("profile=%+v", profileRow)
	}
	rows[0].AttemptCount++
	later, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: rows[:1], ExpectedPurgeEpoch: 0})
	if err != nil || later != revision {
		t.Fatalf("later revision=%d err=%v", later, err)
	}
	afterIdempotent, err := st.RetrievalEmbeddingProfile(ctx, profileID)
	if err != nil || afterIdempotent.LatestRevision != revision || afterIdempotent.L0ReadyCount != 2 {
		t.Fatalf("profile after idempotent re-put=%+v err=%v", afterIdempotent, err)
	}
}

func TestEmbeddingBatchPreservesActiveRootAndAccountsL0AndTombstones(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunks := []retrievalchunk.Chunk{
		testRetrievalChunk("aggregate-a", "item", "item:aggregates", 0, "hash-a", "alpha"),
		testRetrievalChunk("aggregate-b", "item", "item:aggregates", 1, "hash-b", "bravo"),
		testRetrievalChunk("aggregate-c", "item", "item:aggregates", 2, "hash-c", "charlie"),
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:aggregates", chunks); err != nil {
		t.Fatal(err)
	}
	profile := embedding.Profile{Provider: "fake", Model: "fake-v1", Dimensions: 2, ProjectionVersion: retrievalchunk.ProjectionVersion, ChunkerVersion: retrievalchunk.Version, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2}
	profileID, _ := profile.ID()
	rows := make([]RetrievalEmbeddingRow, 0, len(chunks))
	for _, chunk := range chunks {
		rows = append(rows, testEmbedding(chunk.ID, profileID, chunk.TextHash))
	}
	if _, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: rows, ExpectedPurgeEpoch: 0}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{GenerationID: "aggregate-root", ProfileID: profileID, Backend: "exact", BackendVersion: "v1", Dimensions: 2, DistanceMetric: "cosine", IndexedChunkCount: 3, BuildStatus: RetrievalGenerationCompleted}); err != nil {
		t.Fatal(err)
	}
	seedActiveRetrievalGenerationForTest(t, st, "aggregate-root")
	rows[0].AttemptCount++
	idempotentRevision, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: rows[:1], ExpectedPurgeEpoch: 0})
	if err != nil || idempotentRevision != 1 {
		t.Fatalf("idempotent indexed re-put revision=%d err=%v", idempotentRevision, err)
	}
	assertProfileAggregatesForTest(t, st, profileID, "aggregate-root", 3, 0, 0)
	assertGenerationActiveForTest(t, st, "aggregate-root")
	rows[0].VectorBytes = embedding.EncodeDenseF32([]float32{0, 1})
	if _, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: rows[:1], ExpectedPurgeEpoch: 0}); err != nil {
		t.Fatal(err)
	}
	assertProfileAggregatesForTest(t, st, profileID, "aggregate-root", 3, 1, 1)
	assertGenerationActiveForTest(t, st, "aggregate-root")
	if _, err := st.db.Exec(`DELETE FROM retrieval_chunks WHERE chunk_id IN ('aggregate-a','aggregate-b')`); err != nil {
		t.Fatal(err)
	}
	assertProfileAggregatesForTest(t, st, profileID, "aggregate-root", 3, 0, 2)
	assertGenerationActiveForTest(t, st, "aggregate-root")
}

func TestEmbeddingDeletionFailsClosedOnAggregateDrift(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunk := testRetrievalChunk("drift-l0", "item", "item:drift-l0", 0, "hash", "alpha")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:drift-l0", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding(chunk.ID, "drift-profile", chunk.TextHash)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embedding_profiles SET l0_ready_count=0 WHERE profile_id='drift-profile'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DELETE FROM retrieval_chunks WHERE chunk_id='drift-l0'`); err == nil {
		t.Fatal("aggregate drift allowed embedding cascade delete")
	}
	var chunks, embeddings int
	_ = st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunks WHERE chunk_id='drift-l0'`).Scan(&chunks)
	_ = st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_embeddings WHERE chunk_id='drift-l0'`).Scan(&embeddings)
	if chunks != 1 || embeddings != 1 {
		t.Fatalf("failed-closed delete chunks=%d embeddings=%d", chunks, embeddings)
	}
}

func TestEmbeddingBatchRejectsMoreThanFiveThousandRowsBeforeStorage(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	profile := embedding.Profile{Provider: "fake", Model: "fake-v1", Dimensions: 2, ProjectionVersion: retrievalchunk.ProjectionVersion, ChunkerVersion: retrievalchunk.Version, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2}
	_, err := st.PutRetrievalEmbeddingBatch(context.Background(), PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: make([]RetrievalEmbeddingRow, 5_001)})
	if err == nil || !strings.Contains(err.Error(), "between 1 and 5000") {
		t.Fatalf("oversized batch error=%v", err)
	}
}

func TestEmbeddingBatchRollsBackOnHashOrEpochRace(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunks := []retrievalchunk.Chunk{
		testRetrievalChunk("race-a", "item", "item:race-batch", 0, "hash-a", "alpha"),
		testRetrievalChunk("race-b", "item", "item:race-batch", 1, "hash-b", "bravo"),
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:race-batch", chunks); err != nil {
		t.Fatal(err)
	}
	profile := embedding.Profile{Provider: "fake", Model: "fake-v1", Dimensions: 2, ProjectionVersion: retrievalchunk.ProjectionVersion, ChunkerVersion: retrievalchunk.Version, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2}
	profileID, _ := profile.ID()
	rows := []RetrievalEmbeddingRow{testEmbedding(chunks[0].ID, profileID, chunks[0].TextHash), testEmbedding(chunks[1].ID, profileID, "stale")}
	if _, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: rows, ExpectedPurgeEpoch: 0}); err == nil {
		t.Fatal("stale batch unexpectedly committed")
	}
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_embeddings WHERE profile_id=?`, profileID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_state SET purge_epoch=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	rows[1].ChunkTextHash = chunks[1].TextHash
	if _, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: rows, ExpectedPurgeEpoch: 0}); err == nil {
		t.Fatal("stale purge epoch unexpectedly committed")
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_embeddings WHERE profile_id=?`, profileID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("post-epoch count=%d err=%v", count, err)
	}
}

func TestEmbeddingBatchRollsBackEveryRowOnPersistenceFailure(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunks := []retrievalchunk.Chunk{
		testRetrievalChunk("rollback-a", "item", "item:rollback-batch", 0, "hash-a", "alpha"),
		testRetrievalChunk("rollback-b", "item", "item:rollback-batch", 1, "hash-b", "bravo"),
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:rollback-batch", chunks); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`CREATE TRIGGER fail_second_embedding BEFORE INSERT ON retrieval_embeddings WHEN NEW.chunk_id='rollback-b' BEGIN SELECT RAISE(ABORT, 'forced batch failure'); END`); err != nil {
		t.Fatal(err)
	}
	profile := embedding.Profile{Provider: "fake", Model: "fake-v1", Dimensions: 2, ProjectionVersion: retrievalchunk.ProjectionVersion, ChunkerVersion: retrievalchunk.Version, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2}
	profileID, _ := profile.ID()
	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{GenerationID: "rollback-batch-generation", ProfileID: profileID, Backend: "exact", BackendVersion: "v1", Dimensions: 2, DistanceMetric: "cosine", BuildStatus: RetrievalGenerationCompleted}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_index_generations SET active=1 WHERE generation_id='rollback-batch-generation'`); err != nil {
		t.Fatal(err)
	}
	rows := []RetrievalEmbeddingRow{testEmbedding(chunks[0].ID, profileID, chunks[0].TextHash), testEmbedding(chunks[1].ID, profileID, chunks[1].TextHash)}
	if _, err := st.PutRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{Profile: profile, Rows: rows, ExpectedPurgeEpoch: 0}); err == nil {
		t.Fatal("forced persistence failure unexpectedly committed")
	}
	var embeddings, profiles int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_embeddings WHERE profile_id=?`, profileID).Scan(&embeddings); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if embeddings != 0 || profiles != 0 {
		t.Fatalf("rolled back embeddings=%d profiles=%d", embeddings, profiles)
	}
	var generationStatus string
	var generationActive int
	if err := st.db.QueryRow(`SELECT build_status,active FROM retrieval_index_generations WHERE generation_id='rollback-batch-generation'`).Scan(&generationStatus, &generationActive); err != nil {
		t.Fatal(err)
	}
	if generationStatus != string(RetrievalGenerationCompleted) || generationActive != 1 {
		t.Fatalf("generation status=%q active=%d changed despite rollback", generationStatus, generationActive)
	}
}

func TestListRetrievalVectorsPagesLeanRows(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunks := []retrievalchunk.Chunk{
		testRetrievalChunk("vector-a", "item", "item:vectors", 0, "hash-a", "alpha"),
		testRetrievalChunk("vector-b", "item", "item:vectors", 1, "hash-b", "bravo"),
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:vectors", chunks); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		if err := st.PutRetrievalEmbedding(ctx, testEmbedding(chunk.ID, "vector-profile", chunk.TextHash)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := st.ListRetrievalVectors(ctx, "vector-profile", VectorPage{Limit: 1})
	if err != nil || len(page) != 1 || page[0].ChunkID != "vector-a" || len(page[0].VectorBytes) == 0 || page[0].CurrentChunkTextHash != "hash-a" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	next, err := st.ListRetrievalVectors(ctx, "vector-profile", VectorPage{AfterChunkID: page[0].ChunkID, Limit: 1})
	if err != nil || len(next) != 1 || next[0].ChunkID != "vector-b" {
		t.Fatalf("next=%+v err=%v", next, err)
	}
}

func TestListReadyEmbeddingsRejectsCorruptStoredVector(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunk := testRetrievalChunk("read-corrupt-chunk", "item", "item:read-corrupt", 0, "read-hash", "alpha")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:read-corrupt", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:read-corrupt")
	row := testEmbedding(chunk.ID, "read-corrupt-profile", chunk.TextHash)
	if err := st.PutRetrievalEmbedding(ctx, row); err != nil {
		t.Fatalf("put valid embedding: %v", err)
	}
	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "read-corrupt-generation", ProfileID: row.ProfileID,
		Backend: "exact", BackendVersion: "v1", Dimensions: row.Dimensions,
		DistanceMetric: "cosine", BuildStatus: RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatalf("put active generation: %v", err)
	}
	seedActiveRetrievalGenerationForTest(t, st, "read-corrupt-generation")
	if _, err := st.db.Exec(`UPDATE retrieval_embeddings SET vector_bytes = X'00000000' WHERE chunk_id = ? AND profile_id = ?`, row.ChunkID, row.ProfileID); err != nil {
		t.Fatalf("inject corrupt stored vector: %v", err)
	}
	_, err := st.ListReadyEmbeddings(ctx, row.ProfileID, 10)
	var corruption *RetrievalEmbeddingCorruptionError
	if !errors.As(err, &corruption) || corruption.ChunkID != row.ChunkID || corruption.ProfileID != row.ProfileID {
		t.Fatalf("read corrupt ready vector error = %v, want typed chunk/profile corruption", err)
	}
	corruption.Reason = "stale caller reason"
	if err := st.BlockCorruptRetrievalEmbedding(ctx, corruption); err != nil {
		t.Fatalf("block corrupt ready vector: %v", err)
	}
	var status, lastError string
	if err := st.db.QueryRow(`SELECT status, last_error FROM retrieval_embeddings WHERE chunk_id = ? AND profile_id = ?`, row.ChunkID, row.ProfileID).Scan(&status, &lastError); err != nil {
		t.Fatal(err)
	}
	if status != string(RetrievalEmbeddingBlocked) || !strings.Contains(lastError, "byte length") {
		t.Fatalf("quarantined row = status %q error %q, want blocked corruption", status, lastError)
	}
	profileRow, err := st.RetrievalEmbeddingProfile(ctx, row.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if profileRow.LatestRevision != 2 || profileRow.L0ReadyCount != 0 {
		t.Fatalf("profile after quarantine=%+v, want revision 2 and no L0 ready rows", profileRow)
	}
	assertGenerationActiveForTest(t, st, "read-corrupt-generation")
	assertProfileAggregatesForTest(t, st, row.ProfileID, "read-corrupt-generation", 1, 0, 1)
	ready, err := st.ListReadyEmbeddings(ctx, row.ProfileID, 10)
	if err != nil || len(ready) != 0 {
		t.Fatalf("ready rows after explicit corruption transition = %+v, %v", ready, err)
	}
}

func TestBlockCorruptRetrievalEmbeddingDoesNotBlockConcurrentlyRepairedRow(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunk := testRetrievalChunk("repaired-chunk", "item", "item:repaired", 0, "repaired-hash", "alpha")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:repaired", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:repaired")
	row := testEmbedding(chunk.ID, "repaired-profile", chunk.TextHash)
	if err := st.PutRetrievalEmbedding(ctx, row); err != nil {
		t.Fatalf("put valid embedding: %v", err)
	}
	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "repaired-generation", ProfileID: row.ProfileID,
		Backend: "exact", BackendVersion: "v1", Dimensions: row.Dimensions,
		DistanceMetric: "cosine", BuildStatus: RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatalf("put active generation: %v", err)
	}
	seedActiveRetrievalGenerationForTest(t, st, "repaired-generation")
	if _, err := st.db.Exec(`UPDATE retrieval_embeddings SET vector_bytes = X'00000000' WHERE chunk_id = ? AND profile_id = ?`, row.ChunkID, row.ProfileID); err != nil {
		t.Fatalf("inject corrupt stored vector: %v", err)
	}
	_, err := st.ListReadyEmbeddings(ctx, row.ProfileID, 10)
	var corruption *RetrievalEmbeddingCorruptionError
	if !errors.As(err, &corruption) {
		t.Fatalf("read corrupt ready vector error = %v, want typed corruption", err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embeddings SET vector_bytes = ? WHERE chunk_id = ? AND profile_id = ?`, row.VectorBytes, row.ChunkID, row.ProfileID); err != nil {
		t.Fatalf("simulate concurrent repair: %v", err)
	}
	if err := st.BlockCorruptRetrievalEmbedding(ctx, corruption); !errors.Is(err, ErrRetrievalEmbeddingNoLongerCorrupt) {
		t.Fatalf("block repaired embedding error = %v, want ErrRetrievalEmbeddingNoLongerCorrupt", err)
	}
	var status string
	if err := st.db.QueryRow(`SELECT status FROM retrieval_embeddings WHERE chunk_id = ? AND profile_id = ?`, row.ChunkID, row.ProfileID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(RetrievalEmbeddingReady) {
		t.Fatalf("repaired embedding status = %q, want ready", status)
	}
	var generationStatus string
	var active int
	if err := st.db.QueryRow(`SELECT build_status, active FROM retrieval_index_generations WHERE generation_id = 'repaired-generation'`).Scan(&generationStatus, &active); err != nil {
		t.Fatal(err)
	}
	if generationStatus != string(RetrievalGenerationCompleted) || active != 1 {
		t.Fatalf("generation after stale diagnostic = status %q active %d, want completed active", generationStatus, active)
	}
}

func TestListReadyEmbeddingsRejectsStoredVectorHashMismatch(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunk := testRetrievalChunk("hash-corrupt-chunk", "item", "item:hash-corrupt", 0, "hash-corrupt-text", "alpha")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:hash-corrupt", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:hash-corrupt")
	row := testEmbedding(chunk.ID, "hash-corrupt-profile", chunk.TextHash)
	if err := st.PutRetrievalEmbedding(ctx, row); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embeddings SET vector_hash='wrong' WHERE chunk_id=? AND profile_id=?`, row.ChunkID, row.ProfileID); err != nil {
		t.Fatal(err)
	}
	_, err := st.ListReadyEmbeddings(ctx, row.ProfileID, 1)
	var corruption *RetrievalEmbeddingCorruptionError
	if !errors.As(err, &corruption) || !strings.Contains(corruption.Reason, "vector hash") {
		t.Fatalf("error=%v corruption=%+v", err, corruption)
	}
	if err := st.BlockCorruptRetrievalEmbedding(ctx, corruption); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := st.db.QueryRow(`SELECT status FROM retrieval_embeddings WHERE chunk_id=? AND profile_id=?`, row.ChunkID, row.ProfileID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(RetrievalEmbeddingBlocked) {
		t.Fatalf("status=%q", status)
	}
}

func TestListReadyEmbeddingsRejectsStoredStaleHash(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunk := testRetrievalChunk("read-stale-chunk", "item", "item:read-stale", 0, "current-hash", "alpha")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:read-stale", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:read-stale")
	row := testEmbedding(chunk.ID, "read-stale-profile", chunk.TextHash)
	if err := st.PutRetrievalEmbedding(ctx, row); err != nil {
		t.Fatalf("put valid embedding: %v", err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embeddings SET chunk_text_hash = 'stale-hash' WHERE chunk_id = ? AND profile_id = ?`, row.ChunkID, row.ProfileID); err != nil {
		t.Fatalf("inject stale stored hash: %v", err)
	}
	_, err := st.ListReadyEmbeddings(ctx, row.ProfileID, 10)
	var corruption *RetrievalEmbeddingCorruptionError
	if !errors.As(err, &corruption) || !strings.Contains(corruption.Reason, "stale-hash") || !strings.Contains(corruption.Reason, "current-hash") {
		t.Fatalf("read stale ready hash error = %v, want typed stale/current diagnostic", err)
	}
}

func TestHydrateRetrievalChunksBatchesCurrentParentEvidenceAndDropsPurgedParents(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedPurgeItem(t, st, "item:hydrate")
	seedRetrievalSource(t, st, "src:hydrate")
	if _, err := st.db.Exec(`
		UPDATE items SET title = 'Hydrated item', canonical_url = 'https://example.com/item',
			author_name = 'Item Author', author_handle = 'item_author', published_at = '2026-07-18',
			summary_text = 'Item summary', user_tags = 'item-tag'
		WHERE source_key = 'item:hydrate'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`
		UPDATE sources SET title = 'Hydrated source', summary_text = 'Source summary',
			extracted_at = '2026-07-17T00:00:00Z', summarized_at = '2026-07-18T00:00:00Z', user_tags = 'source-tag'
		WHERE source_key = 'src:hydrate'`); err != nil {
		t.Fatal(err)
	}
	itemChunk := testRetrievalChunk("item-hydrate-chunk", "item", "item:hydrate", 2, "item-hash", "item chunk")
	itemChunk.EvidenceRole, itemChunk.Heading = "raw", "Item heading"
	sourceChunk := testRetrievalChunk("source-hydrate-chunk", "source", "src:hydrate", 3, "source-hash", "source chunk")
	sourceChunk.EvidenceRole, sourceChunk.Heading = "summary", "Source heading"
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:hydrate", []retrievalchunk.Chunk{itemChunk}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "source", "src:hydrate", []retrievalchunk.Chunk{sourceChunk}); err != nil {
		t.Fatal(err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:hydrate")
	markProjectionCurrentForTest(t, st, "source", "src:hydrate")
	rows, err := st.HydrateRetrievalChunks(ctx, []string{sourceChunk.ID, "missing", itemChunk.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%+v", rows)
	}
	byID := map[string]RetrievalChunkEvidenceRow{}
	for _, row := range rows {
		byID[row.ChunkID] = row
	}
	if got := byID[itemChunk.ID]; got.Title != "Hydrated item" || got.Author != "Item Author @item_author" || got.Text != "item chunk" || got.Ordinal != 2 || got.ChunkTextHash != "item-hash" {
		t.Fatalf("item row=%+v", got)
	}
	if got := byID[sourceChunk.ID]; got.Title != "Hydrated source" || got.URL == "" || got.Summary != "Source summary" || got.EvidenceRole != "summary" {
		t.Fatalf("source row=%+v", got)
	}
	if _, err := st.db.Exec(`UPDATE sources SET note_path = '' WHERE source_key = 'src:hydrate'`); err != nil {
		t.Fatal(err)
	}
	rows, err = st.HydrateRetrievalChunks(ctx, []string{sourceChunk.ID, itemChunk.ID})
	if err != nil || len(rows) != 1 || rows[0].ChunkID != itemChunk.ID {
		t.Fatalf("rows after purge marker=%+v err=%v", rows, err)
	}
}

func TestPendingParentIsExcludedFromEverySemanticVectorSelectorAndHydration(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:pending-selector")
	markProjectionCurrentForTest(t, st, "source", "source:pending-selector")

	chunk := testRetrievalChunk("pending-selector-chunk", "source", "source:pending-selector", 0, "pending-selector-hash", "stale text")
	if _, err := st.ReplaceRetrievalChunks(ctx, "source", "source:pending-selector", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding(chunk.ID, "pending-selector-profile", chunk.TextHash)); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "pending-selector-generation", ProfileID: "pending-selector-profile",
		Backend: "exact", BackendVersion: "v1", Dimensions: 2,
		DistanceMetric: "cosine", BuildStatus: RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatal(err)
	}
	seedActiveRetrievalGenerationForTest(t, st, "pending-selector-generation")
	if _, err := st.db.Exec(`UPDATE sources SET title='mutated projected title' WHERE source_key='source:pending-selector'`); err != nil {
		t.Fatal(err)
	}
	assertRetrievalGenerationStale(t, st, "pending-selector-generation")

	ready, err := st.ListReadyEmbeddings(ctx, "pending-selector-profile", 10)
	if err != nil {
		t.Fatal(err)
	}
	needed, err := st.ListChunksNeedingEmbedding(ctx, "another-profile", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	count, err := st.CountChunksNeedingEmbeddingAt(ctx, "another-profile", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	hydrated, err := st.HydrateRetrievalChunks(ctx, []string{chunk.ID})
	if err != nil {
		t.Fatal(err)
	}
	status, err := st.RetrievalStatus(ctx, "another-profile")
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 0 || len(needed) != 0 || count != 0 || len(hydrated) != 0 || status.ChunkCount != 0 || status.EmbeddingCandidates != 0 {
		t.Fatalf("non-current parent leaked: ready=%+v needed=%+v count=%d hydrated=%+v status=%+v", ready, needed, count, hydrated, status)
	}
}

func TestProjectedMutationDirtiesOnceAndIrrelevantItemMutationDoesNot(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	beforeInsert := projectionRevisionForTest(t, st)
	seedPurgeItem(t, st, "item:projected-mutation")
	if got := projectionRevisionForTest(t, st); got != beforeInsert+1 {
		t.Fatalf("item insert revision=%d want %d", got, beforeInsert+1)
	}
	markProjectionCurrentForTest(t, st, "item", "item:projected-mutation")
	before := projectionRevisionForTest(t, st)

	if _, err := st.db.Exec(`UPDATE items SET title='new title', text='new body' WHERE source_key='item:projected-mutation'`); err != nil {
		t.Fatal(err)
	}
	if got := projectionRevisionForTest(t, st); got != before+1 {
		t.Fatalf("projected multi-field update revision=%d want %d", got, before+1)
	}
	assertProjectionPendingAtRevision(t, st, "item", "item:projected-mutation", before+1)

	if _, err := st.db.Exec(`UPDATE items SET like_count=like_count+1, last_seen_at='2099-01-01T00:00:00Z' WHERE source_key='item:projected-mutation'`); err != nil {
		t.Fatal(err)
	}
	if got := projectionRevisionForTest(t, st); got != before+1 {
		t.Fatalf("irrelevant item update dirtied revision=%d want %d", got, before+1)
	}
	if _, err := st.db.Exec(`DELETE FROM items WHERE source_key='item:projected-mutation'`); err != nil {
		t.Fatal(err)
	}
	if got := projectionRevisionForTest(t, st); got != before+2 {
		t.Fatalf("item delete revision=%d want %d", got, before+2)
	}
	assertProjectionPendingAtRevision(t, st, "item", "item:projected-mutation", before+2)
}

func TestMarkRetrievalParentDirtyTxAllocatesOnceAndInvalidatesLegacyGeneration(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:named-dirty")
	chunk := testRetrievalChunk("named-dirty-chunk", "source", "source:named-dirty", 0, "named-dirty-hash", "text")
	if _, err := st.ReplaceRetrievalChunks(ctx, "source", "source:named-dirty", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	markProjectionCurrentForTest(t, st, "source", "source:named-dirty")
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding(chunk.ID, "named-dirty-profile", chunk.TextHash)); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "named-dirty-generation", ProfileID: "named-dirty-profile",
		Backend: "exact", BackendVersion: "v1", Dimensions: 2,
		DistanceMetric: "cosine", BuildStatus: RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatal(err)
	}
	seedActiveRetrievalGenerationForTest(t, st, "named-dirty-generation")
	before := projectionRevisionForTest(t, st)
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := MarkRetrievalParentDirtyTx(ctx, tx, "source", "source:named-dirty")
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if revision != before+1 || projectionRevisionForTest(t, st) != before+1 {
		t.Fatalf("named dirty revision=%d watermark=%d want %d", revision, projectionRevisionForTest(t, st), before+1)
	}
	assertRetrievalGenerationStale(t, st, "named-dirty-generation")
}

func markProjectionCurrentForTest(t *testing.T, st *Store, kind, sourceKey string) int64 {
	t.Helper()
	tx, err := st.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	revision, err := allocateRetrievalParentDirtyTx(context.Background(), tx, kind, sourceKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE retrieval_parent_projections SET status='current', projected_revision=dirty_revision WHERE parent_kind=? AND parent_source_key=?`, kind, sourceKey); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return revision
}

func projectionRevisionForTest(t *testing.T, st *Store) int64 {
	t.Helper()
	revision, err := st.ProjectionWorkRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func assertProjectionPendingAtRevision(t *testing.T, st *Store, kind, sourceKey string, revision int64) {
	t.Helper()
	var status string
	var dirtyRevision int64
	if err := st.db.QueryRow(`SELECT status, dirty_revision FROM retrieval_parent_projections WHERE parent_kind=? AND parent_source_key=?`, kind, sourceKey).Scan(&status, &dirtyRevision); err != nil {
		t.Fatal(err)
	}
	if status != string(RetrievalProjectionPending) || dirtyRevision != revision {
		t.Fatalf("projection %s/%s status=%q revision=%d want pending/%d", kind, sourceKey, status, dirtyRevision, revision)
	}
}

func testRetrievalChunk(id, parentKind, parentKey string, ordinal int, textHash, text string) retrievalchunk.Chunk {
	return retrievalchunk.Chunk{
		ID: id, ParentKind: parentKind, ParentSourceKey: parentKey, EvidenceRole: "raw",
		SectionOrdinal: 0, Ordinal: ordinal, StartChar: 0, EndChar: len([]rune(text)),
		Heading: "Heading", ProjectionVersion: retrievalchunk.ProjectionVersion, ChunkerVersion: retrievalchunk.Version,
		InputContentHash: "input-hash", TextHash: textHash, Text: text,
	}
}

func testEmbedding(chunkID, profileID, textHash string) RetrievalEmbeddingRow {
	return RetrievalEmbeddingRow{
		ChunkID: chunkID, ProfileID: profileID, Provider: "fake", Model: "fake-v1",
		Dimensions: 2, Representation: "dense_f32", Normalization: "l2",
		VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), ChunkTextHash: textHash,
		Status: RetrievalEmbeddingReady, AttemptCount: 1, EmbeddedAt: time.Now().UTC(),
	}
}

func seedPurgeItem(t *testing.T, st *Store, sourceKey string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := st.db.Exec(`
		INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title, text,
			content_hash, raw_json, imported_at, updated_at, last_seen_at, note_path
		) VALUES (?, 'apple_note', ?, '', 'Private', 'private', ?, '{}', ?, ?, ?, ?)`,
		sourceKey, sourceKey, "item-content-hash", now, now, now, sourceKey+".md")
	if err != nil {
		t.Fatalf("seed purge item %s: %v", sourceKey, err)
	}
}

func seedRetrievalSource(t *testing.T, st *Store, sourceKey string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := st.db.Exec(`
		INSERT INTO sources (
			source_key, canonical_url, normalized_url, source_type, title,
			extracted_text, content_hash, note_path, created_at, updated_at
		) VALUES (?, ?, ?, 'article', 'Source', 'source text', 'source-hash', ?, ?, ?)`,
		sourceKey, "https://example.com/"+sourceKey, "https://example.com/"+sourceKey,
		sourceKey+".md", now, now)
	if err != nil {
		t.Fatalf("seed retrieval source %s: %v", sourceKey, err)
	}
}

func TestGiantProjectionStagingIsNonSearchableResumableAndPromotesOnce(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:giant-stage")
	work, err := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 10)
	if err != nil || len(work) != 1 {
		t.Fatalf("work=%+v err=%v", work, err)
	}
	projection, err := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
	if err != nil || len(projection.Occurrences) == 0 {
		t.Fatalf("projection=%+v err=%v", projection, err)
	}
	row := RetrievalProjectionStageRow{Chunk: projection.Chunks[0], Occurrence: projection.Occurrences[0]}
	cp, err := st.StageRetrievalProjectionBatch(ctx, StageRetrievalProjectionInput{
		ParentKind: "source", ParentSourceKey: "source:giant-stage", DirtyRevision: work[0].DirtyRevision,
		ProjectionHash: projection.ParentHash, Cursor: retrievalchunk.Cursor{SectionKey: row.Occurrence.SectionKey, NextBoundary: row.Occurrence.EndChar}, Rows: []RetrievalProjectionStageRow{row},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cp.WorkID == "" || cp.DirtyRevision != work[0].DirtyRevision || cp.SectionKey == "" || cp.NextBoundary <= 0 || cp.StagedChunks != 1 {
		t.Fatalf("checkpoint=%+v", cp)
	}
	var searchable int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunks WHERE parent_source_key='source:giant-stage'`).Scan(&searchable); err != nil || searchable != 0 {
		t.Fatalf("searchable=%d err=%v", searchable, err)
	}
	loaded, ok, err := st.LoadRetrievalProjectionStaging(ctx, work[0].Parent, work[0].DirtyRevision)
	if err != nil || !ok || loaded != cp {
		t.Fatalf("loaded=%+v ok=%v err=%v want=%+v", loaded, ok, err, cp)
	}
	if _, err := st.PromoteRetrievalProjectionStaging(ctx, cp); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete promotion err=%v", err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunks WHERE parent_source_key='source:giant-stage'`).Scan(&searchable); err != nil || searchable != 0 {
		t.Fatalf("incomplete promotion searchable=%d err=%v", searchable, err)
	}
	cp, err = st.StageRetrievalProjectionBatch(ctx, StageRetrievalProjectionInput{
		WorkID: cp.WorkID, ParentKind: cp.ParentKind, ParentSourceKey: cp.ParentSourceKey,
		DirtyRevision: cp.DirtyRevision, ProjectionHash: cp.ProjectionHash,
	})
	if err != nil || cp.SectionKey != "" || cp.NextBoundary != 0 {
		t.Fatalf("complete checkpoint=%+v err=%v", cp, err)
	}
	result, err := st.PromoteRetrievalProjectionStaging(ctx, cp)
	if err != nil || result.Created != 1 {
		t.Fatalf("promote result=%+v err=%v", result, err)
	}
	if _, err := st.PromoteRetrievalProjectionStaging(ctx, cp); err == nil {
		t.Fatal("second promotion unexpectedly succeeded")
	}
	var staged, live int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_projection_staging WHERE work_id=?`, cp.WorkID).Scan(&staged); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunks WHERE parent_source_key='source:giant-stage'`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if staged != 0 || live != 1 {
		t.Fatalf("staged=%d live=%d", staged, live)
	}
}

func TestGiantProjectionRedirtyDiscardsStaleStaging(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:giant-redirty")
	work, err := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
	if err != nil || len(work) != 1 {
		t.Fatalf("work=%+v err=%v", work, err)
	}
	projection, _ := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
	cp, err := st.StageRetrievalProjectionBatch(ctx, StageRetrievalProjectionInput{
		ParentKind: "source", ParentSourceKey: "source:giant-redirty", DirtyRevision: work[0].DirtyRevision,
		ProjectionHash: projection.ParentHash, Cursor: retrievalchunk.Cursor{SectionKey: projection.Occurrences[0].SectionKey, NextBoundary: projection.Occurrences[0].EndChar},
		Rows: []RetrievalProjectionStageRow{{Chunk: projection.Chunks[0], Occurrence: projection.Occurrences[0]}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE sources SET extracted_text='changed source text' WHERE source_key='source:giant-redirty'`); err != nil {
		t.Fatal(err)
	}
	newWork, err := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
	if err != nil || len(newWork) != 1 || newWork[0].DirtyRevision == work[0].DirtyRevision {
		t.Fatalf("new work=%+v err=%v", newWork, err)
	}
	if _, ok, err := st.LoadRetrievalProjectionStaging(ctx, newWork[0].Parent, newWork[0].DirtyRevision); err != nil || ok {
		t.Fatalf("stale staging loaded ok=%v err=%v", ok, err)
	}
	var rows int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_projection_staging WHERE work_id=?`, cp.WorkID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("stale rows=%d err=%v", rows, err)
	}
}

func TestProjectionTooLargeIsTerminalBlockedAndRemovesSearchableChunks(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:too-large")
	work, err := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
	if err != nil || len(work) != 1 {
		t.Fatalf("work=%+v err=%v", work, err)
	}
	projection, _ := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
	if _, err := st.ReplaceRetrievalChunks(ctx, "source", "source:too-large", projection.Chunks); err != nil {
		t.Fatal(err)
	}
	if err := st.BlockRetrievalProjectionTooLarge(ctx, work[0].Parent, work[0].DirtyRevision, projection.ParentHash); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	var live, staged int
	if err := st.db.QueryRow(`SELECT status,reason FROM retrieval_parent_projections WHERE parent_kind='source' AND parent_source_key='source:too-large'`).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	_ = st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunks WHERE parent_source_key='source:too-large'`).Scan(&live)
	_ = st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_projection_staging WHERE parent_source_key='source:too-large'`).Scan(&staged)
	if status != string(RetrievalProjectionBlocked) || reason != "projection_too_large_for_flat_retrieval" || live != 0 || staged != 0 {
		t.Fatalf("status=%q reason=%q live=%d staged=%d", status, reason, live, staged)
	}
}

func seedItemEnrichmentRows(t *testing.T, st *Store, itemID int64, rows map[string]string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	for role, text := range rows {
		if _, err := st.db.Exec(`
			INSERT INTO item_enrichments (
				item_id, role, status, text, raw_json, error, model, prompt_version,
				tool, tool_version, input_hash, completed_at, created_at, updated_at
			) VALUES (?, ?, 'ok', ?, '{}', '', 'test', 'v1', 'test', 'v1', 'hash', ?, ?, ?)`,
			itemID, role, text, now, now, now); err != nil {
			t.Fatalf("seed item enrichment %s: %v", role, err)
		}
	}
}

func assertRetrievalGenerationStale(t *testing.T, st *Store, generationID string) {
	t.Helper()
	var status string
	var active int
	if err := st.db.QueryRow(`SELECT build_status, active FROM retrieval_index_generations WHERE generation_id = ?`, generationID).Scan(&status, &active); err != nil {
		t.Fatalf("read generation %s: %v", generationID, err)
	}
	if status != string(RetrievalGenerationStale) || active != 0 {
		t.Fatalf("generation %s = status %q active %d, want stale inactive", generationID, status, active)
	}
}

// seedActiveRetrievalGenerationForTest explicitly creates the source-revision
// and membership relationship that the current production schema cannot prove.
// Production activation must remain fail-closed until segmented generations
// persist that provenance themselves.
func seedActiveRetrievalGenerationForTest(t *testing.T, st *Store, generationID string) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var profileID string
	if err := tx.QueryRowContext(ctx, `SELECT profile_id FROM retrieval_index_generations WHERE generation_id=?`, generationID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT latest_revision FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	var indexed int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM retrieval_embeddings WHERE profile_id=? AND status='ready'`, profileID).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE retrieval_index_generations SET active=0,activated_at='' WHERE profile_id=?`, profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE retrieval_index_generations SET active=1,indexed_chunk_count=?,activated_at=? WHERE generation_id=?`, indexed, time.Now().UTC().Format(time.RFC3339), generationID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE retrieval_embedding_profiles SET active_generation_id=?,active_snapshot_revision=?,active_indexed_count=?,l0_ready_count=0,active_tombstone_count=0 WHERE profile_id=?`, generationID, revision, indexed, profileID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func assertProfileAggregatesForTest(t *testing.T, st *Store, profileID, activeGenerationID string, indexed, l0, tombstones int) {
	t.Helper()
	row, err := st.RetrievalEmbeddingProfile(context.Background(), profileID)
	if err != nil {
		t.Fatal(err)
	}
	if row.ActiveGenerationID != activeGenerationID || row.ActiveIndexedCount != indexed || row.L0ReadyCount != l0 || row.ActiveTombstoneCount != tombstones {
		t.Fatalf("profile aggregates=%+v want active=%q indexed=%d l0=%d tombstones=%d", row, activeGenerationID, indexed, l0, tombstones)
	}
}

func assertGenerationActiveForTest(t *testing.T, st *Store, generationID string) {
	t.Helper()
	var status string
	var active int
	if err := st.db.QueryRow(`SELECT build_status,active FROM retrieval_index_generations WHERE generation_id=?`, generationID).Scan(&status, &active); err != nil {
		t.Fatal(err)
	}
	if status != string(RetrievalGenerationCompleted) || active != 1 {
		t.Fatalf("generation %s status=%q active=%d", generationID, status, active)
	}
}

func TestListChunksNeedingEmbeddingExcludesCurrentAndIncludesChanged(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunks := []retrievalchunk.Chunk{
		testRetrievalChunk("chunk-a", "item", "item:one", 0, "hash-a", "alpha"),
		testRetrievalChunk("chunk-b", "item", "item:one", 1, "hash-b", "bravo"),
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:one", chunks); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:one")
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding("chunk-a", "profile-a", "hash-a")); err != nil {
		t.Fatalf("put embedding: %v", err)
	}
	rows, err := st.ListChunksNeedingEmbedding(ctx, "profile-a", "", 10)
	if err != nil {
		t.Fatalf("list chunks needing embeddings: %v", err)
	}
	if len(rows) != 1 || rows[0].ChunkID != "chunk-b" {
		t.Fatalf("chunks needing embeddings = %+v, want chunk-b", rows)
	}
	status, err := st.RetrievalStatus(ctx, "profile-a")
	if err != nil {
		t.Fatalf("retrieval status: %v", err)
	}
	if status.ReadyEmbeddings != 1 || status.PendingEmbeddings != len(rows) {
		t.Fatalf("retrieval status = %+v, want ready=1 pending=%d", status, len(rows))
	}
}

func TestRetrievalStatusEmbeddingCandidatesMatchesCurrentHashErrorSelector(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunk := testRetrievalChunk("chunk-error", "item", "item:error", 0, "hash-error", "error text")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:error", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:error")
	embedding := testEmbedding(chunk.ID, "profile-error", chunk.TextHash)
	embedding.Status = RetrievalEmbeddingError
	embedding.LastError = "provider unavailable"
	if err := st.PutRetrievalEmbedding(ctx, embedding); err != nil {
		t.Fatalf("put error embedding: %v", err)
	}
	candidates, err := st.ListChunksNeedingEmbedding(ctx, embedding.ProfileID, "", 10)
	if err != nil {
		t.Fatalf("list error candidates: %v", err)
	}
	status, err := st.RetrievalStatus(ctx, embedding.ProfileID)
	if err != nil {
		t.Fatalf("retrieval status: %v", err)
	}
	if len(candidates) != 1 || status.EmbeddingCandidates != len(candidates) {
		t.Fatalf("candidates=%+v status=%+v, want selector parity", candidates, status)
	}
	if status.PendingEmbeddings != 0 || status.BlockedEmbeddings != 0 || status.FailedEmbeddings != 1 {
		t.Fatalf("error partition status = %+v, want failed=1 only", status)
	}
}

func TestRetrievalEmbeddingProfileInvariantsRejectMixedProvenance(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunks := []retrievalchunk.Chunk{
		testRetrievalChunk("profile-chunk-a", "item", "item:profile", 0, "hash-a", "alpha"),
		testRetrievalChunk("profile-chunk-b", "item", "item:profile", 1, "hash-b", "bravo"),
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:profile", chunks); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
	first := testEmbedding(chunks[0].ID, "fixed-profile", chunks[0].TextHash)
	if err := st.PutRetrievalEmbedding(ctx, first); err != nil {
		t.Fatalf("put first profile embedding: %v", err)
	}
	mixed := testEmbedding(chunks[1].ID, first.ProfileID, chunks[1].TextHash)
	mixed.Model = "different-model"
	if err := st.PutRetrievalEmbedding(ctx, mixed); err == nil {
		t.Fatal("API accepted mixed model provenance under one profile")
	}
	if _, err := st.db.Exec(`
		INSERT INTO retrieval_embeddings (
			chunk_id, profile_id, provider, model, dimensions, representation,
			normalization, vector_bytes, chunk_text_hash, status, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'ready', ?)`,
		mixed.ChunkID, mixed.ProfileID, mixed.Provider, mixed.Model, mixed.Dimensions,
		mixed.Representation, mixed.Normalization, mixed.VectorBytes, mixed.ChunkTextHash,
		time.Now().UTC().Format(time.RFC3339)); err == nil {
		t.Fatal("SQLite accepted mixed model provenance under one profile")
	}
	if _, err := st.db.Exec(`UPDATE retrieval_embeddings SET model = 'different-model' WHERE chunk_id = ? AND profile_id = ?`, first.ChunkID, first.ProfileID); err == nil {
		t.Fatal("SQLite allowed an existing profile invariant to mutate")
	}
	if _, err := st.db.Exec(`UPDATE retrieval_chunks SET projection_version='different-projection' WHERE chunk_id=?`, chunks[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`
		INSERT INTO retrieval_embeddings (
			chunk_id, profile_id, provider, model, dimensions, representation,
			normalization, vector_bytes, chunk_text_hash, status, revision, vector_hash, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'ready', 2, ?, ?)`,
		chunks[1].ID, first.ProfileID, first.Provider, first.Model, first.Dimensions,
		first.Representation, first.Normalization, first.VectorBytes, chunks[1].TextHash,
		retrievalVectorHash(first.VectorBytes), time.Now().UTC().Format(time.RFC3339)); err == nil {
		t.Fatal("SQLite accepted chunk projection provenance that conflicts with the profile definition")
	}
	for _, trigger := range retrievalEmbeddingProfileTriggersV19[:2] {
		definition := normalizeSQLiteTriggerSQL(trigger.sql)
		if strings.Contains(definition, "fromretrieval_embeddingse") {
			t.Fatalf("profile invariant trigger %s scans retrieval_embeddings: %s", trigger.name, definition)
		}
		if !strings.Contains(definition, "p.profile_id=new.profile_id") || !strings.Contains(definition, "c.chunk_id=new.chunk_id") {
			t.Fatalf("profile invariant trigger %s lacks primary-key profile/chunk lookups: %s", trigger.name, definition)
		}
	}
}

func TestRetrievalMissingTablesAreUnavailableNotErrors(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, source_key TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	_, err = ro.RetrievalStatus(context.Background(), "profile-a")
	if !errors.Is(err, ErrRetrievalUnavailable) {
		t.Fatalf("status error = %v, want ErrRetrievalUnavailable", err)
	}
}
