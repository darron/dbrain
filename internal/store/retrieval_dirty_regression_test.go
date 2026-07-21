package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrievalchunk"
)

func TestProjectedMutationRollbackRestoresAuthoritativeAndRetrievalState(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:rollback-dirty")
	chunk := testRetrievalChunk("rollback-dirty-chunk", "source", "source:rollback-dirty", 0, "rollback-dirty-hash", "source text")
	if _, err := st.ReplaceRetrievalChunks(ctx, "source", "source:rollback-dirty", []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	markProjectionCurrentForTest(t, st, "source", "source:rollback-dirty")
	if err := st.PutRetrievalEmbedding(ctx, testEmbedding(chunk.ID, "rollback-dirty-profile", chunk.TextHash)); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRetrievalIndexGeneration(ctx, RetrievalIndexGenerationRow{
		GenerationID: "rollback-dirty-generation", ProfileID: "rollback-dirty-profile",
		Backend: "exact", BackendVersion: "v1", Dimensions: 2,
		DistanceMetric: "cosine", BuildStatus: RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ActivateRetrievalIndexGeneration(ctx, "rollback-dirty-generation"); err != nil {
		t.Fatal(err)
	}

	type projectionState struct {
		Title                 string
		Revision              int64
		Status                string
		DirtyRevision         int64
		ProjectedRevision     int64
		GenerationStatus      string
		GenerationActive      int
		GenerationActivatedAt string
	}
	readState := func() projectionState {
		t.Helper()
		var got projectionState
		if err := st.db.QueryRow(`SELECT title FROM sources WHERE source_key='source:rollback-dirty'`).Scan(&got.Title); err != nil {
			t.Fatal(err)
		}
		got.Revision = projectionRevisionForTest(t, st)
		if err := st.db.QueryRow(`
			SELECT status, dirty_revision, projected_revision
			FROM retrieval_parent_projections
			WHERE parent_kind='source' AND parent_source_key='source:rollback-dirty'`).Scan(
			&got.Status, &got.DirtyRevision, &got.ProjectedRevision); err != nil {
			t.Fatal(err)
		}
		if err := st.db.QueryRow(`
			SELECT build_status, active, activated_at
			FROM retrieval_index_generations
			WHERE generation_id='rollback-dirty-generation'`).Scan(
			&got.GenerationStatus, &got.GenerationActive, &got.GenerationActivatedAt); err != nil {
			t.Fatal(err)
		}
		return got
	}
	want := readState()
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE sources SET title='rolled back' WHERE source_key='source:rollback-dirty'`); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := readState(); got != want {
		t.Fatalf("rollback changed authoritative/retrieval state: got %+v want %+v", got, want)
	}
}

func TestProjectedEnrichmentTransitionsDirtyExactlyAffectedParentsOnce(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedPurgeItem(t, st, "item:enrichment-transition-a")
	seedPurgeItem(t, st, "item:enrichment-transition-b")
	var itemA, itemB int64
	if err := st.db.QueryRow(`SELECT id FROM items WHERE source_key='item:enrichment-transition-a'`).Scan(&itemA); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT id FROM items WHERE source_key='item:enrichment-transition-b'`).Scan(&itemB); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO item_enrichments (item_id, role, status, text, created_at, updated_at)
		VALUES (?, 'nonprojected', 'ok', 'text', ?, ?)`, itemA, now, now); err != nil {
		t.Fatal(err)
	}

	markProjectionCurrentForTest(t, st, "item", "item:enrichment-transition-a")
	before := projectionRevisionForTest(t, st)
	if _, err := st.db.ExecContext(ctx, `
		UPDATE item_enrichments SET role=? WHERE item_id=? AND role='nonprojected'`,
		model.ItemEnrichmentRoleSummary, itemA); err != nil {
		t.Fatal(err)
	}
	if got := projectionRevisionForTest(t, st); got != before+1 {
		t.Fatalf("nonprojected to projected revision=%d want %d", got, before+1)
	}
	assertProjectionPendingAtRevision(t, st, "item", "item:enrichment-transition-a", before+1)

	markProjectionCurrentForTest(t, st, "item", "item:enrichment-transition-a")
	before = projectionRevisionForTest(t, st)
	if _, err := st.db.ExecContext(ctx, `
		UPDATE item_enrichments SET role='nonprojected' WHERE item_id=? AND role=?`,
		itemA, model.ItemEnrichmentRoleSummary); err != nil {
		t.Fatal(err)
	}
	if got := projectionRevisionForTest(t, st); got != before+1 {
		t.Fatalf("projected to nonprojected revision=%d want %d", got, before+1)
	}
	assertProjectionPendingAtRevision(t, st, "item", "item:enrichment-transition-a", before+1)

	if _, err := st.db.ExecContext(ctx, `
		UPDATE item_enrichments SET role=? WHERE item_id=? AND role='nonprojected'`,
		model.ItemEnrichmentRoleSummary, itemA); err != nil {
		t.Fatal(err)
	}
	markProjectionCurrentForTest(t, st, "item", "item:enrichment-transition-a")
	markProjectionCurrentForTest(t, st, "item", "item:enrichment-transition-b")
	before = projectionRevisionForTest(t, st)
	if _, err := st.db.ExecContext(ctx, `
		UPDATE item_enrichments SET item_id=? WHERE item_id=? AND role=?`,
		itemB, itemA, model.ItemEnrichmentRoleSummary); err != nil {
		t.Fatal(err)
	}
	if got := projectionRevisionForTest(t, st); got != before+1 {
		t.Fatalf("projected enrichment item move revision=%d want %d", got, before+1)
	}
	for _, key := range []string{"item:enrichment-transition-a", "item:enrichment-transition-b"} {
		assertProjectionPendingAtRevision(t, st, "item", key, before+1)
	}
}

func TestProjectedMutationItemSourceKeyMoveUsesOneRevisionForOldCleanupAndNewProjection(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedPurgeItem(t, st, "item:key-old")
	markProjectionCurrentForTest(t, st, "item", "item:key-old")
	before := projectionRevisionForTest(t, st)
	if _, err := st.db.ExecContext(ctx, `UPDATE items SET source_key='item:key-new' WHERE source_key='item:key-old'`); err != nil {
		t.Fatal(err)
	}
	if got := projectionRevisionForTest(t, st); got != before+1 {
		t.Fatalf("item key move revision=%d want %d", got, before+1)
	}
	for _, key := range []string{"item:key-old", "item:key-new"} {
		assertProjectionPendingAtRevision(t, st, "item", key, before+1)
	}
	work, err := st.ListDirtyRetrievalParents(ctx, before+1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(work) != 2 || work[0].Parent.SourceKey != "item:key-new" || work[1].Parent.SourceKey != "item:key-old" ||
		len(work[0].Parent.Sections) == 0 || len(work[1].Parent.Sections) != 0 {
		t.Fatalf("item key move work=%+v", work)
	}
}
