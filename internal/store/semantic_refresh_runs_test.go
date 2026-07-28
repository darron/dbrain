package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func semanticRefreshTestNow() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) }

func startSemanticRefreshRunForTest(t *testing.T, st *Store, runID, profileID string, epoch, watermark int64) SemanticRefreshRun {
	t.Helper()
	run, resumed, err := st.StartOrResumeSemanticRefreshRun(t.Context(), StartSemanticRefreshRunInput{RunID: runID, ProfileID: profileID, PurgeEpoch: epoch, ProjectionWatermark: watermark, Now: semanticRefreshTestNow()})
	if err != nil || resumed {
		t.Fatalf("start run err=%v resumed=%v", err, resumed)
	}
	return run
}

func TestSemanticRefreshRunResumePreservesImmutableWatermark(t *testing.T) {
	st := openTestStore(t)
	run := startSemanticRefreshRunForTest(t, st, "run-1", "profile-a", 3, 41)
	updated, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshEmbedding, State: SemanticRefreshRunFailed, Counters: SemanticRefreshCounters{ProjectedParents: 7}, ErrorCode: "temporary", ErrorText: "retry", Now: semanticRefreshTestNow().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	resumed, didResume, err := st.StartOrResumeSemanticRefreshRun(t.Context(), StartSemanticRefreshRunInput{RunID: "ignored", ProfileID: "profile-a", PurgeEpoch: 3, ProjectionWatermark: 99, Now: semanticRefreshTestNow().Add(2 * time.Minute)})
	if err != nil || !didResume {
		t.Fatalf("resume err=%v resumed=%v", err, didResume)
	}
	if resumed.RunID != run.RunID || resumed.ProjectionWatermark != 41 || resumed.Counters.ProjectedParents != 7 || resumed.State != SemanticRefreshRunRunning || resumed.ErrorCode != "" || resumed.Version != updated.Version+1 {
		t.Fatalf("resumed=%+v", resumed)
	}
}

func TestSemanticRefreshRunProfileChangeSupersedesOldRun(t *testing.T) {
	st := openTestStore(t)
	old := startSemanticRefreshRunForTest(t, st, "run-a", "profile-a", 1, 1)
	fresh := startSemanticRefreshRunForTest(t, st, "run-b", "profile-b", 1, 2)
	if fresh.State != SemanticRefreshRunRunning {
		t.Fatalf("fresh=%+v", fresh)
	}
	got, err := st.LatestSemanticRefreshRun(t.Context(), "profile-a")
	if err != nil || got == nil || got.RunID != old.RunID || got.State != SemanticRefreshRunSuperseded {
		t.Fatalf("old=%+v err=%v", got, err)
	}
}

func TestSemanticRefreshRunPurgeEpochChangeSupersedesOldRun(t *testing.T) {
	st := openTestStore(t)
	old := startSemanticRefreshRunForTest(t, st, "run-a", "profile-a", 1, 11)
	fresh := startSemanticRefreshRunForTest(t, st, "run-b", "profile-a", 2, 12)
	if fresh.RunID != "run-b" || fresh.PurgeEpoch != 2 {
		t.Fatalf("fresh=%+v", fresh)
	}
	got, err := st.LatestSemanticRefreshRun(t.Context(), "profile-a")
	if err != nil || got == nil || got.RunID != fresh.RunID {
		t.Fatalf("latest=%+v err=%v", got, err)
	}
	var state SemanticRefreshRunState
	if err := st.db.QueryRow(`SELECT state FROM semantic_refresh_runs WHERE run_id=?`, old.RunID).Scan(&state); err != nil || state != SemanticRefreshRunSuperseded {
		t.Fatalf("old state=%q err=%v", state, err)
	}
}

func TestSemanticRefreshRunCASRejectsStaleWriter(t *testing.T) {
	st := openTestStore(t)
	run := startSemanticRefreshRunForTest(t, st, "run-a", "profile-a", 1, 11)
	if _, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshEmbedding, State: SemanticRefreshRunRunning, Now: semanticRefreshTestNow().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshFlush, State: SemanticRefreshRunRunning, Now: semanticRefreshTestNow().Add(2 * time.Minute)}); !errors.Is(err, ErrSemanticRefreshRunStale) {
		t.Fatalf("stale update err=%v", err)
	}
}

