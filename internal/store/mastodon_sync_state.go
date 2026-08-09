package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrMastodonSyncStateChanged = errors.New("mastodon sync state changed during write")

type MastodonSyncState struct {
	AccountKey          string
	CanonicalOrigin     string
	VerifiedAccountID   string
	Handle              string
	BackfillNextURL     string
	BackfillComplete    bool
	BackfillIncremental bool
	BackfillPageURL     string
	BackfillPageDigest  string
	BackfillPageOffset  int
	LastPageURL         string
	LastPageDigest      string
	CapabilitiesJSON    string
	LastSuccessAt       time.Time
	LastErrorAt         time.Time
	LastError           string
}

func (s *Store) GetMastodonSyncState(ctx context.Context, accountKey, origin string) (*MastodonSyncState, error) {
	row := s.db.QueryRowContext(ctx, `SELECT account_key, canonical_origin, verified_account_id, handle, backfill_next_url, backfill_complete, backfill_incremental, backfill_page_url, backfill_page_digest, backfill_page_offset, last_page_url, last_page_digest, capabilities_json, last_success_at, last_error_at, last_error FROM mastodon_sync_state WHERE account_key=? AND canonical_origin=?`, strings.TrimSpace(accountKey), strings.TrimSpace(origin))
	var state MastodonSyncState
	var complete, incremental int
	var successAt, errorAt string
	if err := row.Scan(&state.AccountKey, &state.CanonicalOrigin, &state.VerifiedAccountID, &state.Handle, &state.BackfillNextURL, &complete, &incremental, &state.BackfillPageURL, &state.BackfillPageDigest, &state.BackfillPageOffset, &state.LastPageURL, &state.LastPageDigest, &state.CapabilitiesJSON, &successAt, &errorAt, &state.LastError); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("load Mastodon sync state: %w", err)
	}
	state.BackfillComplete = complete != 0
	state.BackfillIncremental = incremental != 0
	state.LastSuccessAt = parseStoredTime(successAt)
	state.LastErrorAt = parseStoredTime(errorAt)
	return &state, nil
}

func (s *Store) GetMastodonSyncStateByVerifiedAccount(ctx context.Context, origin, verifiedAccountID string) (*MastodonSyncState, error) {
	row := s.db.QueryRowContext(ctx, `SELECT account_key, canonical_origin, verified_account_id, handle, backfill_next_url, backfill_complete, backfill_incremental, backfill_page_url, backfill_page_digest, backfill_page_offset, last_page_url, last_page_digest, capabilities_json, last_success_at, last_error_at, last_error FROM mastodon_sync_state WHERE canonical_origin=? AND verified_account_id=?`, strings.TrimSpace(origin), strings.TrimSpace(verifiedAccountID))
	var state MastodonSyncState
	var complete, incremental int
	var successAt, errorAt string
	if err := row.Scan(&state.AccountKey, &state.CanonicalOrigin, &state.VerifiedAccountID, &state.Handle, &state.BackfillNextURL, &complete, &incremental, &state.BackfillPageURL, &state.BackfillPageDigest, &state.BackfillPageOffset, &state.LastPageURL, &state.LastPageDigest, &state.CapabilitiesJSON, &successAt, &errorAt, &state.LastError); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("load Mastodon verified-account state: %w", err)
	}
	state.BackfillComplete = complete != 0
	state.BackfillIncremental = incremental != 0
	state.LastSuccessAt = parseStoredTime(successAt)
	state.LastErrorAt = parseStoredTime(errorAt)
	return &state, nil
}

