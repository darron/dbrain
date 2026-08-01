package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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
	InitialCounters                 SemanticRefreshCounters
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

const semanticRefreshRunsV25CompatibilityArchiveCreateTableSQL = `CREATE TABLE IF NOT EXISTS semantic_refresh_runs_v25_compatibility_archive (run_id TEXT PRIMARY KEY,profile_id TEXT NOT NULL,purge_epoch INTEGER NOT NULL,projection_watermark INTEGER NOT NULL,embedding_revision INTEGER NOT NULL,stage TEXT NOT NULL,checkpoint TEXT NOT NULL,projected_parents INTEGER NOT NULL,embedded_chunks INTEGER NOT NULL,flushed_vectors INTEGER NOT NULL,compacted_vectors INTEGER NOT NULL,verified_vectors INTEGER NOT NULL,successor_runs INTEGER NOT NULL,current_generation_id TEXT NOT NULL,state TEXT NOT NULL,error_code TEXT NOT NULL,error_text TEXT NOT NULL,readiness_state TEXT NOT NULL,version INTEGER NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,last_progress_at TEXT NOT NULL,compatibility_action TEXT NOT NULL CHECK(compatibility_action IN ('quarantined','truncated')),compatibility_reason TEXT NOT NULL CHECK(compatibility_reason IN ('immutable_identifier_byte_limit','mutable_field_byte_limit')) CHECK(length(CAST(compatibility_reason AS BLOB)) BETWEEN 1 AND 64),compatibility_fields TEXT NOT NULL CHECK(length(CAST(compatibility_fields AS BLOB)) BETWEEN 1 AND 128))`

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

