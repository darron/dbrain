package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlanRetrievalSemanticGCRetainsRecentlyStaledOldRoot(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	now := time.Date(2026, time.August, 6, 3, 0, 0, 0, time.UTC)
	seedSemanticGCProfile(t, st, "profile-a", "")
	seedSemanticGCGeneration(t, st, semanticGCGenerationFixture{
		generationID: "old-root", profileID: "profile-a", segmentHash: "old-segment",
		status: RetrievalGenerationStale, createdAt: now.Add(-24 * time.Hour), updatedAt: now.Add(-time.Minute),
	})

	plan, err := st.PlanRetrievalSemanticGC(context.Background(), RetrievalSemanticGCOptions{
		Now: now, GracePeriod: 10 * time.Minute, RetainPublished: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !semanticGCArtifactContains(plan.RetainedGenerations, "old-root") {
		t.Fatalf("recently staled old root was not retained: %+v", plan)
	}
	if semanticGCArtifactContains(plan.PrunableSegments, "old-segment") {
		t.Fatalf("recently staled root segment was prunable: %+v", plan)
	}
}

func TestPlanRetrievalSemanticGCRetainsPublishedRollbackAndSharedSegments(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	now := time.Date(2026, time.August, 6, 3, 0, 0, 0, time.UTC)
	seedSemanticGCProfile(t, st, "profile-a", "active-root")
	fixtures := []semanticGCGenerationFixture{
		{generationID: "old-root", profileID: "profile-a", segmentHash: "old-only", status: RetrievalGenerationStale, createdAt: now.Add(-4 * time.Hour), updatedAt: now.Add(-4 * time.Hour)},
		{generationID: "rollback-root", profileID: "profile-a", segmentHash: "shared", status: RetrievalGenerationStale, createdAt: now.Add(-3 * time.Hour), updatedAt: now.Add(-3 * time.Hour)},
		{generationID: "active-root", profileID: "profile-a", segmentHash: "shared", status: RetrievalGenerationCompleted, active: true, createdAt: now.Add(-2 * time.Hour), updatedAt: now.Add(-2 * time.Hour)},
		{generationID: "error-root", profileID: "profile-a", segmentHash: "error-only", status: RetrievalGenerationError, createdAt: now.Add(-time.Hour), updatedAt: now.Add(-time.Hour)},
	}
	for _, fixture := range fixtures {
		seedSemanticGCGeneration(t, st, fixture)
	}

	plan, err := st.PlanRetrievalSemanticGC(context.Background(), RetrievalSemanticGCOptions{
		Now: now, GracePeriod: 10 * time.Minute, RetainPublished: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, generationID := range []string{"active-root", "rollback-root"} {
		if !semanticGCArtifactContains(plan.RetainedGenerations, generationID) {
			t.Fatalf("published generation %s was not retained: %+v", generationID, plan)
		}
	}
	for _, generationID := range []string{"old-root", "error-root"} {
		if !semanticGCArtifactContains(plan.PrunableGenerations, generationID) {
			t.Fatalf("generation %s was not prunable: %+v", generationID, plan)
		}
	}
	if semanticGCArtifactContains(plan.PrunableSegments, "shared") {
		t.Fatalf("shared retained segment was prunable: %+v", plan)
	}
	for _, segmentHash := range []string{"old-only", "error-only"} {
		if !semanticGCArtifactContains(plan.PrunableSegments, segmentHash) {
			t.Fatalf("unreachable segment %s was not prunable: %+v", segmentHash, plan)
		}
	}
	if plan.PrunableMemberRows != 2 {
		t.Fatalf("prunable member rows=%d want 2", plan.PrunableMemberRows)
	}
}

func TestPruneRetrievalSemanticCatalogDeletesOnlyRecomputedUnreachableRows(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	now := time.Date(2026, time.August, 6, 3, 0, 0, 0, time.UTC)
	seedSemanticGCProfile(t, st, "profile-a", "active-root")
	for _, fixture := range []semanticGCGenerationFixture{
		{generationID: "dead-root", profileID: "profile-a", segmentHash: "dead-segment", status: RetrievalGenerationError, createdAt: now.Add(-2 * time.Hour), updatedAt: now.Add(-2 * time.Hour)},
		{generationID: "active-root", profileID: "profile-a", segmentHash: "live-segment", status: RetrievalGenerationCompleted, active: true, createdAt: now.Add(-time.Hour), updatedAt: now.Add(-time.Hour)},
	} {
		seedSemanticGCGeneration(t, st, fixture)
	}
	preview, err := st.PlanRetrievalSemanticGC(context.Background(), RetrievalSemanticGCOptions{Now: now, GracePeriod: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_index_generations SET updated_at=? WHERE generation_id='dead-root'`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	applied, err := st.PruneRetrievalSemanticCatalog(context.Background(), RetrievalSemanticGCOptions{Now: now, GracePeriod: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if !semanticGCArtifactContains(preview.PrunableGenerations, "dead-root") {
		t.Fatalf("dry-run did not initially select dead root: %+v", preview)
	}
	if semanticGCArtifactContains(applied.PrunableGenerations, "dead-root") {
		t.Fatalf("apply trusted stale dry-run instead of recomputing: %+v", applied)
	}
	for table := range map[string]struct{}{
		"retrieval_index_generations": {}, "retrieval_index_segments": {}, "retrieval_index_segment_members": {},
	} {
		var count int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Fatalf("%s count=%d want 2 after recomputed no-op", table, count)
		}
	}
}

func TestPruneRetrievalSemanticCatalogRollsBackOnForeignKeyFailure(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	now := time.Date(2026, time.August, 6, 3, 0, 0, 0, time.UTC)
	seedSemanticGCProfile(t, st, "profile-a", "")
	seedSemanticGCGeneration(t, st, semanticGCGenerationFixture{
		generationID: "dead-root", profileID: "profile-a", segmentHash: "dead-segment",
		status: RetrievalGenerationError, createdAt: now.Add(-time.Hour), updatedAt: now.Add(-time.Hour),
	})
	if _, err := st.db.Exec(`CREATE TRIGGER fail_gc_delete BEFORE DELETE ON retrieval_index_segment_members BEGIN SELECT RAISE(ABORT,'forced GC rollback'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := st.PruneRetrievalSemanticCatalog(context.Background(), RetrievalSemanticGCOptions{Now: now})
	if err == nil || !strings.Contains(err.Error(), "forced GC rollback") {
		t.Fatalf("PruneRetrievalSemanticCatalog error=%v", err)
	}
	for table := range map[string]struct{}{
		"retrieval_index_generations": {}, "retrieval_generation_segments": {}, "retrieval_index_segments": {}, "retrieval_index_segment_members": {},
	} {
		var count int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s count=%d want 1 after rollback", table, count)
		}
	}
}

func TestPruneRetrievalSemanticCatalogIsIdempotent(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	now := time.Date(2026, time.August, 6, 3, 0, 0, 0, time.UTC)
	seedSemanticGCProfile(t, st, "profile-a", "")
	seedSemanticGCGeneration(t, st, semanticGCGenerationFixture{
		generationID: "dead-root", profileID: "profile-a", segmentHash: "dead-segment",
		status: RetrievalGenerationError, createdAt: now.Add(-time.Hour), updatedAt: now.Add(-time.Hour),
	})
	first, err := st.PruneRetrievalSemanticCatalog(context.Background(), RetrievalSemanticGCOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.PruneRetrievalSemanticCatalog(context.Background(), RetrievalSemanticGCOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.PrunableGenerations) != 1 || len(first.PrunableSegments) != 1 || len(second.PrunableGenerations) != 0 || len(second.PrunableSegments) != 0 || second.PrunableMemberRows != 0 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestVacuumRetrievalDatabaseReclaimsPagesAndPreservesRows(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	if _, err := st.db.Exec(`
		CREATE TABLE vacuum_gc_test (id INTEGER PRIMARY KEY,payload BLOB);
		WITH RECURSIVE counter(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM counter WHERE value<1000)
		INSERT INTO vacuum_gc_test(payload) SELECT zeroblob(8192) FROM counter`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DELETE FROM vacuum_gc_test WHERE id > 1`); err != nil {
		t.Fatal(err)
	}
	var before, freelist int
	if err := st.db.QueryRow(`PRAGMA page_count`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`PRAGMA freelist_count`).Scan(&freelist); err != nil {
		t.Fatal(err)
	}
	if freelist == 0 {
		t.Fatal("test fixture created no reclaimable SQLite pages")
	}
	if err := st.VacuumRetrievalDatabase(context.Background()); err != nil {
		t.Fatal(err)
	}
	var after, retained int
	if err := st.db.QueryRow(`PRAGMA page_count`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM vacuum_gc_test`).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if after >= before || retained != 1 {
		t.Fatalf("VACUUM page_count before=%d after=%d retained=%d", before, after, retained)
	}
}

func TestRetrievalSemanticGCVacuumBusyErrorIsActionable(t *testing.T) {
	t.Parallel()
	err := retrievalSemanticGCVacuumError("VACUUM", errors.New("database is locked (5) (SQLITE_BUSY)"))
	if !strings.Contains(err.Error(), "stop the dbrain daemon and other writers") {
		t.Fatalf("busy error=%v", err)
	}
}

func TestVacuumRetrievalDatabaseReportsLiveWriterContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openCurrentTestStoreAtPath(t, path)
	defer func() { _ = st.Close() }()
	if _, err := st.db.Exec(`PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	blocker, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Close() }()
	if _, err := blocker.Exec(`PRAGMA busy_timeout=1; BEGIN IMMEDIATE; CREATE TABLE vacuum_blocker (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = blocker.Exec(`ROLLBACK`) }()
	err = st.VacuumRetrievalDatabase(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stop the dbrain daemon and other writers") {
		t.Fatalf("contention error=%v", err)
	}
}

func TestPlanRetrievalSemanticGCRejectsUnsafeOptions(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	for _, opts := range []RetrievalSemanticGCOptions{
		{},
		{Now: time.Now(), GracePeriod: -time.Second},
		{Now: time.Now(), RetainPublished: -1},
	} {
		if _, err := st.PlanRetrievalSemanticGC(context.Background(), opts); err == nil {
			t.Fatalf("PlanRetrievalSemanticGC(%+v) succeeded", opts)
		}
	}
}

type semanticGCGenerationFixture struct {
	generationID, profileID, segmentHash string
	status                               RetrievalGenerationStatus
	active                               bool
	createdAt, updatedAt                 time.Time
}

func seedSemanticGCProfile(t *testing.T, st *Store, profileID, activeGenerationID string) {
	t.Helper()
	if _, err := st.db.Exec(`
		INSERT INTO retrieval_embedding_profiles (profile_id,active_generation_id,updated_at)
		VALUES (?,?,?)`, profileID, activeGenerationID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
}

func seedSemanticGCGeneration(t *testing.T, st *Store, fixture semanticGCGenerationFixture) {
	t.Helper()
	active := 0
	activatedAt := ""
	if fixture.active {
		active = 1
		activatedAt = fixture.updatedAt.Format(time.RFC3339Nano)
	}
	rootPath := "semantic/database/" + fixture.profileID + "/generations/" + fixture.generationID
	if _, err := st.db.Exec(`
		INSERT INTO retrieval_index_generations (
			generation_id,profile_id,backend,backend_version,dimensions,distance_metric,indexed_chunk_count,
			source_manifest_hash,build_status,relative_cache_path,activated_at,active,created_at,updated_at
		) VALUES (?,?,'usearch','v1',2,'cosine',1,'root-manifest',?,?,?,?,?,?)`,
		fixture.generationID, fixture.profileID, fixture.status, rootPath, activatedAt, active,
		fixture.createdAt.Format(time.RFC3339Nano), fixture.updatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	var segmentCount int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_index_segments WHERE segment_hash=?`, fixture.segmentHash).Scan(&segmentCount); err != nil {
		t.Fatal(err)
	}
	if segmentCount == 0 {
		segmentPath := "semantic/database/" + fixture.profileID + "/segments/" + fixture.segmentHash
		if _, err := st.db.Exec(`
			INSERT INTO retrieval_index_segments (
				segment_hash,profile_id,backend,backend_version,dimensions,distance_metric,indexed_chunk_count,
				relative_cache_path,membership_hash,payload_hash,manifest_hash,created_at
			) VALUES (?,?,'usearch','v1',2,'cosine',1,?,'members','payload','manifest',?)`,
			fixture.segmentHash, fixture.profileID, segmentPath, fixture.createdAt.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		if _, err := st.db.Exec(`
			INSERT INTO retrieval_index_segment_members (segment_hash,ordinal,chunk_id,revision,vector_hash)
			VALUES (?,0,?,1,?)`, fixture.segmentHash, "chunk-"+fixture.segmentHash, "vector-"+fixture.segmentHash); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.db.Exec(`INSERT INTO retrieval_generation_segments (generation_id,segment_hash) VALUES (?,?)`, fixture.generationID, fixture.segmentHash); err != nil {
		t.Fatal(err)
	}
}

func semanticGCArtifactContains(artifacts []RetrievalSemanticGCArtifact, id string) bool {
	for _, artifact := range artifacts {
		if artifact.ID == id {
			return true
		}
	}
	return false
}