func TestSemanticRefreshRunUpdateReturnsOwnCASWithQueuedWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	callerStore := openStoreAtPath(t, path)
	t.Cleanup(func() { _ = callerStore.Close() })
	competingStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = competingStore.Close() })
	run := startSemanticRefreshRunForTest(t, callerStore, "run-a", "profile-a", 1, 11)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	callerWrote := make(chan struct{})
	competitorReady := make(chan struct{})
	competitorDone := make(chan struct{})
	wait := func(ch <-chan struct{}) {
		select {
		case <-ch:
		case <-ctx.Done():
		}
	}
	callerCtx := context.WithValue(ctx, semanticRefreshRunUpdateTestHooksKey{}, semanticRefreshRunUpdateTestHooks{
		AfterWrite: func() {
			close(callerWrote)
			wait(competitorReady)
		},
		AfterCommit: func() {
			wait(competitorDone)
		},
	})
	competitorCtx := context.WithValue(ctx, semanticRefreshRunUpdateTestHooksKey{}, semanticRefreshRunUpdateTestHooks{
		BeforeWrite: func() {
			close(competitorReady)
		},
	})

	type updateResult struct {
		run SemanticRefreshRun
		err error
	}
	callerResult := make(chan updateResult, 1)
	go func() {
		got, err := callerStore.UpdateSemanticRefreshRun(callerCtx, SemanticRefreshRunUpdate{
			RunID: run.RunID, ExpectedVersion: run.Version, EmbeddingRevision: 9,
			Stage: SemanticRefreshFlush, State: SemanticRefreshRunRunning, Checkpoint: "caller-flush-9",
			Counters: SemanticRefreshCounters{ProjectedParents: 2, EmbeddedChunks: 3, FlushedVectors: 4},
			Now:      semanticRefreshTestNow().Add(time.Minute),
		})
		callerResult <- updateResult{run: got, err: err}
	}()
	wait(callerWrote)
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	competitorResult := make(chan updateResult, 1)
	go func() {
		got, err := competingStore.UpdateSemanticRefreshRun(competitorCtx, SemanticRefreshRunUpdate{
			RunID: run.RunID, ExpectedVersion: run.Version + 1, EmbeddingRevision: 10,
			Stage: SemanticRefreshCompaction, State: SemanticRefreshRunRunning, Checkpoint: "competitor-compaction-10",
			Counters: SemanticRefreshCounters{ProjectedParents: 5, EmbeddedChunks: 6, FlushedVectors: 7, CompactedVectors: 8},
			Now:      semanticRefreshTestNow().Add(2 * time.Minute),
		})
		competitorResult <- updateResult{run: got, err: err}
		close(competitorDone)
	}()

	caller := <-callerResult
	competitor := <-competitorResult
	if caller.err != nil || competitor.err != nil {
		t.Fatalf("caller err=%v competitor err=%v", caller.err, competitor.err)
	}
	if caller.run.Version != run.Version+1 || caller.run.Stage != SemanticRefreshFlush || caller.run.Checkpoint != "caller-flush-9" || caller.run.Counters.FlushedVectors != 4 {
		t.Fatalf("caller returned competitor or stale snapshot: %+v", caller.run)
	}
	if competitor.run.Version != run.Version+2 || competitor.run.Stage != SemanticRefreshCompaction || competitor.run.Checkpoint != "competitor-compaction-10" || competitor.run.Counters.CompactedVectors != 8 {
		t.Fatalf("competitor snapshot=%+v", competitor.run)
	}
	stored, err := callerStore.LatestSemanticRefreshRun(t.Context(), "profile-a")
	if err != nil || stored == nil || stored.Version != competitor.run.Version || stored.Checkpoint != competitor.run.Checkpoint || stored.Counters != competitor.run.Counters {
		t.Fatalf("stored=%+v competitor=%+v err=%v", stored, competitor.run, err)
	}
}

