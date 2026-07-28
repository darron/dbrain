package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type SemanticRefreshRunState string

const (
	SemanticRefreshRunRunning    SemanticRefreshRunState = "running"
	SemanticRefreshRunFailed     SemanticRefreshRunState = "failed"
	SemanticRefreshRunCancelled  SemanticRefreshRunState = "cancelled"
	SemanticRefreshRunCompleted  SemanticRefreshRunState = "completed"
	SemanticRefreshRunSuperseded SemanticRefreshRunState = "superseded"
)

type SemanticRefreshStage string

const (
	SemanticRefreshProjection SemanticRefreshStage = "projection"
	SemanticRefreshEmbedding  SemanticRefreshStage = "embedding"
	SemanticRefreshFlush      SemanticRefreshStage = "flush"
	SemanticRefreshCompaction SemanticRefreshStage = "compaction"
	SemanticRefreshVerify     SemanticRefreshStage = "verify"
	SemanticRefreshReadiness  SemanticRefreshStage = "readiness"
)

type SemanticRefreshCounters struct {
	ProjectedParents int64 `json:"projected_parents"`
	EmbeddedChunks   int64 `json:"embedded_chunks"`
	FlushedVectors   int64 `json:"flushed_vectors"`
	CompactedVectors int64 `json:"compacted_vectors"`
	VerifiedVectors  int64 `json:"verified_vectors"`
	SuccessorRuns    int64 `json:"successor_runs"`
}
type SemanticRefreshRun struct {
	RunID, ProfileID, Checkpoint, CurrentGenerationID  string
	ErrorCode, ErrorText, ReadinessState               string
	PurgeEpoch, ProjectionWatermark, EmbeddingRevision int64
	Version                                            int64
	Stage                                              SemanticRefreshStage
	State                                              SemanticRefreshRunState
	Counters                                           SemanticRefreshCounters
	CreatedAt, UpdatedAt, LastProgressAt               time.Time
}
type StartSemanticRefreshRunInput struct {
	RunID, ProfileID                string
	PurgeEpoch, ProjectionWatermark int64
	Now                             time.Time
}
type SemanticRefreshRunUpdate struct {
	RunID, Checkpoint, CurrentGenerationID string
	ErrorCode, ErrorText, ReadinessState   string
	ExpectedVersion, EmbeddingRevision     int64
	Stage                                  SemanticRefreshStage
	State                                  SemanticRefreshRunState
	Counters                               SemanticRefreshCounters
	Now                                    time.Time
}

var ErrSemanticRefreshRunStale = errors.New("semantic refresh run changed")

const semanticRefreshRunColumns = `run_id,profile_id,purge_epoch,projection_watermark,embedding_revision,stage,checkpoint,projected_parents,embedded_chunks,flushed_vectors,compacted_vectors,verified_vectors,successor_runs,current_generation_id,state,error_code,error_text,readiness_state,version,created_at,updated_at,last_progress_at`

const semanticRefreshTimestampLayout = "2006-01-02T15:04:05.000000000Z"

const semanticRefreshRunsV25CreateTableSQL = `CREATE TABLE IF NOT EXISTS semantic_refresh_runs (run_id TEXT PRIMARY KEY,profile_id TEXT NOT NULL,purge_epoch INTEGER NOT NULL,projection_watermark INTEGER NOT NULL,embedding_revision INTEGER NOT NULL DEFAULT 0,stage TEXT NOT NULL,checkpoint TEXT NOT NULL DEFAULT '',projected_parents INTEGER NOT NULL DEFAULT 0,embedded_chunks INTEGER NOT NULL DEFAULT 0,flushed_vectors INTEGER NOT NULL DEFAULT 0,compacted_vectors INTEGER NOT NULL DEFAULT 0,verified_vectors INTEGER NOT NULL DEFAULT 0,successor_runs INTEGER NOT NULL DEFAULT 0,current_generation_id TEXT NOT NULL DEFAULT '',state TEXT NOT NULL,error_code TEXT NOT NULL DEFAULT '',error_text TEXT NOT NULL DEFAULT '',readiness_state TEXT NOT NULL DEFAULT '',version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,last_progress_at TEXT NOT NULL,CHECK(length(run_id) BETWEEN 1 AND 64),CHECK(length(profile_id) BETWEEN 1 AND 192),CHECK(purge_epoch >= 0),CHECK(projection_watermark >= 0),CHECK(embedding_revision >= 0),CHECK(stage IN ('projection','embedding','flush','compaction','verify','readiness')),CHECK(length(checkpoint) <= 256),CHECK(projected_parents >= 0 AND embedded_chunks >= 0),CHECK(flushed_vectors >= 0 AND compacted_vectors >= 0 AND verified_vectors >= 0),CHECK(successor_runs >= 0),CHECK(state IN ('running','failed','cancelled','completed','superseded')),CHECK(length(error_code) <= 64),CHECK(length(error_text) <= 512),CHECK(length(readiness_state) <= 64),CHECK(version > 0))`

