package sqlitearchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/darron/dbrain/internal/config"
	brainstore "github.com/darron/dbrain/internal/store"
)

func TestArchiveSnapshotsCompressesAndUploadsSQLiteDB(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := testConfig(t)
	writeTestDB(t, cfg.DBPath, "archived value")
	store := newMemoryStore()
	now := time.Date(2026, 4, 26, 20, 15, 30, 0, time.UTC)
	var events []Event

	result, err := Archive(ctx, cfg, Options{
		Prefix:   "archive/db",
		Now:      func() time.Time { return now },
		Store:    store,
		Progress: func(event Event) { events = append(events, event) },
	})
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if result.Key != "archive/db/brain-20260426T201530Z.db.gz" {
		t.Fatalf("unexpected archive key %q", result.Key)
	}
	if result.SnapshotSize <= 0 || result.ArchiveSize <= 0 {
		t.Fatalf("expected positive sizes, got %+v", result)
	}

	uploaded := store.objects[result.Key]
	if len(uploaded.body) == 0 {
		t.Fatalf("expected uploaded object body")
	}
	snapshot := gunzipBytes(t, uploaded.body)
	restoredPath := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(restoredPath, snapshot, 0o644); err != nil {
		t.Fatalf("write restored snapshot: %v", err)
	}
	if got := readTestValue(t, restoredPath); got != "archived value" {
		t.Fatalf("unexpected restored value %q", got)
	}
	for _, want := range []EventKind{EventStageStart, EventTransferProgress, EventStageDone} {
		if !hasEventKind(events, want) {
			t.Fatalf("expected archive progress event %q in %+v", want, events)
		}
	}
}

func TestLatestChoosesNewestArchiveUnderPrefix(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.addObject("archive/db/brain-20260426T201500Z.db.gz", time.Date(2026, 4, 26, 20, 15, 0, 0, time.UTC), nil)
	store.addObject("archive/db/brain-20260426T201700Z.db.gz", time.Date(2026, 4, 26, 20, 17, 0, 0, time.UTC), nil)
	store.addObject("archive/other/brain-20260426T202000Z.db.gz", time.Date(2026, 4, 26, 20, 20, 0, 0, time.UTC), nil)
	store.addObject("archive/db/readme.txt", time.Date(2026, 4, 26, 20, 30, 0, 0, time.UTC), nil)

	plan, err := Latest(context.Background(), Options{Prefix: "archive/db", Store: store})
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if plan.Object.Key != "archive/db/brain-20260426T201700Z.db.gz" {
		t.Fatalf("unexpected latest key %q", plan.Object.Key)
	}
}