func TestSemanticRefreshRunCASRejectsTerminalRows(t *testing.T) {
	for _, state := range []SemanticRefreshRunState{SemanticRefreshRunCompleted, SemanticRefreshRunSuperseded} {
		t.Run(string(state), func(t *testing.T) {
			st := openTestStore(t)
			run := startSemanticRefreshRunForTest(t, st, "run-a", "profile-a", 1, 11)
			expectedVersion := run.Version
			if state == SemanticRefreshRunSuperseded {
				if _, _, err := st.StartOrResumeSemanticRefreshRun(t.Context(), StartSemanticRefreshRunInput{RunID: "run-b", ProfileID: "profile-b", PurgeEpoch: 1, ProjectionWatermark: 12, Now: semanticRefreshTestNow().Add(time.Minute)}); err != nil {
					t.Fatal(err)
				}
				terminal, err := st.LatestSemanticRefreshRun(t.Context(), "profile-a")
				if err != nil || terminal == nil {
					t.Fatalf("read superseded run=%+v err=%v", terminal, err)
				}
				expectedVersion = terminal.Version
			} else {
				terminal, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshReadiness, State: state, Now: semanticRefreshTestNow().Add(time.Minute)})
				if err != nil {
					t.Fatal(err)
				}
				expectedVersion = terminal.Version
			}
			if _, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: expectedVersion, Stage: SemanticRefreshEmbedding, State: SemanticRefreshRunRunning, Now: semanticRefreshTestNow().Add(2 * time.Minute)}); !errors.Is(err, ErrSemanticRefreshRunStale) {
				t.Fatalf("terminal revival err=%v", err)
			}
		})
	}
}

func TestSemanticRefreshRunFailedAndCancelledStatesResume(t *testing.T) {
	for _, state := range []SemanticRefreshRunState{SemanticRefreshRunFailed, SemanticRefreshRunCancelled} {
		t.Run(string(state), func(t *testing.T) {
			st := openTestStore(t)
			run := startSemanticRefreshRunForTest(t, st, "run-a", "profile-a", 1, 11)
			_, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshEmbedding, State: state, Now: semanticRefreshTestNow().Add(time.Minute)})
			if err != nil {
				t.Fatal(err)
			}
			got, resumed, err := st.StartOrResumeSemanticRefreshRun(t.Context(), StartSemanticRefreshRunInput{RunID: "run-b", ProfileID: "profile-a", PurgeEpoch: 1, ProjectionWatermark: 12, Now: semanticRefreshTestNow().Add(2 * time.Minute)})
			if err != nil || !resumed || got.RunID != run.RunID || got.State != SemanticRefreshRunRunning {
				t.Fatalf("got=%+v resumed=%v err=%v", got, resumed, err)
			}
		})
	}
}

func TestSemanticRefreshRunBoundsCheckpointAndDiagnostics(t *testing.T) {
	st := openTestStore(t)
	run := startSemanticRefreshRunForTest(t, st, "run-a", "profile-a", 1, 11)
	if _, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshEmbedding, State: SemanticRefreshRunRunning, Checkpoint: strings.Repeat("x", 257), Now: semanticRefreshTestNow()}); err == nil {
		t.Fatal("expected checkpoint bound violation")
	}
	if _, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshEmbedding, State: SemanticRefreshRunRunning, ErrorCode: strings.Repeat("x", 65), Now: semanticRefreshTestNow()}); err == nil {
		t.Fatal("expected error code bound violation")
	}
	if _, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshEmbedding, State: SemanticRefreshRunRunning, Counters: SemanticRefreshCounters{EmbeddedChunks: -1}, Now: semanticRefreshTestNow()}); err == nil {
		t.Fatal("expected counter bound violation")
	}
	if _, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshEmbedding, State: SemanticRefreshRunRunning, Checkpoint: strings.Repeat("é", 129), Now: semanticRefreshTestNow()}); err == nil {
		t.Fatal("expected UTF-8 checkpoint byte bound violation")
	}
	if _, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshEmbedding, State: SemanticRefreshRunRunning, ErrorText: strings.Repeat("é", 257), Now: semanticRefreshTestNow()}); err == nil {
		t.Fatal("expected UTF-8 error text byte bound violation")
	}
	if _, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshEmbedding, State: SemanticRefreshRunRunning, ErrorCode: strings.Repeat("é", 33), Now: semanticRefreshTestNow()}); err == nil {
		t.Fatal("expected UTF-8 error code byte bound violation")
	}
	if _, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshEmbedding, State: SemanticRefreshRunRunning, ReadinessState: strings.Repeat("é", 33), Now: semanticRefreshTestNow()}); err == nil {
		t.Fatal("expected UTF-8 readiness state byte bound violation")
	}
	if _, _, err := st.StartOrResumeSemanticRefreshRun(t.Context(), StartSemanticRefreshRunInput{RunID: strings.Repeat("é", 33), ProfileID: "profile-b", Now: semanticRefreshTestNow()}); err == nil {
		t.Fatal("expected UTF-8 run ID byte bound violation")
	}
	if _, _, err := st.StartOrResumeSemanticRefreshRun(t.Context(), StartSemanticRefreshRunInput{RunID: "run-profile-multibyte", ProfileID: strings.Repeat("é", 97), Now: semanticRefreshTestNow()}); err == nil {
		t.Fatal("expected UTF-8 profile byte bound violation")
	}
}

