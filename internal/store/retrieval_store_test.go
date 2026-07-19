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

func TestOnlyOneGenerationIsActivePerProfile(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	for _, id := range []string{"generation-a", "generation-b"} {
		if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
			GenerationID: id, ProfileID: "profile-a", Backend: "hnsw", BackendVersion: "1",
			Dimensions: 2, DistanceMetric: "cosine", BuildStatus: RetrievalGenerationCompleted,
		}); err != nil {
			t.Fatalf("put generation %s: %v", id, err)
		}
	}
	if err := st.ActivateRetrievalIndexGeneration(ctx, "generation-a"); err != nil {
		t.Fatalf("activate generation-a: %v", err)
	}
	if err := st.ActivateRetrievalIndexGeneration(ctx, "generation-b"); err != nil {
		t.Fatalf("activate generation-b: %v", err)
	}

	rows, err := st.db.Query(`SELECT generation_id FROM retrieval_index_generations WHERE profile_id = 'profile-a' AND active = 1`)
	if err != nil {
		t.Fatalf("query active generation: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var active []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan active generation: %v", err)
		}
		active = append(active, id)
	}
	if len(active) != 1 || active[0] != "generation-b" {
		t.Fatalf("active generations = %v, want generation-b only", active)
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
	if err := st.ActivateRetrievalIndexGeneration(ctx, "generation-a"); err != nil {
		t.Fatalf("activate generation: %v", err)
	}
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
	assertRetrievalGenerationStale(t, st, "generation-a")

	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "generation-b", ProfileID: "profile-a", Backend: "hnsw", BackendVersion: "1",
		Dimensions: 2, DistanceMetric: "cosine", BuildStatus: RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatalf("put replacement generation: %v", err)
	}
	if err := st.ActivateRetrievalIndexGeneration(ctx, "generation-b"); err != nil {
		t.Fatalf("activate replacement generation: %v", err)
	}
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
	if err := st.ActivateRetrievalIndexGeneration(ctx, "generation-a"); err != nil {
		t.Fatalf("activate generation: %v", err)
	}

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

func TestListReadyEmbeddingsRejectsCorruptStoredVector(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunk := testRetrievalChunk("read-corrupt-chunk", "item", "item:read-corrupt", 0, "read-hash", "alpha")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:read-corrupt", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
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
	if err := st.ActivateRetrievalIndexGeneration(ctx, "read-corrupt-generation"); err != nil {
		t.Fatalf("activate generation: %v", err)
	}
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
	assertRetrievalGenerationStale(t, st, "read-corrupt-generation")
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
	if err := st.ActivateRetrievalIndexGeneration(ctx, "repaired-generation"); err != nil {
		t.Fatalf("activate generation: %v", err)
	}
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

func TestListReadyEmbeddingsRejectsStoredStaleHash(t *testing.T) {
	t.Parallel()
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	chunk := testRetrievalChunk("read-stale-chunk", "item", "item:read-stale", 0, "current-hash", "alpha")
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", "item:read-stale", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
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

func testRetrievalChunk(id, parentKind, parentKey string, ordinal int, textHash, text string) retrievalchunk.Chunk {
	return retrievalchunk.Chunk{
		ID: id, ParentKind: parentKind, ParentSourceKey: parentKey, EvidenceRole: "raw",
		SectionOrdinal: 0, Ordinal: ordinal, StartChar: 0, EndChar: len([]rune(text)),
		Heading: "Heading", ChunkerVersion: retrievalchunk.Version,
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
