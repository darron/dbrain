package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/semanticlock"
)

func TestPurgeItemIndexedContentWaitsForGenerationReader(t *testing.T) {
	st, scope, chunk := openPurgeLockFixture(t, "commit")
	queryLease, err := scope.AcquireGenerationShared(t.Context(), "owner=purge-lock-test-query\n")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queryLease.Close() })
	rows, err := st.HydrateRetrievalChunks(t.Context(), []string{chunk.ID})
	if err != nil || len(rows) != 1 {
		t.Fatalf("hydrate before purge: rows=%+v err=%v", rows, err)
	}

	purgeDone := make(chan error, 1)
	go func() {
		_, purgeErr := st.PurgeItemIndexedContent(context.Background(), chunk.ParentSourceKey, `{"purged":true}`)
		purgeDone <- purgeErr
	}()

	waitForAuthoritativeWriterIntent(t, queryLease.Path(), 2*time.Second)

	maintenanceCtx, cancelMaintenance := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancelMaintenance()
	if lease, err := scope.AcquireMaintenanceShared(maintenanceCtx, "owner=late-authoritative-write\n"); lease != nil || !errors.Is(err, context.DeadlineExceeded) {
		if lease != nil {
			_ = lease.Close()
		}
		t.Fatalf("late maintenance reader lease=%v error=%v, want deadline behind purge maintenance exclusive", lease, err)
	}

	lateHydrateDone := make(chan []RetrievalChunkEvidenceRow, 1)
	lateHydrateErr := make(chan error, 1)
	go func() {
		lease, acquireErr := scope.AcquireGenerationShared(context.Background(), "owner=late-semantic-query\n")
		if acquireErr != nil {
			lateHydrateErr <- acquireErr
			return
		}
		defer func() { _ = lease.Close() }()
		hydrated, hydrateErr := st.HydrateRetrievalChunks(context.Background(), []string{chunk.ID})
		if hydrateErr != nil {
			lateHydrateErr <- hydrateErr
			return
		}
		lateHydrateDone <- hydrated
	}()

	select {
	case err := <-purgeDone:
		t.Fatalf("purge completed while a semantic query held shared generation: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	select {
	case rows := <-lateHydrateDone:
		t.Fatalf("late semantic query barged ahead of purge and hydrated rows: %+v", rows)
	case err := <-lateHydrateErr:
		t.Fatalf("late semantic query failed before purge released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := queryLease.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-purgeDone:
		if err != nil {
			t.Fatalf("purge after query release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("purge did not complete after query released generation")
	}
	select {
	case rows := <-lateHydrateDone:
		if len(rows) != 0 {
			t.Fatalf("semantic query hydrated purged content after waiting for purge commit: %+v", rows)
		}
	case err := <-lateHydrateErr:
		t.Fatalf("late semantic query after purge: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("late semantic query did not resume after purge commit")
	}
}

func TestPurgeItemIndexedContentRejectsStoreWithoutSemanticLockScope(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	t.Cleanup(func() { _ = st.Close() })
	seedPurgeItem(t, st, "apple-note:unlocked-purge")

	purged, err := st.PurgeItemIndexedContent(t.Context(), "apple-note:unlocked-purge", `{"purged":true}`)
	if purged || err == nil || !strings.Contains(err.Error(), "semantic purge lock scope is not configured") {
		t.Fatalf("PurgeItemIndexedContent() = (%t, %v), want explicit unconfigured semantic lock error", purged, err)
	}
}

func TestPurgeItemIndexedContentHoldsSemanticLocksThroughRollback(t *testing.T) {
	st, scope, chunk := openPurgeLockFixture(t, "rollback")
	if _, err := st.db.Exec(`DROP TABLE items_fts`); err != nil {
		t.Fatalf("force terminal purge error: %v", err)
	}

	queryLease, err := scope.AcquireGenerationShared(t.Context(), "owner=rollback-query\n")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queryLease.Close() })
	purgeDone := make(chan error, 1)
	go func() {
		_, purgeErr := st.PurgeItemIndexedContent(context.Background(), chunk.ParentSourceKey, `{"purged":true}`)
		purgeDone <- purgeErr
	}()
	waitForAuthoritativeWriterIntent(t, queryLease.Path(), 2*time.Second)

	lateHydrateDone := make(chan []RetrievalChunkEvidenceRow, 1)
	lateHydrateErr := make(chan error, 1)
	go func() {
		lease, acquireErr := scope.AcquireGenerationShared(context.Background(), "owner=late-rollback-query\n")
		if acquireErr != nil {
			lateHydrateErr <- acquireErr
			return
		}
		defer func() { _ = lease.Close() }()
		hydrated, hydrateErr := st.HydrateRetrievalChunks(context.Background(), []string{chunk.ID})
		if hydrateErr != nil {
			lateHydrateErr <- hydrateErr
			return
		}
		lateHydrateDone <- hydrated
	}()

	select {
	case rows := <-lateHydrateDone:
		t.Fatalf("late semantic query barged ahead of purge rollback and hydrated rows: %+v", rows)
	case err := <-lateHydrateErr:
		t.Fatalf("late semantic query failed before purge rollback: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := queryLease.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-purgeDone:
		if err == nil || !strings.Contains(err.Error(), "delete purged item fts") {
			t.Fatalf("purge error=%v, want forced FTS failure", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("purge rollback did not complete after query released generation")
	}
	select {
	case rows := <-lateHydrateDone:
		if len(rows) != 1 || rows[0].ChunkID != chunk.ID {
			t.Fatalf("rollback did not restore content for later query: %+v", rows)
		}
	case err := <-lateHydrateErr:
		t.Fatalf("late semantic query after rollback: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("late semantic query did not resume after purge rollback")
	}
}

func openPurgeLockFixture(t *testing.T, suffix string) (*Store, *semanticlock.Scope, retrievalchunk.Chunk) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "brain.db")
	cacheDir := t.TempDir()
	st, err := OpenWithSemanticCache(dbPath, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sourceKey := "apple-note:purge-lock-" + suffix
	seedPurgeItem(t, st, sourceKey)
	chunk := testRetrievalChunk("purge-lock-chunk-"+suffix, "item", sourceKey, 0, "purge-lock-hash-"+suffix, "private")
	if _, err := st.ReplaceRetrievalChunks(t.Context(), "item", sourceKey, []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("seed retrieval chunk: %v", err)
	}
	markProjectionCurrentForTest(t, st, "item", sourceKey)
	if err := st.PutRetrievalEmbedding(t.Context(), testEmbedding(chunk.ID, "purge-lock-profile", chunk.TextHash)); err != nil {
		t.Fatalf("seed retrieval embedding: %v", err)
	}
	if err := st.PutRetrievalIndexGeneration(t.Context(), RetrievalIndexGenerationRow{
		GenerationID:   "purge-lock-generation-" + suffix,
		ProfileID:      "purge-lock-profile",
		Backend:        "hnsw",
		BackendVersion: "1",
		Dimensions:     2,
		DistanceMetric: "cosine",
		BuildStatus:    RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatalf("seed retrieval generation: %v", err)
	}
	seedActiveRetrievalGenerationForTest(t, st, "purge-lock-generation-"+suffix)

	databaseID, err := st.RetrievalDatabaseID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := semanticlock.NewScope(cacheDir, databaseID)
	if err != nil {
		t.Fatal(err)
	}
	return st, scope, chunk
}