func TestLatestSemanticRefreshRunFiltersProfileOrReturnsDatabaseLatest(t *testing.T) {
	st := openTestStore(t)
	first := startSemanticRefreshRunForTest(t, st, "run-a", "profile-a", 1, 11)
	second := startSemanticRefreshRunForTest(t, st, "run-b", "profile-b", 1, 12)
	got, err := st.LatestSemanticRefreshRun(t.Context(), "profile-a")
	if err != nil || got == nil || got.RunID != first.RunID {
		t.Fatalf("profile latest=%+v err=%v", got, err)
	}
	got, err = st.LatestSemanticRefreshRun(t.Context(), "")
	if err != nil || got == nil || got.RunID != second.RunID {
		t.Fatalf("database latest=%+v err=%v", got, err)
	}
}

func TestLatestSemanticRefreshRunOrdersFixedWidthTimestampsWithinSecond(t *testing.T) {
	st := openTestStore(t)
	first := startSemanticRefreshRunForTest(t, st, "run-a", "profile-a", 1, 11)
	if _, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: first.RunID, ExpectedVersion: first.Version, Stage: SemanticRefreshReadiness, State: SemanticRefreshRunCompleted, Now: semanticRefreshTestNow()}); err != nil {
		t.Fatal(err)
	}
	_, resumed, err := st.StartOrResumeSemanticRefreshRun(t.Context(), StartSemanticRefreshRunInput{RunID: "run-b", ProfileID: "profile-b", PurgeEpoch: 1, ProjectionWatermark: 12, Now: semanticRefreshTestNow().Add(100 * time.Millisecond)})
	if err != nil || resumed {
		t.Fatalf("start later run resumed=%v err=%v", resumed, err)
	}
	got, err := st.LatestSemanticRefreshRun(t.Context(), "")
	if err != nil || got == nil || got.RunID != "run-b" {
		t.Fatalf("latest=%+v err=%v", got, err)
	}
}

func TestSemanticRefreshRunRejectsCorruptStoredTimestamp(t *testing.T) {
	st := openTestStore(t)
	run := startSemanticRefreshRunForTest(t, st, "run-a", "profile-a", 1, 11)
	if _, err := st.db.Exec(`UPDATE semantic_refresh_runs SET updated_at='not-a-time' WHERE run_id=?`, run.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LatestSemanticRefreshRun(t.Context(), "profile-a"); err == nil {
		t.Fatal("expected corrupt timestamp error")
	}
}

func TestSemanticRefreshRunTouchDoesNotInvalidateCAS(t *testing.T) {
	st := openTestStore(t)
	run := startSemanticRefreshRunForTest(t, st, "run-a", "profile-a", 1, 11)
	if err := st.TouchSemanticRefreshRunProgress(context.Background(), run.RunID, semanticRefreshTestNow().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshEmbedding, State: SemanticRefreshRunRunning, Now: semanticRefreshTestNow().Add(2 * time.Minute)}); err != nil {
		t.Fatalf("CAS after touch: %v", err)
	}
}

func TestSemanticRefreshRunTouchRejectsMissingRun(t *testing.T) {
	st := openTestStore(t)
	if err := st.TouchSemanticRefreshRunProgress(t.Context(), "missing", semanticRefreshTestNow()); !errors.Is(err, ErrSemanticRefreshRunStale) {
		t.Fatalf("missing touch err=%v", err)
	}
}