func (s *Store) UpsertMastodonSyncState(ctx context.Context, state MastodonSyncState) error {
	state, err := normalizeMastodonSyncState(state)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, mastodonSyncStateInsertSQL(`ON CONFLICT(account_key, canonical_origin) DO UPDATE SET verified_account_id=excluded.verified_account_id, handle=excluded.handle, backfill_next_url=excluded.backfill_next_url, backfill_complete=excluded.backfill_complete, backfill_incremental=excluded.backfill_incremental, backfill_page_url=excluded.backfill_page_url, backfill_page_digest=excluded.backfill_page_digest, backfill_page_offset=excluded.backfill_page_offset, last_page_url=excluded.last_page_url, last_page_digest=excluded.last_page_digest, capabilities_json=excluded.capabilities_json, last_success_at=excluded.last_success_at, last_error_at=excluded.last_error_at, last_error=excluded.last_error`), mastodonSyncStateWriteArgs(state)...)
	if err != nil {
		return fmt.Errorf("save Mastodon sync state: %w", err)
	}
	return nil
}

// UpsertMastodonSyncStateIfCurrent writes a checkpoint only if the complete
// state observed by the caller is still current. Identity alone is not enough:
// a competing run may have changed pagination while retaining the same account.
func (s *Store) UpsertMastodonSyncStateIfCurrent(ctx context.Context, state MastodonSyncState, expected *MastodonSyncState) error {
	state, err := normalizeMastodonSyncState(state)
	if err != nil {
		return err
	}
	if expected == nil {
		result, err := s.db.ExecContext(ctx, mastodonSyncStateInsertSQL(`ON CONFLICT(account_key, canonical_origin) DO NOTHING`), mastodonSyncStateWriteArgs(state)...)
		if err != nil {
			return fmt.Errorf("save initial Mastodon sync state: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect initial Mastodon sync state write: %w", err)
		}
		if rows == 0 {
			return ErrMastodonSyncStateChanged
		}
		return nil
	}
	expectedState, err := normalizeMastodonSyncState(*expected)
	if err != nil {
		return err
	}
	if expectedState.AccountKey != state.AccountKey || expectedState.CanonicalOrigin != state.CanonicalOrigin {
		return fmt.Errorf("mastodon sync state checkpoint identity does not match expected state")
	}
	args := append([]any{}, mastodonSyncStateWriteArgsWithoutKeys(state)...)
	args = append(args, state.AccountKey, state.CanonicalOrigin)
	args = append(args, mastodonSyncStateMatchArgs(expectedState)...)
	result, err := s.db.ExecContext(ctx, `UPDATE mastodon_sync_state SET verified_account_id=?, handle=?, backfill_next_url=?, backfill_complete=?, backfill_incremental=?, backfill_page_url=?, backfill_page_digest=?, backfill_page_offset=?, last_page_url=?, last_page_digest=?, capabilities_json=?, last_success_at=?, last_error_at=?, last_error=? WHERE account_key=? AND canonical_origin=? AND `+mastodonSyncStateMatchClause(), args...)
	if err != nil {
		return fmt.Errorf("save conditional Mastodon sync state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect conditional Mastodon sync state write: %w", err)
	}
	if rows == 0 {
		return ErrMastodonSyncStateChanged
	}
	return nil
}