func TestRestoreMovesExistingSQLiteFilesAndInstallsDbrainArchive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := testConfig(t)
	writeDbrainTestDB(t, cfg.DBPath, "old value")
	if err := os.WriteFile(cfg.DBPath+"-wal", []byte("wal"), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	if err := os.WriteFile(cfg.DBPath+"-shm", []byte("shm"), 0o644); err != nil {
		t.Fatalf("write shm: %v", err)
	}

	sourceDB := filepath.Join(t.TempDir(), "source.db")
	writeDbrainTestDB(t, sourceDB, "restored value")
	compressed := gzipDB(t, sourceDB)

	store := newMemoryStore()
	key := "archive/db/brain-20260426T201500Z.db.gz"
	store.addObject(key, time.Date(2026, 4, 26, 20, 15, 0, 0, time.UTC), compressed.Bytes())
	now := time.Date(2026, 4, 26, 20, 20, 0, 0, time.UTC)
	var events []Event

	result, err := Restore(ctx, cfg, RestorePlan{Object: Object{Key: key, Size: int64(compressed.Len())}}, Options{
		Now:      func() time.Time { return now },
		Store:    store,
		Progress: func(event Event) { events = append(events, event) },
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readTestValue(t, cfg.DBPath); got != "restored value" {
		t.Fatalf("unexpected restored value %q", got)
	}
	wantBackups := []string{
		cfg.DBPath + ".pre-restore-20260426T202000Z",
		cfg.DBPath + "-wal.pre-restore-20260426T202000Z",
		cfg.DBPath + "-shm.pre-restore-20260426T202000Z",
	}
	if len(result.BackupPaths) != len(wantBackups) {
		t.Fatalf("expected backups %v, got %v", wantBackups, result.BackupPaths)
	}
	for _, path := range wantBackups {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected backup %s: %v", path, err)
		}
	}
	for _, want := range []EventKind{EventStageStart, EventTransferProgress, EventStageDone} {
		if !hasEventKind(events, want) {
			t.Fatalf("expected restore progress event %q in %+v", want, events)
		}
	}
}

func TestRestoreRejectsForeignSQLiteBeforeMovingExistingFiles(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	writeDbrainTestDB(t, cfg.DBPath, "old value")
	writeSQLiteSidecars(t, cfg.DBPath)
	before := readSQLiteFileSet(t, cfg.DBPath)

	sourceDB := filepath.Join(t.TempDir(), "foreign.db")
	writeTestDB(t, sourceDB, "foreign value")
	events, err := restoreTestDB(t, cfg, sourceDB)
	if err == nil {
		t.Fatal("expected restore to reject a foreign SQLite database")
	}
	assertSQLiteFilesNotMoved(t, cfg.DBPath)
	assertSQLiteFileSetUnchanged(t, cfg.DBPath, before)
	if got := readTestValue(t, cfg.DBPath); got != "old value" {
		t.Fatalf("existing database changed after rejected restore: %q", got)
	}
	if hasStageEvent(events, "install") {
		t.Fatalf("install stage emitted for rejected restore: %+v", events)
	}
}

func TestRestoreRejectsFutureDbrainSchemaBeforeMovingExistingFiles(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	writeDbrainTestDB(t, cfg.DBPath, "old value")
	writeSQLiteSidecars(t, cfg.DBPath)
	before := readSQLiteFileSet(t, cfg.DBPath)

	sourceDB := filepath.Join(t.TempDir(), "future.db")
	writeDbrainTestDB(t, sourceDB, "future value")
	execTestDB(t, sourceDB, `PRAGMA user_version = 999`)
	events, err := restoreTestDB(t, cfg, sourceDB)
	if err == nil {
		t.Fatal("expected restore to reject a future dbrain schema")
	}
	assertSQLiteFilesNotMoved(t, cfg.DBPath)
	assertSQLiteFileSetUnchanged(t, cfg.DBPath, before)
	if got := readTestValue(t, cfg.DBPath); got != "old value" {
		t.Fatalf("existing database changed after rejected restore: %q", got)
	}
	if hasStageEvent(events, "install") {
		t.Fatalf("install stage emitted for rejected restore: %+v", events)
	}
}

func TestRestoreRejectsUnknownMigrationNameBeforeMovingExistingFiles(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	writeDbrainTestDB(t, cfg.DBPath, "old value")
	writeSQLiteSidecars(t, cfg.DBPath)
	before := readSQLiteFileSet(t, cfg.DBPath)

	sourceDB := filepath.Join(t.TempDir(), "mismatch.db")
	writeDbrainTestDB(t, sourceDB, "mismatch value")
	execTestDB(t, sourceDB, `UPDATE schema_migrations SET name = 'unknown_migration' WHERE version = 6`)
	events, err := restoreTestDB(t, cfg, sourceDB)
	if err == nil {
		t.Fatal("expected restore to reject an unknown migration name")
	}
	assertSQLiteFilesNotMoved(t, cfg.DBPath)
	assertSQLiteFileSetUnchanged(t, cfg.DBPath, before)
	if got := readTestValue(t, cfg.DBPath); got != "old value" {
		t.Fatalf("existing database changed after rejected restore: %q", got)
	}
	if hasStageEvent(events, "install") {
		t.Fatalf("install stage emitted for rejected restore: %+v", events)
	}
}

func TestRestoreRejectsCorruptSQLiteBeforeMovingExistingFiles(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	writeDbrainTestDB(t, cfg.DBPath, "old value")
	writeSQLiteSidecars(t, cfg.DBPath)
	before := readSQLiteFileSet(t, cfg.DBPath)

	corruptDB := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(corruptDB, []byte("not a SQLite database"), 0o600); err != nil {
		t.Fatalf("write corrupt database: %v", err)
	}
	events, err := restoreTestDB(t, cfg, corruptDB)
	if err == nil {
		t.Fatal("expected restore to reject corrupt SQLite database")
	}
	assertSQLiteFilesNotMoved(t, cfg.DBPath)
	assertSQLiteFileSetUnchanged(t, cfg.DBPath, before)
	if got := readTestValue(t, cfg.DBPath); got != "old value" {
		t.Fatalf("existing database changed after rejected restore: %q", got)
	}
	if hasStageEvent(events, "install") {
		t.Fatalf("install stage emitted for rejected restore: %+v", events)
	}
}

func TestRestoreAcceptsHistoricalMigrationNameAlias(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	writeDbrainTestDB(t, cfg.DBPath, "old value")

	sourceDB := filepath.Join(t.TempDir(), "legacy-alias.db")
	writeDbrainTestDB(t, sourceDB, "legacy value")
	execTestDB(t, sourceDB, `
		UPDATE schema_migrations SET name = 'source_summary_failure_timestamp' WHERE version = 6;
		DELETE FROM schema_migrations WHERE version >= 8;
		PRAGMA user_version = 7;
	`)
	if _, err := restoreTestDB(t, cfg, sourceDB); err != nil {
		t.Fatalf("restore historical migration alias: %v", err)
	}
	if got := readTestValue(t, cfg.DBPath); got != "legacy value" {
		t.Fatalf("unexpected restored value %q", got)
	}
}

type memoryStore struct {
	objects map[string]memoryObject
}

type memoryObject struct {
	body         []byte
	lastModified time.Time
}

func newMemoryStore() *memoryStore {
	return &memoryStore{objects: map[string]memoryObject{}}
}

func (s *memoryStore) PutObject(_ context.Context, key string, body io.Reader, _ string, contentLength int64) (string, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	if contentLength != int64(len(data)) {
		return "", errUnexpectedContentLength{}
	}
	s.addObject(key, time.Now().UTC(), data)
	return `"etag"`, nil
}

func (s *memoryStore) ListObjects(_ context.Context, prefix string) ([]Object, error) {
	var objects []Object
	for key, obj := range s.objects {
		if prefix != "" && !bytes.HasPrefix([]byte(key), []byte(prefix)) {
			continue
		}
		objects = append(objects, Object{
			Key:          key,
			LastModified: obj.lastModified,
			Size:         int64(len(obj.body)),
		})
	}
	return objects, nil
}

func (s *memoryStore) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	obj, ok := s.objects[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(obj.body)), nil
}