func TestSemanticRefreshRunTouchRejectsTerminalRowsAndCannotReorderLatest(t *testing.T) {
	st := openTestStore(t)
	old := startSemanticRefreshRunForTest(t, st, "run-a", "profile-a", 1, 11)
	completed, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: old.RunID, ExpectedVersion: old.Version, Stage: SemanticRefreshReadiness, State: SemanticRefreshRunCompleted, Now: semanticRefreshTestNow()})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = st.StartOrResumeSemanticRefreshRun(t.Context(), StartSemanticRefreshRunInput{RunID: "run-b", ProfileID: "profile-b", PurgeEpoch: 1, ProjectionWatermark: 12, Now: semanticRefreshTestNow().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = st.StartOrResumeSemanticRefreshRun(t.Context(), StartSemanticRefreshRunInput{RunID: "run-c", ProfileID: "profile-c", PurgeEpoch: 1, ProjectionWatermark: 13, Now: semanticRefreshTestNow().Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	for _, runID := range []string{completed.RunID, "run-b"} {
		if err := st.TouchSemanticRefreshRunProgress(t.Context(), runID, semanticRefreshTestNow().Add(2*time.Minute)); !errors.Is(err, ErrSemanticRefreshRunStale) {
			t.Fatalf("touch %s err=%v", runID, err)
		}
	}
	latest, err := st.LatestSemanticRefreshRun(t.Context(), "")
	if err != nil || latest == nil || latest.RunID != "run-c" {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}

func TestSemanticRefreshRunsMigrationV26RepairsGenuineV25Database(t *testing.T) {
	path := t.TempDir() + "/brain.db"
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE semantic_refresh_runs`); err != nil {
		t.Fatal(err)
	}
	if err := ensureSemanticRefreshRunSchemaV25(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO semantic_refresh_runs (` + semanticRefreshRunColumns + `) VALUES ('old-run','profile-a',1,2,3,'embedding','checkpoint',4,5,6,7,8,9,'generation','failed','code','text','not_ready',4,'2026-07-28T12:00:00Z','2026-07-28T12:00:00.1Z','2026-07-28T12:00:00.01Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version>25`); err != nil {
		t.Fatal(err)
	}
	var removed int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, semanticRefreshRunsRepairMigrationVersion).Scan(&removed); err != nil || removed != 0 {
		t.Fatalf("v26 removal count=%d err=%v", removed, err)
	}
	if _, err := db.Exec(`PRAGMA user_version=25`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var events []MigrationEvent
	st, err = OpenWithOptions(path, OpenOptions{MigrationReporter: func(event MigrationEvent) { events = append(events, event) }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var before int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, semanticRefreshRunsRepairMigrationVersion).Scan(&before); err != nil || before != 1 {
		t.Fatalf("v26 before read count=%d err=%v", before, err)
	}
	if len(events) != 2 || events[0].Version != semanticRefreshRunsRepairMigrationVersion {
		t.Fatalf("migration events=%+v", events)
	}
	var createdText string
	if err := st.db.QueryRow(`SELECT created_at FROM semantic_refresh_runs WHERE run_id='old-run'`).Scan(&createdText); err != nil || createdText != "2026-07-28T12:00:00.000000000Z" {
		t.Fatalf("normalized created_at=%q err=%v", createdText, err)
	}
	run, err := st.LatestSemanticRefreshRun(t.Context(), "profile-a")
	if err != nil || run == nil || run.RunID != "old-run" || run.Version != 4 || run.Counters.SuccessorRuns != 9 || run.UpdatedAt.Nanosecond() != 100000000 {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=? AND name=?`, semanticRefreshRunsRepairMigrationVersion, semanticRefreshRunsRepairMigrationName).Scan(&count); err != nil || count != 1 {
		t.Fatalf("v26 count=%d err=%v", count, err)
	}
	if _, err := st.UpdateSemanticRefreshRun(t.Context(), SemanticRefreshRunUpdate{RunID: run.RunID, ExpectedVersion: run.Version, Stage: SemanticRefreshEmbedding, State: SemanticRefreshRunFailed, Checkpoint: strings.Repeat("é", 129), Now: semanticRefreshTestNow()}); err == nil {
		t.Fatal("expected repaired byte constraint")
	}
}

func TestSemanticRefreshRunsMigrationV26ArchivesV25ByteOverflows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE semantic_refresh_runs`); err != nil {
		t.Fatal(err)
	}
	if err := ensureSemanticRefreshRunSchemaV25(db); err != nil {
		t.Fatal(err)
	}

	oversizedRunID := strings.Repeat("界", 22)
	oversizedProfileID := strings.Repeat("界", 65)
	oversizedCheckpoint := strings.Repeat("界", 86)
	oversizedErrorCode := strings.Repeat("界", 22)
	oversizedErrorText := strings.Repeat("界", 171)
	oversizedReadiness := strings.Repeat("界", 22)
	insertV25 := func(values ...any) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO semantic_refresh_runs (`+semanticRefreshRunColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, values...); err != nil {
			t.Fatalf("insert genuine v25 row: %v", err)
		}
	}
	insertV25(
		oversizedRunID, "profile-run-overflow", 1, 2, 3, "embedding", "checkpoint",
		4, 5, 6, 7, 8, 9, "generation-run", "failed", "code", "text", "not_ready", 10,
		"2026-07-28T12:00:00Z", "2026-07-28T12:00:00.1Z", "2026-07-28T12:00:00.01Z",
	)
	insertV25(
		"profile-overflow-run", oversizedProfileID, 11, 12, 13, "flush", "checkpoint",
		14, 15, 16, 17, 18, 19, "generation-profile", "completed", "code", "text", "ready", 20,
		"2026-07-28T12:01:00Z", "2026-07-28T12:01:00.1Z", "2026-07-28T12:01:00.01Z",
	)
	insertV25(
		"diagnostic-overflow-run", "profile-diagnostic-overflow", 21, 22, 23, "readiness", oversizedCheckpoint,
		24, 25, 26, 27, 28, 29, "generation-diagnostic", "completed", oversizedErrorCode, oversizedErrorText, oversizedReadiness, 30,
		"2026-07-28T12:02:00Z", "2026-07-28T12:02:00.1Z", "2026-07-28T12:02:00.01Z",
	)
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version>25`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version=25`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatalf("upgrade genuine v25 overflow database: %v", err)
	}
	var activeCount, archivedCount int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM semantic_refresh_runs`).Scan(&activeCount); err != nil || activeCount != 1 {
		t.Fatalf("active count=%d err=%v", activeCount, err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM semantic_refresh_runs_v25_compatibility_archive`).Scan(&archivedCount); err != nil || archivedCount != 3 {
		t.Fatalf("archive count=%d err=%v", archivedCount, err)
	}

	var action, reason, fields, archivedProfile string
	var archivedSuccessors int64
	if err := st.db.QueryRow(`
		SELECT compatibility_action,compatibility_reason,compatibility_fields,profile_id,successor_runs
		FROM semantic_refresh_runs_v25_compatibility_archive WHERE run_id=?`, oversizedRunID).
		Scan(&action, &reason, &fields, &archivedProfile, &archivedSuccessors); err != nil {
		t.Fatal(err)
	}
	if action != "quarantined" || reason != "immutable_identifier_byte_limit" || fields != "run_id" || archivedProfile != "profile-run-overflow" || archivedSuccessors != 9 {
		t.Fatalf("run-id archive action=%q reason=%q fields=%q profile=%q successors=%d", action, reason, fields, archivedProfile, archivedSuccessors)
	}
	var archivedProfileID string
	if err := st.db.QueryRow(`
		SELECT compatibility_action,compatibility_reason,compatibility_fields,profile_id
		FROM semantic_refresh_runs_v25_compatibility_archive WHERE run_id='profile-overflow-run'`).
		Scan(&action, &reason, &fields, &archivedProfileID); err != nil {
		t.Fatal(err)
	}
	if action != "quarantined" || reason != "immutable_identifier_byte_limit" || fields != "profile_id" || archivedProfileID != oversizedProfileID {
		t.Fatalf("profile archive action=%q reason=%q fields=%q profile preserved=%v", action, reason, fields, archivedProfileID == oversizedProfileID)
	}
	var archivedCheckpoint, archivedErrorCode, archivedErrorText, archivedReadiness, archivedUpdated string
	if err := st.db.QueryRow(`
		SELECT compatibility_action,compatibility_reason,compatibility_fields,
		       checkpoint,error_code,error_text,readiness_state,updated_at
		FROM semantic_refresh_runs_v25_compatibility_archive WHERE run_id='diagnostic-overflow-run'`).
		Scan(&action, &reason, &fields, &archivedCheckpoint, &archivedErrorCode, &archivedErrorText, &archivedReadiness, &archivedUpdated); err != nil {
		t.Fatal(err)
	}
	if action != "truncated" || reason != "mutable_field_byte_limit" || fields != "checkpoint,error_code,error_text,readiness_state" {
		t.Fatalf("diagnostic archive action=%q reason=%q fields=%q", action, reason, fields)
	}
	if _, err := st.db.Exec(`UPDATE semantic_refresh_runs_v25_compatibility_archive SET compatibility_action='unknown' WHERE run_id='diagnostic-overflow-run'`); err == nil {
		t.Fatal("expected bounded compatibility action constraint")
	}
	if _, err := st.db.Exec(`UPDATE semantic_refresh_runs_v25_compatibility_archive SET compatibility_reason=? WHERE run_id='diagnostic-overflow-run'`, strings.Repeat("x", 65)); err == nil {
		t.Fatal("expected bounded compatibility reason constraint")
	}
	if archivedCheckpoint != oversizedCheckpoint || archivedErrorCode != oversizedErrorCode || archivedErrorText != oversizedErrorText || archivedReadiness != oversizedReadiness || archivedUpdated != "2026-07-28T12:02:00.1Z" {
		t.Fatal("diagnostic archive did not preserve full v25 row")
	}

	active, err := st.LatestSemanticRefreshRun(t.Context(), "profile-diagnostic-overflow")
	if err != nil || active == nil {
		t.Fatalf("active diagnostic row=%+v err=%v", active, err)
	}
	if active.Checkpoint != strings.Repeat("界", 85) || active.ErrorCode != strings.Repeat("界", 21) || active.ErrorText != strings.Repeat("界", 170) || active.ReadinessState != strings.Repeat("界", 21) {
		t.Fatalf("active truncation checkpoint_bytes=%d code_bytes=%d text_bytes=%d readiness_bytes=%d", len(active.Checkpoint), len(active.ErrorCode), len(active.ErrorText), len(active.ReadinessState))
	}
	if active.Counters != (SemanticRefreshCounters{ProjectedParents: 24, EmbeddedChunks: 25, FlushedVectors: 26, CompactedVectors: 27, VerifiedVectors: 28, SuccessorRuns: 29}) ||
		active.CurrentGenerationID != "generation-diagnostic" || active.Version != 30 ||
		active.CreatedAt.Nanosecond() != 0 || active.UpdatedAt.Nanosecond() != 100000000 || active.LastProgressAt.Nanosecond() != 10000000 {
		t.Fatalf("active row lost counters/version/time: %+v", active)
	}
	if !utf8.ValidString(active.Checkpoint) || !utf8.ValidString(active.ErrorCode) || !utf8.ValidString(active.ErrorText) || !utf8.ValidString(active.ReadinessState) {
		t.Fatal("active truncation produced invalid UTF-8")
	}
	for _, runID := range []string{oversizedRunID, "profile-overflow-run"} {
		var count int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM semantic_refresh_runs WHERE run_id=?`, runID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("quarantined run %q active count=%d err=%v", runID, count, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version=?`, semanticRefreshRunsRepairMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version=25`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatalf("idempotent migration replay: %v", err)
	}
	defer func() { _ = st.Close() }()
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM semantic_refresh_runs_v25_compatibility_archive`).Scan(&archivedCount); err != nil || archivedCount != 3 {
		t.Fatalf("archive after replay count=%d err=%v", archivedCount, err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM semantic_refresh_runs`).Scan(&activeCount); err != nil || activeCount != 1 {
		t.Fatalf("active after replay count=%d err=%v", activeCount, err)
	}
	var migrationCount int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=? AND name=?`, semanticRefreshRunsRepairMigrationVersion, semanticRefreshRunsRepairMigrationName).Scan(&migrationCount); err != nil || migrationCount != 1 {
		t.Fatalf("v26 migration after replay count=%d err=%v", migrationCount, err)
	}
}