// RecordMastodonSyncErrorIfCurrent updates only diagnostics. It never copies
// stale pagination from the failed run back over a newer state.
func (s *Store) RecordMastodonSyncErrorIfCurrent(ctx context.Context, accountKey, origin, verifiedAccountID string, at time.Time, message string, expected *MastodonSyncState) error {
	state := MastodonSyncState{AccountKey: accountKey, CanonicalOrigin: origin, VerifiedAccountID: verifiedAccountID, LastErrorAt: at, LastError: message}
	state, err := normalizeMastodonSyncState(state)
	if err != nil {
		return err
	}
	if expected == nil {
		result, err := s.db.ExecContext(ctx, mastodonSyncStateInsertSQL(`ON CONFLICT(account_key, canonical_origin) DO NOTHING`), mastodonSyncStateWriteArgs(state)...)
		if err != nil {
			return fmt.Errorf("save initial Mastodon sync error: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect initial Mastodon sync error: %w", err)
		}
		if rows == 0 {
			return ErrMastodonSyncStateChanged
		}
		return nil
	}
	expectedState, err := normalizeMastodonSyncState(*expected)
	if err != nil {
		return err
	}
	if expectedState.AccountKey != state.AccountKey || expectedState.CanonicalOrigin != state.CanonicalOrigin || expectedState.VerifiedAccountID != state.VerifiedAccountID {
		return fmt.Errorf("mastodon sync error identity does not match expected state")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE mastodon_sync_state SET last_error_at=?, last_error=? WHERE account_key=? AND canonical_origin=? AND `+mastodonSyncStateMatchClause(), append([]any{formatTimeForDB(state.LastErrorAt), state.LastError, expectedState.AccountKey, expectedState.CanonicalOrigin}, mastodonSyncStateMatchArgs(expectedState)...)...)
	if err != nil {
		return fmt.Errorf("save conditional Mastodon sync error: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect conditional Mastodon sync error: %w", err)
	}
	if rows == 0 {
		return ErrMastodonSyncStateChanged
	}
	return nil
}

func normalizeMastodonSyncState(state MastodonSyncState) (MastodonSyncState, error) {
	state.AccountKey = strings.TrimSpace(state.AccountKey)
	state.CanonicalOrigin = strings.TrimSpace(state.CanonicalOrigin)
	if state.AccountKey == "" || state.CanonicalOrigin == "" {
		return MastodonSyncState{}, fmt.Errorf("mastodon sync state account key and origin are required")
	}
	if len(state.CapabilitiesJSON) > 64*1024 {
		return MastodonSyncState{}, fmt.Errorf("mastodon capability metadata exceeds 65536 bytes")
	}
	if len(state.LastError) > 2048 {
		state.LastError = state.LastError[:2048]
	}
	if state.BackfillPageOffset < 0 {
		state.BackfillPageOffset = 0
	}
	return state, nil
}

func mastodonSyncStateInsertSQL(conflict string) string {
	return `INSERT INTO mastodon_sync_state (account_key, canonical_origin, verified_account_id, handle, backfill_next_url, backfill_complete, backfill_incremental, backfill_page_url, backfill_page_digest, backfill_page_offset, last_page_url, last_page_digest, capabilities_json, last_success_at, last_error_at, last_error) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)` + " " + conflict
}

func mastodonSyncStateWriteArgs(state MastodonSyncState) []any {
	return append([]any{state.AccountKey, state.CanonicalOrigin}, mastodonSyncStateWriteArgsWithoutKeys(state)...)
}

func mastodonSyncStateWriteArgsWithoutKeys(state MastodonSyncState) []any {
	complete := 0
	if state.BackfillComplete {
		complete = 1
	}
	incremental := 0
	if state.BackfillIncremental {
		incremental = 1
	}
	return []any{state.VerifiedAccountID, state.Handle, state.BackfillNextURL, complete, incremental, state.BackfillPageURL, state.BackfillPageDigest, state.BackfillPageOffset, state.LastPageURL, state.LastPageDigest, state.CapabilitiesJSON, formatTimeForDB(state.LastSuccessAt), formatTimeForDB(state.LastErrorAt), state.LastError}
}

func mastodonSyncStateMatchClause() string {
	return `verified_account_id=? AND handle=? AND backfill_next_url=? AND backfill_complete=? AND backfill_incremental=? AND backfill_page_url=? AND backfill_page_digest=? AND backfill_page_offset=? AND last_page_url=? AND last_page_digest=? AND capabilities_json=? AND last_success_at=? AND last_error_at=? AND last_error=?`
}

func mastodonSyncStateMatchArgs(state MastodonSyncState) []any {
	return mastodonSyncStateWriteArgsWithoutKeys(state)
}

func (s *Store) ResetMastodonSyncState(ctx context.Context, accountKey, origin string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE mastodon_sync_state SET backfill_next_url='', backfill_complete=0, backfill_incremental=0, backfill_page_url='', backfill_page_digest='', backfill_page_offset=0, last_page_url='', last_page_digest='', last_error_at='', last_error='' WHERE account_key=? AND canonical_origin=?`, strings.TrimSpace(accountKey), strings.TrimSpace(origin)); err != nil {
		return fmt.Errorf("reset Mastodon sync state: %w", err)
	}
	return nil
}