const semanticRefreshRunsV26CreateTableSQL = `CREATE TABLE IF NOT EXISTS semantic_refresh_runs (run_id TEXT PRIMARY KEY,profile_id TEXT NOT NULL,purge_epoch INTEGER NOT NULL,projection_watermark INTEGER NOT NULL,embedding_revision INTEGER NOT NULL DEFAULT 0,stage TEXT NOT NULL,checkpoint TEXT NOT NULL DEFAULT '',projected_parents INTEGER NOT NULL DEFAULT 0,embedded_chunks INTEGER NOT NULL DEFAULT 0,flushed_vectors INTEGER NOT NULL DEFAULT 0,compacted_vectors INTEGER NOT NULL DEFAULT 0,verified_vectors INTEGER NOT NULL DEFAULT 0,successor_runs INTEGER NOT NULL DEFAULT 0,current_generation_id TEXT NOT NULL DEFAULT '',state TEXT NOT NULL,error_code TEXT NOT NULL DEFAULT '',error_text TEXT NOT NULL DEFAULT '',readiness_state TEXT NOT NULL DEFAULT '',version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,last_progress_at TEXT NOT NULL,CHECK(length(CAST(run_id AS BLOB)) BETWEEN 1 AND 64),CHECK(length(CAST(profile_id AS BLOB)) BETWEEN 1 AND 192),CHECK(purge_epoch >= 0),CHECK(projection_watermark >= 0),CHECK(embedding_revision >= 0),CHECK(stage IN ('projection','embedding','flush','compaction','verify','readiness')),CHECK(length(CAST(checkpoint AS BLOB)) <= 256),CHECK(projected_parents >= 0 AND embedded_chunks >= 0),CHECK(flushed_vectors >= 0 AND compacted_vectors >= 0 AND verified_vectors >= 0),CHECK(successor_runs >= 0),CHECK(state IN ('running','failed','cancelled','completed','superseded')),CHECK(length(CAST(error_code AS BLOB)) <= 64),CHECK(length(CAST(error_text AS BLOB)) <= 512),CHECK(length(CAST(readiness_state AS BLOB)) <= 64),CHECK(version > 0))`