func (s *memoryStore) addObject(key string, modified time.Time, body []byte) {
	s.objects[key] = memoryObject{body: append([]byte(nil), body...), lastModified: modified}
}

func hasEventKind(events []Event, kind EventKind) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

type errUnexpectedContentLength struct{}

func (errUnexpectedContentLength) Error() string {
	return "unexpected content length"
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	return cfg
}

func writeTestDB(t *testing.T, path string, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir db dir: %v", err)
	}
	db, err := sql.Open(sqliteDriverName, path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()
	if _, err := db.Exec(`CREATE TABLE test_values (value TEXT NOT NULL);`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO test_values (value) VALUES (?)`, value); err != nil {
		t.Fatalf("insert value: %v", err)
	}
}

func writeDbrainTestDB(t *testing.T, path string, value string) {
	t.Helper()

	st, err := brainstore.Open(path)
	if err != nil {
		t.Fatalf("open dbrain test database: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close dbrain test database: %v", err)
	}
	writeTestDB(t, path, value)
}

func execTestDB(t *testing.T, path string, statement string) {
	t.Helper()

	db, err := sql.Open(sqliteDriverName, path)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()
	if _, err := db.Exec(statement); err != nil {
		t.Fatalf("execute test database statement: %v", err)
	}
}

func gzipDB(t *testing.T, path string) bytes.Buffer {
	t.Helper()

	var compressed bytes.Buffer
	gw := gzip.NewWriter(&compressed)
	dbBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read source db: %v", err)
	}
	if _, err := gw.Write(dbBytes); err != nil {
		t.Fatalf("gzip source db: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return compressed
}

func restoreTestDB(t *testing.T, cfg config.Config, sourceDB string) ([]Event, error) {
	t.Helper()

	compressed := gzipDB(t, sourceDB)
	objectStore := newMemoryStore()
	key := "archive/db/brain-20260426T201500Z.db.gz"
	objectStore.addObject(key, time.Date(2026, 4, 26, 20, 15, 0, 0, time.UTC), compressed.Bytes())
	var events []Event
	_, err := Restore(context.Background(), cfg, RestorePlan{Object: Object{Key: key, Size: int64(compressed.Len())}}, Options{
		Now:      func() time.Time { return time.Date(2026, 4, 26, 20, 20, 0, 0, time.UTC) },
		Store:    objectStore,
		Progress: func(event Event) { events = append(events, event) },
	})
	return events, err
}

func writeSQLiteSidecars(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path+"-wal", []byte("wal"), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	if err := os.WriteFile(path+"-shm", []byte("shm"), 0o644); err != nil {
		t.Fatalf("write shm: %v", err)
	}
}

func assertSQLiteFilesNotMoved(t *testing.T, path string) {
	t.Helper()

	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(candidate); err != nil {
			t.Fatalf("expected existing SQLite file to remain at %s: %v", candidate, err)
		}
		matches, err := filepath.Glob(candidate + ".pre-restore-*")
		if err != nil {
			t.Fatalf("glob backup files for %s: %v", candidate, err)
		}
		if len(matches) != 0 {
			t.Fatalf("rejected restore moved %s to backups: %v", candidate, matches)
		}
	}
}

func readSQLiteFileSet(t *testing.T, path string) map[string][]byte {
	t.Helper()

	files := make(map[string][]byte, 3)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatalf("read SQLite file %s: %v", path+suffix, err)
		}
		files[suffix] = data
	}
	return files
}

func assertSQLiteFileSetUnchanged(t *testing.T, path string, before map[string][]byte) {
	t.Helper()

	for _, suffix := range []string{"", "-wal", "-shm"} {
		after, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatalf("read SQLite file %s after rejected restore: %v", path+suffix, err)
		}
		if !bytes.Equal(after, before[suffix]) {
			t.Fatalf("SQLite file %s changed after rejected restore", path+suffix)
		}
	}
}

func hasStageEvent(events []Event, stage string) bool {
	for _, event := range events {
		if event.Stage == stage {
			return true
		}
	}
	return false
}

func readTestValue(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open(sqliteDriverName, path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()
	var value string
	if err := db.QueryRow(`SELECT value FROM test_values LIMIT 1`).Scan(&value); err != nil {
		t.Fatalf("select value: %v", err)
	}
	return value
}

func gunzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer func() {
		_ = gr.Close()
	}()
	out, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("ReadAll gzip: %v", err)
	}
	return out
}