// ResetMastodonSyncStateForVerifiedAccount clears pagination only when the
// snapshot loaded by this call is still current. The full-state compare-and-
// set prevents a concurrent account or pagination replacement from being
// erased by --force or stale-cursor recovery.
func (s *Store) ResetMastodonSyncStateForVerifiedAccount(ctx context.Context, accountKey, origin, verifiedAccountID string) error {
	accountKey = strings.TrimSpace(accountKey)
	origin = strings.TrimSpace(origin)
	verifiedAccountID = strings.TrimSpace(verifiedAccountID)
	if accountKey == "" || origin == "" || verifiedAccountID == "" {
		return fmt.Errorf("reset Mastodon sync state requires account key, origin, and verified account ID")
	}
	state, err := s.GetMastodonSyncState(ctx, accountKey, origin)
	if err != nil {
		return err
	}
	if state == nil {
		return nil
	}
	if state.VerifiedAccountID != verifiedAccountID {
		return fmt.Errorf("mastodon verified account identity changed from %q to %q; refusing to reset bookmark state", verifiedAccountID, state.VerifiedAccountID)
	}
	_, err = s.ResetMastodonSyncStateForVerifiedAccountIfCurrent(ctx, state)
	return err
}

// ResetMastodonSyncStateForVerifiedAccountIfCurrent clears pagination using a
// full-state CAS and returns the exact cleared snapshot. Callers must retain
// that returned value as their expected state; reloading after the reset can
// accidentally adopt a competing run's progress.
func (s *Store) ResetMastodonSyncStateForVerifiedAccountIfCurrent(ctx context.Context, expected *MastodonSyncState) (*MastodonSyncState, error) {
	if expected == nil {
		return nil, nil
	}
	normalized, err := normalizeMastodonSyncState(*expected)
	if err != nil {
		return nil, err
	}
	if normalized.VerifiedAccountID == "" {
		return nil, fmt.Errorf("reset Mastodon sync state requires a verified account ID")
	}
	cleared := normalized
	cleared.BackfillNextURL = ""
	cleared.BackfillComplete = false
	cleared.BackfillIncremental = false
	cleared.BackfillPageURL = ""
	cleared.BackfillPageDigest = ""
	cleared.BackfillPageOffset = 0
	cleared.LastPageURL = ""
	cleared.LastPageDigest = ""
	cleared.LastErrorAt = time.Time{}
	cleared.LastError = ""
	args := []any{
		cleared.BackfillNextURL,
		boolInt(cleared.BackfillComplete),
		boolInt(cleared.BackfillIncremental),
		cleared.BackfillPageURL,
		cleared.BackfillPageDigest,
		cleared.BackfillPageOffset,
		cleared.LastPageURL,
		cleared.LastPageDigest,
		formatTimeForDB(cleared.LastErrorAt),
		cleared.LastError,
		normalized.AccountKey,
		normalized.CanonicalOrigin,
	}
	args = append(args, mastodonSyncStateMatchArgs(normalized)...)
	result, err := s.db.ExecContext(ctx, `UPDATE mastodon_sync_state SET backfill_next_url=?, backfill_complete=?, backfill_incremental=?, backfill_page_url=?, backfill_page_digest=?, backfill_page_offset=?, last_page_url=?, last_page_digest=?, last_error_at=?, last_error=? WHERE account_key=? AND canonical_origin=? AND `+mastodonSyncStateMatchClause(), args...)
	if err != nil {
		return nil, fmt.Errorf("reset Mastodon sync state for verified account: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("inspect Mastodon sync state reset: %w", err)
	}
	if rows == 0 {
		return nil, ErrMastodonSyncStateChanged
	}
	return &cleared, nil
}

const mastodonSyncStateTableDDL = `CREATE TABLE mastodon_sync_state (
		account_key TEXT NOT NULL,
		canonical_origin TEXT NOT NULL,
		verified_account_id TEXT NOT NULL DEFAULT '',
		handle TEXT NOT NULL DEFAULT '',
		backfill_next_url TEXT NOT NULL DEFAULT '',
		backfill_complete INTEGER NOT NULL DEFAULT 0,
		backfill_incremental INTEGER NOT NULL DEFAULT 0,
		backfill_page_url TEXT NOT NULL DEFAULT '',
		backfill_page_digest TEXT NOT NULL DEFAULT '',
		backfill_page_offset INTEGER NOT NULL DEFAULT 0,
		last_page_url TEXT NOT NULL DEFAULT '',
		last_page_digest TEXT NOT NULL DEFAULT '',
		capabilities_json TEXT NOT NULL DEFAULT '',
		last_success_at TEXT NOT NULL DEFAULT '',
		last_error_at TEXT NOT NULL DEFAULT '',
		last_error TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (account_key, canonical_origin),
		UNIQUE (canonical_origin, verified_account_id)
	)`

var mastodonSyncStateColumns = []struct {
	name         string
	defaultValue string
}{
	{name: "account_key", defaultValue: "''"},
	{name: "canonical_origin", defaultValue: "''"},
	{name: "verified_account_id", defaultValue: "''"},
	{name: "handle", defaultValue: "''"},
	{name: "backfill_next_url", defaultValue: "''"},
	{name: "backfill_complete", defaultValue: "0"},
	{name: "backfill_incremental", defaultValue: "0"},
	{name: "backfill_page_url", defaultValue: "''"},
	{name: "backfill_page_digest", defaultValue: "''"},
	{name: "backfill_page_offset", defaultValue: "0"},
	{name: "last_page_url", defaultValue: "''"},
	{name: "last_page_digest", defaultValue: "''"},
	{name: "capabilities_json", defaultValue: "''"},
	{name: "last_success_at", defaultValue: "''"},
	{name: "last_error_at", defaultValue: "''"},
	{name: "last_error", defaultValue: "''"},
}

type mastodonSyncStateSchema struct {
	columns       map[string]struct{}
	primaryKey    []string
	uniqueIndexes [][]string
	indexNames    []string
}

func ensureMastodonSyncStateTable(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS mastodon_sync_state (
		account_key TEXT NOT NULL,
		canonical_origin TEXT NOT NULL,
		verified_account_id TEXT NOT NULL DEFAULT '',
		handle TEXT NOT NULL DEFAULT '',
		backfill_next_url TEXT NOT NULL DEFAULT '',
		backfill_complete INTEGER NOT NULL DEFAULT 0,
		backfill_incremental INTEGER NOT NULL DEFAULT 0,
		backfill_page_url TEXT NOT NULL DEFAULT '',
		backfill_page_digest TEXT NOT NULL DEFAULT '',
		backfill_page_offset INTEGER NOT NULL DEFAULT 0,
		last_page_url TEXT NOT NULL DEFAULT '',
		last_page_digest TEXT NOT NULL DEFAULT '',
		capabilities_json TEXT NOT NULL DEFAULT '',
		last_success_at TEXT NOT NULL DEFAULT '',
		last_error_at TEXT NOT NULL DEFAULT '',
		last_error TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (account_key, canonical_origin),
		UNIQUE (canonical_origin, verified_account_id)
	)`); err != nil {
		return fmt.Errorf("ensure Mastodon sync state table: %w", err)
	}
	schema, err := inspectMastodonSyncStateSchema(db)
	if err != nil {
		return err
	}
	if mastodonSyncStateSchemaReady(schema) {
		return ensureMastodonSyncStateVerifiedIndex(db)
	}

	for _, required := range []string{"account_key", "canonical_origin", "verified_account_id"} {
		if _, ok := schema.columns[required]; !ok {
			return fmt.Errorf("repair Mastodon sync state: required column %q is missing", required)
		}
	}
	if err := rejectDuplicateMastodonSyncStateIdentity(db); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin Mastodon sync state repair: %w", err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	if err := dropMastodonSyncStateUserIndexes(tx, schema.indexNames); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE mastodon_sync_state RENAME TO mastodon_sync_state_rebuild`); err != nil {
		return fmt.Errorf("rename deficient Mastodon sync state table: %w", err)
	}
	if _, err := tx.Exec(mastodonSyncStateTableDDL); err != nil {
		return fmt.Errorf("create repaired Mastodon sync state table: %w", err)
	}
	columns := make([]string, 0, len(mastodonSyncStateColumns))
	expressions := make([]string, 0, len(mastodonSyncStateColumns))
	for _, column := range mastodonSyncStateColumns {
		columns = append(columns, quoteSQLiteIdentifier(column.name))
		if _, ok := schema.columns[column.name]; ok {
			expressions = append(expressions, quoteSQLiteIdentifier(column.name))
		} else {
			expressions = append(expressions, column.defaultValue)
		}
	}
	copySQL := `INSERT INTO mastodon_sync_state (` + strings.Join(columns, ", ") + `) SELECT ` + strings.Join(expressions, ", ") + ` FROM mastodon_sync_state_rebuild`
	if _, err := tx.Exec(copySQL); err != nil {
		return fmt.Errorf("copy Mastodon sync state rows during repair: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE mastodon_sync_state_rebuild`); err != nil {
		return fmt.Errorf("drop deficient Mastodon sync state table: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_mastodon_sync_state_verified_account ON mastodon_sync_state(canonical_origin, verified_account_id)`); err != nil {
		return fmt.Errorf("ensure Mastodon sync state index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Mastodon sync state repair: %w", err)
	}
	rollback = false
	return nil
}

func inspectMastodonSyncStateSchema(db interface {
	Query(string, ...any) (*sql.Rows, error)
}) (mastodonSyncStateSchema, error) {
	schema := mastodonSyncStateSchema{columns: make(map[string]struct{})}
	rows, err := db.Query(`PRAGMA table_info(mastodon_sync_state)`)
	if err != nil {
		return schema, fmt.Errorf("inspect Mastodon sync state columns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	primaryByOrder := make(map[int]string)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType, defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return schema, fmt.Errorf("scan Mastodon sync state column: %w", err)
		}
		schema.columns[name.String] = struct{}{}
		if primaryKey > 0 {
			primaryByOrder[primaryKey] = name.String
		}
	}
	if err := rows.Err(); err != nil {
		return schema, fmt.Errorf("iterate Mastodon sync state columns: %w", err)
	}
	for order := 1; ; order++ {
		name, ok := primaryByOrder[order]
		if !ok {
			break
		}
		schema.primaryKey = append(schema.primaryKey, name)
	}
	indexes, err := db.Query(`PRAGMA index_list(mastodon_sync_state)`)
	if err != nil {
		return schema, fmt.Errorf("inspect Mastodon sync state indexes: %w", err)
	}
	type indexDescriptor struct {
		name   string
		unique bool
	}
	descriptors := make([]indexDescriptor, 0)
	for indexes.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := indexes.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			return schema, fmt.Errorf("scan Mastodon sync state index: %w", err)
		}
		isAutoIndex := strings.HasPrefix(name, "sqlite_autoindex_")
		if !isAutoIndex {
			schema.indexNames = append(schema.indexNames, name)
		}
		descriptors = append(descriptors, indexDescriptor{name: name, unique: unique != 0})
	}
	if err := indexes.Err(); err != nil {
		_ = indexes.Close()
		return schema, fmt.Errorf("iterate Mastodon sync state indexes: %w", err)
	}
	if err := indexes.Close(); err != nil {
		return schema, fmt.Errorf("close Mastodon sync state index inspection: %w", err)
	}
	for _, descriptor := range descriptors {
		if !descriptor.unique {
			continue
		}
		columns, err := mastodonSyncStateIndexColumns(db, descriptor.name)
		if err != nil {
			return schema, err
		}
		schema.uniqueIndexes = append(schema.uniqueIndexes, columns)
	}
	return schema, nil
}