var semanticRefreshRunIndexStatements = []string{
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_semantic_refresh_runs_one_resumable ON semantic_refresh_runs(profile_id) WHERE state IN ('running','failed','cancelled')`,
	`CREATE INDEX IF NOT EXISTS idx_semantic_refresh_runs_latest ON semantic_refresh_runs(updated_at DESC,run_id DESC)`,
}

func ensureSemanticRefreshRunSchema(db *sql.DB) error {
	return ensureSemanticRefreshRunSchemaWithDefinition(db, semanticRefreshRunsV26CreateTableSQL)
}

func ensureSemanticRefreshRunSchemaV25(db *sql.DB) error {
	return ensureSemanticRefreshRunSchemaWithDefinition(db, semanticRefreshRunsV25CreateTableSQL)
}

func ensureSemanticRefreshRunSchemaWithDefinition(db *sql.DB, tableStatement string) error {
	for _, q := range append([]string{tableStatement}, semanticRefreshRunIndexStatements...) {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("ensure semantic refresh run schema: %w", err)
		}
	}
	return nil
}

type semanticRefreshRunMigrationRow struct {
	SemanticRefreshRun
	createdAt, updatedAt, lastProgressAt string
}

func (s *Store) repairSemanticRefreshRunSchemaV26() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin semantic refresh run v26 repair: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query(`SELECT ` + semanticRefreshRunColumns + ` FROM semantic_refresh_runs ORDER BY run_id`)
	if err != nil {
		return fmt.Errorf("read semantic refresh runs for v26 repair: %w", err)
	}
	var stored []semanticRefreshRunMigrationRow
	for rows.Next() {
		var row semanticRefreshRunMigrationRow
		err = rows.Scan(&row.RunID, &row.ProfileID, &row.PurgeEpoch, &row.ProjectionWatermark, &row.EmbeddingRevision, &row.Stage, &row.Checkpoint, &row.Counters.ProjectedParents, &row.Counters.EmbeddedChunks, &row.Counters.FlushedVectors, &row.Counters.CompactedVectors, &row.Counters.VerifiedVectors, &row.Counters.SuccessorRuns, &row.CurrentGenerationID, &row.State, &row.ErrorCode, &row.ErrorText, &row.ReadinessState, &row.Version, &row.createdAt, &row.updatedAt, &row.lastProgressAt)
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan semantic refresh run for v26 repair: %w", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close semantic refresh runs for v26 repair: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate semantic refresh runs for v26 repair: %w", err)
	}
	for i := range stored {
		row := &stored[i]
		if row.createdAt, err = normalizeSemanticRefreshTimestamp(row.createdAt); err != nil {
			return fmt.Errorf("normalize semantic refresh run %s created_at: %w", row.RunID, err)
		}
		if row.updatedAt, err = normalizeSemanticRefreshTimestamp(row.updatedAt); err != nil {
			return fmt.Errorf("normalize semantic refresh run %s updated_at: %w", row.RunID, err)
		}
		if row.lastProgressAt, err = normalizeSemanticRefreshTimestamp(row.lastProgressAt); err != nil {
			return fmt.Errorf("normalize semantic refresh run %s last_progress_at: %w", row.RunID, err)
		}
	}
	for _, index := range []string{"idx_semantic_refresh_runs_one_resumable", "idx_semantic_refresh_runs_latest"} {
		if _, err := tx.Exec(`DROP INDEX IF EXISTS ` + index); err != nil {
			return fmt.Errorf("drop semantic refresh run v25 index: %w", err)
		}
	}
	if _, err := tx.Exec(`ALTER TABLE semantic_refresh_runs RENAME TO semantic_refresh_runs_v25`); err != nil {
		return fmt.Errorf("rename semantic refresh run v25 table: %w", err)
	}
	if _, err := tx.Exec(semanticRefreshRunsV26CreateTableSQL); err != nil {
		return fmt.Errorf("create semantic refresh run v26 table: %w", err)
	}
	for _, row := range stored {
		if _, err := tx.Exec(`INSERT INTO semantic_refresh_runs (`+semanticRefreshRunColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, row.RunID, row.ProfileID, row.PurgeEpoch, row.ProjectionWatermark, row.EmbeddingRevision, row.Stage, row.Checkpoint, row.Counters.ProjectedParents, row.Counters.EmbeddedChunks, row.Counters.FlushedVectors, row.Counters.CompactedVectors, row.Counters.VerifiedVectors, row.Counters.SuccessorRuns, row.CurrentGenerationID, row.State, row.ErrorCode, row.ErrorText, row.ReadinessState, row.Version, row.createdAt, row.updatedAt, row.lastProgressAt); err != nil {
			return fmt.Errorf("copy semantic refresh run %s to v26: %w", row.RunID, err)
		}
	}
	if _, err := tx.Exec(`DROP TABLE semantic_refresh_runs_v25`); err != nil {
		return fmt.Errorf("drop semantic refresh run v25 table: %w", err)
	}
	for _, statement := range semanticRefreshRunIndexStatements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("create semantic refresh run v26 index: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit semantic refresh run v26 repair: %w", err)
	}
	return nil
}

func normalizeSemanticRefreshTimestamp(value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", err
	}
	return parsed.UTC().Format(semanticRefreshTimestampLayout), nil
}
func semanticRefreshNow(now time.Time) (time.Time, string) {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	return now, now.Format(semanticRefreshTimestampLayout)
}
func (s *Store) StartOrResumeSemanticRefreshRun(ctx context.Context, in StartSemanticRefreshRunInput) (SemanticRefreshRun, bool, error) {
	in.RunID, in.ProfileID = strings.TrimSpace(in.RunID), strings.TrimSpace(in.ProfileID)
	if in.RunID == "" || in.ProfileID == "" || in.PurgeEpoch < 0 || in.ProjectionWatermark < 0 {
		return SemanticRefreshRun{}, false, fmt.Errorf("invalid semantic refresh run input")
	}
	_, now := semanticRefreshNow(in.Now)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SemanticRefreshRun{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE semantic_refresh_runs SET state='superseded',version=version+1,updated_at=?,last_progress_at=? WHERE profile_id<>? AND state IN ('running','failed','cancelled')`, now, now, in.ProfileID); err != nil {
		return SemanticRefreshRun{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE semantic_refresh_runs SET state='superseded',version=version+1,updated_at=?,last_progress_at=? WHERE profile_id=? AND purge_epoch<>? AND state IN ('running','failed','cancelled')`, now, now, in.ProfileID, in.PurgeEpoch); err != nil {
		return SemanticRefreshRun{}, false, err
	}
	var run SemanticRefreshRun
	err = scanSemanticRefreshRun(tx.QueryRowContext(ctx, `SELECT `+semanticRefreshRunColumns+` FROM semantic_refresh_runs WHERE profile_id=? AND purge_epoch=? AND state IN ('running','failed','cancelled')`, in.ProfileID, in.PurgeEpoch), &run)
	if err == nil {
		if _, err = tx.ExecContext(ctx, `UPDATE semantic_refresh_runs SET state='running',error_code='',error_text='',version=version+1,updated_at=?,last_progress_at=? WHERE run_id=?`, now, now, run.RunID); err != nil {
			return SemanticRefreshRun{}, false, err
		}
		if err = scanSemanticRefreshRun(tx.QueryRowContext(ctx, `SELECT `+semanticRefreshRunColumns+` FROM semantic_refresh_runs WHERE run_id=?`, run.RunID), &run); err != nil {
			return SemanticRefreshRun{}, false, err
		}
		if err = tx.Commit(); err != nil {
			return SemanticRefreshRun{}, false, err
		}
		return run, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SemanticRefreshRun{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO semantic_refresh_runs (`+semanticRefreshRunColumns+`) VALUES (?,?,?,?,0,'projection','',0,0,0,0,0,0,'','running','','','',1,?,?,?)`, in.RunID, in.ProfileID, in.PurgeEpoch, in.ProjectionWatermark, now, now, now)
	if err != nil {
		return SemanticRefreshRun{}, false, err
	}
	if err = scanSemanticRefreshRun(tx.QueryRowContext(ctx, `SELECT `+semanticRefreshRunColumns+` FROM semantic_refresh_runs WHERE run_id=?`, in.RunID), &run); err != nil {
		return SemanticRefreshRun{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return SemanticRefreshRun{}, false, err
	}
	return run, false, nil
}
func (s *Store) UpdateSemanticRefreshRun(ctx context.Context, in SemanticRefreshRunUpdate) (SemanticRefreshRun, error) {
	_, now := semanticRefreshNow(in.Now)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SemanticRefreshRun{}, fmt.Errorf("begin semantic refresh run update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE semantic_refresh_runs SET embedding_revision=?,stage=?,checkpoint=?,projected_parents=?,embedded_chunks=?,flushed_vectors=?,compacted_vectors=?,verified_vectors=?,successor_runs=?,current_generation_id=?,state=?,error_code=?,error_text=?,readiness_state=?,version=version+1,updated_at=?,last_progress_at=? WHERE run_id=? AND version=? AND state IN ('running','failed','cancelled')`, in.EmbeddingRevision, in.Stage, in.Checkpoint, in.Counters.ProjectedParents, in.Counters.EmbeddedChunks, in.Counters.FlushedVectors, in.Counters.CompactedVectors, in.Counters.VerifiedVectors, in.Counters.SuccessorRuns, in.CurrentGenerationID, in.State, in.ErrorCode, in.ErrorText, in.ReadinessState, now, now, in.RunID, in.ExpectedVersion)
	if err != nil {
		return SemanticRefreshRun{}, fmt.Errorf("update semantic refresh run: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return SemanticRefreshRun{}, fmt.Errorf("count semantic refresh run update: %w", err)
	}
	if n != 1 {
		return SemanticRefreshRun{}, ErrSemanticRefreshRunStale
	}
	var run SemanticRefreshRun
	if err := scanSemanticRefreshRun(tx.QueryRowContext(ctx, `SELECT `+semanticRefreshRunColumns+` FROM semantic_refresh_runs WHERE run_id=?`, in.RunID), &run); err != nil {
		return SemanticRefreshRun{}, fmt.Errorf("read updated semantic refresh run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SemanticRefreshRun{}, fmt.Errorf("commit semantic refresh run update: %w", err)
	}
	return run, nil
}
func (s *Store) TouchSemanticRefreshRunProgress(ctx context.Context, runID string, at time.Time) error {
	_, now := semanticRefreshNow(at)
	result, err := s.db.ExecContext(ctx, `UPDATE semantic_refresh_runs SET updated_at=?,last_progress_at=? WHERE run_id=? AND state='running'`, now, now, runID)
	if err != nil {
		return fmt.Errorf("touch semantic refresh run progress: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count semantic refresh run heartbeat: %w", err)
	}
	if n != 1 {
		return ErrSemanticRefreshRunStale
	}
	return nil
}
func (s *Store) LatestSemanticRefreshRun(ctx context.Context, profileID string) (*SemanticRefreshRun, error) {
	q, args := `SELECT `+semanticRefreshRunColumns+` FROM semantic_refresh_runs`, []any{}
	if profileID = strings.TrimSpace(profileID); profileID != "" {
		q += ` WHERE profile_id=?`
		args = append(args, profileID)
	}
	q += ` ORDER BY updated_at DESC,run_id DESC LIMIT 1`
	var run SemanticRefreshRun
	err := scanSemanticRefreshRun(s.db.QueryRowContext(ctx, q, args...), &run)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

type semanticRefreshRunScanner interface{ Scan(...any) error }

func scanSemanticRefreshRun(row semanticRefreshRunScanner, run *SemanticRefreshRun) error {
	var created, updated, progressed string
	err := row.Scan(&run.RunID, &run.ProfileID, &run.PurgeEpoch, &run.ProjectionWatermark, &run.EmbeddingRevision, &run.Stage, &run.Checkpoint, &run.Counters.ProjectedParents, &run.Counters.EmbeddedChunks, &run.Counters.FlushedVectors, &run.Counters.CompactedVectors, &run.Counters.VerifiedVectors, &run.Counters.SuccessorRuns, &run.CurrentGenerationID, &run.State, &run.ErrorCode, &run.ErrorText, &run.ReadinessState, &run.Version, &created, &updated, &progressed)
	if err != nil {
		return err
	}
	run.CreatedAt, err = time.Parse(semanticRefreshTimestampLayout, created)
	if err != nil {
		return fmt.Errorf("parse semantic refresh run created_at: %w", err)
	}
	run.UpdatedAt, err = time.Parse(semanticRefreshTimestampLayout, updated)
	if err != nil {
		return fmt.Errorf("parse semantic refresh run updated_at: %w", err)
	}
	run.LastProgressAt, err = time.Parse(semanticRefreshTimestampLayout, progressed)
	if err != nil {
		return fmt.Errorf("parse semantic refresh run last_progress_at: %w", err)
	}
	return nil
}
