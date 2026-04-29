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

func TestRestoreMovesExistingSQLiteFilesAndInstallsArchive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := testConfig(t)
	writeTestDB(t, cfg.DBPath, "old value")
	if err := os.WriteFile(cfg.DBPath+"-wal", []byte("wal"), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	if err := os.WriteFile(cfg.DBPath+"-shm", []byte("shm"), 0o644); err != nil {
		t.Fatalf("write shm: %v", err)
	}

	sourceDB := filepath.Join(t.TempDir(), "source.db")
	writeTestDB(t, sourceDB, "restored value")
	var compressed bytes.Buffer
	gw := gzip.NewWriter(&compressed)
	dbBytes, err := os.ReadFile(sourceDB)
	if err != nil {
		t.Fatalf("read source db: %v", err)
	}
	if _, err := gw.Write(dbBytes); err != nil {
		t.Fatalf("gzip source db: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

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