type semanticRefreshRunCompatibility struct {
	action, reason, fields string
	quarantine             bool
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
	if _, err := tx.Exec(semanticRefreshRunsV25CompatibilityArchiveCreateTableSQL); err != nil {
		return fmt.Errorf("create semantic refresh run v25 compatibility archive: %w", err)
	}
	for _, row := range stored {
		compatibility := semanticRefreshRunV25Compatibility(row)
		if compatibility.action != "" {
			if _, err := tx.Exec(`INSERT INTO semantic_refresh_runs_v25_compatibility_archive (`+semanticRefreshRunColumns+`,compatibility_action,compatibility_reason,compatibility_fields) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id) DO NOTHING`, row.RunID, row.ProfileID, row.PurgeEpoch, row.ProjectionWatermark, row.EmbeddingRevision, row.Stage, row.Checkpoint, row.Counters.ProjectedParents, row.Counters.EmbeddedChunks, row.Counters.FlushedVectors, row.Counters.CompactedVectors, row.Counters.VerifiedVectors, row.Counters.SuccessorRuns, row.CurrentGenerationID, row.State, row.ErrorCode, row.ErrorText, row.ReadinessState, row.Version, row.createdAt, row.updatedAt, row.lastProgressAt, compatibility.action, compatibility.reason, compatibility.fields); err != nil {
				return fmt.Errorf("archive semantic refresh run %s for v26 compatibility: %w", row.RunID, err)
			}
		}
		if compatibility.quarantine {
			continue
		}
		if row.createdAt, err = normalizeSemanticRefreshTimestamp(row.createdAt); err != nil {
			return fmt.Errorf("normalize semantic refresh run %s created_at: %w", row.RunID, err)
		}
		if row.updatedAt, err = normalizeSemanticRefreshTimestamp(row.updatedAt); err != nil {
			return fmt.Errorf("normalize semantic refresh run %s updated_at: %w", row.RunID, err)
		}
		if row.lastProgressAt, err = normalizeSemanticRefreshTimestamp(row.lastProgressAt); err != nil {
			return fmt.Errorf("normalize semantic refresh run %s last_progress_at: %w", row.RunID, err)
		}
		row.Checkpoint = truncateSemanticRefreshRunUTF8(row.Checkpoint, 256)
		row.ErrorCode = truncateSemanticRefreshRunUTF8(row.ErrorCode, 64)
		row.ErrorText = truncateSemanticRefreshRunUTF8(row.ErrorText, 512)
		row.ReadinessState = truncateSemanticRefreshRunUTF8(row.ReadinessState, 64)
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

func semanticRefreshRunV25Compatibility(row semanticRefreshRunMigrationRow) semanticRefreshRunCompatibility {
	// V25 constrained Unicode character counts, while V26 constrains UTF-8 bytes.
	// Immutable overflows cannot be rewritten collision-free, so preserve and
	// quarantine those rows. Mutable overflows retain an active byte-safe prefix,
	// with this archive preserving the exact V25 values for diagnosis or recovery.
	var immutable, mutable []string
	if len(row.RunID) > 64 {
		immutable = append(immutable, "run_id")
	}
	if len(row.ProfileID) > 192 {
		immutable = append(immutable, "profile_id")
	}
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{name: "checkpoint", value: row.Checkpoint, limit: 256},
		{name: "error_code", value: row.ErrorCode, limit: 64},
		{name: "error_text", value: row.ErrorText, limit: 512},
		{name: "readiness_state", value: row.ReadinessState, limit: 64},
	} {
		if len(field.value) > field.limit {
			mutable = append(mutable, field.name)
		}
	}
	fields := append(append([]string(nil), immutable...), mutable...)
	if len(fields) == 0 {
		return semanticRefreshRunCompatibility{}
	}
	if len(immutable) > 0 {
		return semanticRefreshRunCompatibility{
			action:     "quarantined",
			reason:     "immutable_identifier_byte_limit",
			fields:     strings.Join(fields, ","),
			quarantine: true,
		}
	}
	return semanticRefreshRunCompatibility{
		action: "truncated",
		reason: "mutable_field_byte_limit",
		fields: strings.Join(fields, ","),
	}
}

func truncateSemanticRefreshRunUTF8(value string, limit int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
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
	if !validSemanticRefreshCounters(in.InitialCounters) {
		return SemanticRefreshRun{}, false, fmt.Errorf("invalid semantic refresh run input")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO semantic_refresh_runs (`+semanticRefreshRunColumns+`) VALUES (?,?,?,?,0,'projection','',?,?,?,?,?,?,'','running','','','',1,?,?,?)`,
		in.RunID,
		in.ProfileID,
		in.PurgeEpoch,
		in.ProjectionWatermark,
		in.InitialCounters.ProjectedParents,
		in.InitialCounters.EmbeddedChunks,
		in.InitialCounters.FlushedVectors,
		in.InitialCounters.CompactedVectors,
		in.InitialCounters.VerifiedVectors,
		in.InitialCounters.SuccessorRuns,
		now,
		now,
		now,
	)
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

func validSemanticRefreshCounters(counters SemanticRefreshCounters) bool {
	return counters.ProjectedParents >= 0 &&
		counters.EmbeddedChunks >= 0 &&
		counters.FlushedVectors >= 0 &&
		counters.CompactedVectors >= 0 &&
		counters.VerifiedVectors >= 0 &&
		counters.SuccessorRuns >= 0
}

func (s *Store) UpdateSemanticRefreshRun(ctx context.Context, in SemanticRefreshRunUpdate) (SemanticRefreshRun, error) {
	_, now := semanticRefreshNow(in.Now)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SemanticRefreshRun{}, fmt.Errorf("begin semantic refresh run update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	hooks, _ := ctx.Value(semanticRefreshRunUpdateTestHooksKey{}).(semanticRefreshRunUpdateTestHooks)
	var run SemanticRefreshRun
	err = scanSemanticRefreshRun(tx.QueryRowContext(ctx, `UPDATE semantic_refresh_runs SET embedding_revision=?,stage=?,checkpoint=?,projected_parents=?,embedded_chunks=?,flushed_vectors=?,compacted_vectors=?,verified_vectors=?,successor_runs=?,current_generation_id=?,state=?,error_code=?,error_text=?,readiness_state=?,version=version+1,updated_at=?,last_progress_at=? WHERE run_id=? AND version=? AND state IN ('running','failed','cancelled') RETURNING `+semanticRefreshRunColumns, in.EmbeddingRevision, in.Stage, in.Checkpoint, in.Counters.ProjectedParents, in.Counters.EmbeddedChunks, in.Counters.FlushedVectors, in.Counters.CompactedVectors, in.Counters.VerifiedVectors, in.Counters.SuccessorRuns, in.CurrentGenerationID, in.State, in.ErrorCode, in.ErrorText, in.ReadinessState, now, now, in.RunID, in.ExpectedVersion), &run)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SemanticRefreshRun{}, ErrSemanticRefreshRunStale
		}
		return SemanticRefreshRun{}, fmt.Errorf("update semantic refresh run: %w", err)
	}
	if hooks.AfterWrite != nil {
		hooks.AfterWrite()
	}
	if err := tx.Commit(); err != nil {
		return SemanticRefreshRun{}, fmt.Errorf("commit semantic refresh run update: %w", err)
	}
	if hooks.AfterCommit != nil {
		hooks.AfterCommit()
	}
	return run, nil
}

type semanticRefreshRunUpdateTestHooksKey struct{}

type semanticRefreshRunUpdateTestHooks struct {
	AfterWrite, AfterCommit func()
}

func (s *Store) AcquireSemanticRefreshStage(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, fmt.Errorf("semantic refresh stage context is required")
	}
	s.semanticStageOnce.Do(func() {
		s.semanticStageGate = make(chan struct{}, 1)
		s.semanticStageGate <- struct{}{}
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.semanticStageGate:
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			s.semanticStageGate <- struct{}{}
		})
	}, nil
}

func (s *Store) TouchSemanticRefreshRunProgress(ctx context.Context, runID string, at time.Time) error {
	release, err := s.AcquireSemanticRefreshStage(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, now := semanticRefreshNow(at)
	db, err := s.semanticProgressDB()
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, `
		UPDATE semantic_refresh_runs
		SET
			updated_at=CASE WHEN updated_at<? THEN ? ELSE updated_at END,
			last_progress_at=CASE WHEN last_progress_at IS NULL OR last_progress_at<? THEN ? ELSE last_progress_at END
		WHERE run_id=? AND state='running'`,
		now, now, now, now, runID,
	)
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
	available, err := s.tableExistsContext(ctx, "semantic_refresh_runs")
	if err != nil {
		return nil, fmt.Errorf("check semantic refresh run ledger: %w", err)
	}
	if !available {
		return nil, fmt.Errorf("semantic refresh run ledger: %w", ErrRetrievalUnavailable)
	}
	q, args := `SELECT `+semanticRefreshRunColumns+` FROM semantic_refresh_runs`, []any{}
	if profileID = strings.TrimSpace(profileID); profileID != "" {
		q += ` WHERE profile_id=?`
		args = append(args, profileID)
	}
	q += ` ORDER BY updated_at DESC,run_id DESC LIMIT 1`
	var run SemanticRefreshRun
	err = scanSemanticRefreshRun(s.db.QueryRowContext(ctx, q, args...), &run)
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