func mastodonSyncStateIndexColumns(db interface {
	Query(string, ...any) (*sql.Rows, error)
}, name string) ([]string, error) {
	rows, err := db.Query(`PRAGMA index_info(` + quoteSQLiteIdentifier(name) + `)`)
	if err != nil {
		return nil, fmt.Errorf("inspect Mastodon sync state index %q: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	byOrder := make(map[int]string)
	for rows.Next() {
		var sequence, columnID int
		var columnName sql.NullString
		if err := rows.Scan(&sequence, &columnID, &columnName); err != nil {
			return nil, fmt.Errorf("scan Mastodon sync state index %q: %w", name, err)
		}
		byOrder[sequence] = columnName.String
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Mastodon sync state index %q: %w", name, err)
	}
	columns := make([]string, 0, len(byOrder))
	for order := 0; ; order++ {
		column, ok := byOrder[order]
		if !ok {
			break
		}
		columns = append(columns, column)
	}
	return columns, nil
}

func mastodonSyncStateSchemaReady(schema mastodonSyncStateSchema) bool {
	for _, column := range mastodonSyncStateColumns {
		if _, ok := schema.columns[column.name]; !ok {
			return false
		}
	}
	if len(schema.primaryKey) != 2 || schema.primaryKey[0] != "account_key" || schema.primaryKey[1] != "canonical_origin" {
		return false
	}
	for _, columns := range schema.uniqueIndexes {
		if len(columns) == 2 && columns[0] == "canonical_origin" && columns[1] == "verified_account_id" {
			return true
		}
	}
	return false
}

func rejectDuplicateMastodonSyncStateIdentity(db interface {
	Query(string, ...any) (*sql.Rows, error)
}) error {
	rows, err := db.Query(`SELECT account_key, canonical_origin, COUNT(*) FROM mastodon_sync_state GROUP BY account_key, canonical_origin HAVING COUNT(*) > 1`)
	if err != nil {
		return fmt.Errorf("inspect duplicate Mastodon sync state keys: %w", err)
	}
	if rows.Next() {
		var accountKey, origin string
		var count int
		if err := rows.Scan(&accountKey, &origin, &count); err != nil {
			return fmt.Errorf("scan duplicate Mastodon sync state key: %w", err)
		}
		return fmt.Errorf("repair Mastodon sync state: duplicate account/origin key %q/%q (%d rows)", accountKey, origin, count)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate duplicate Mastodon sync state keys: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close duplicate Mastodon sync state keys: %w", err)
	}
	rows, err = db.Query(`SELECT canonical_origin, verified_account_id, COUNT(*) FROM mastodon_sync_state GROUP BY canonical_origin, verified_account_id HAVING COUNT(*) > 1`)
	if err != nil {
		return fmt.Errorf("inspect duplicate Mastodon verified identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var origin, verifiedID string
		var count int
		if err := rows.Scan(&origin, &verifiedID, &count); err != nil {
			return fmt.Errorf("scan duplicate Mastodon verified identity: %w", err)
		}
		return fmt.Errorf("repair Mastodon sync state: duplicate verified identity %q/%q (%d rows)", origin, verifiedID, count)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate duplicate Mastodon verified identities: %w", err)
	}
	return nil
}

func dropMastodonSyncStateUserIndexes(tx interface {
	Exec(string, ...any) (sql.Result, error)
}, names []string) error {
	for _, name := range names {
		if _, err := tx.Exec(`DROP INDEX IF EXISTS ` + quoteSQLiteIdentifier(name)); err != nil {
			return fmt.Errorf("drop deficient Mastodon sync state index %q: %w", name, err)
		}
	}
	return nil
}

func ensureMastodonSyncStateVerifiedIndex(db interface {
	Exec(string, ...any) (sql.Result, error)
}) error {
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_mastodon_sync_state_verified_account ON mastodon_sync_state(canonical_origin, verified_account_id)`); err != nil {
		return fmt.Errorf("ensure Mastodon sync state index: %w", err)
	}
	return nil
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
