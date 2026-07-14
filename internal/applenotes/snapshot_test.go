package applenotes

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/config"

	_ "modernc.org/sqlite"
)

type cancelAfterFirstRead struct {
	cancel context.CancelFunc
	reads  int
}

func (r *cancelAfterFirstRead) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > 1 {
		return 0, fmt.Errorf("reader was called after cancellation")
	}
	copy(p, "first chunk")
	r.cancel()
	return len("first chunk"), nil
}

func TestSnapshotCopyReaderStopsBetweenChunksOnCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterFirstRead{cancel: cancel}
	var dst bytes.Buffer
	_, err := copyReaderContext(ctx, &dst, reader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("copyReaderContext error = %v, want canceled", err)
	}
	if reader.reads != 1 || dst.String() != "first chunk" {
		t.Fatalf("copy state reads=%d body=%q", reader.reads, dst.String())
	}
}

func TestCreateSnapshotContextChecksCancellationBeforeSourceAccess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = CreateSnapshotContext(ctx, cfg, Options{DBPath: filepath.Join(root, "missing.sqlite")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateSnapshotContext error = %v, want canceled", err)
	}
}

func TestCreateSnapshotCopiesTripletWithoutHardlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	sourceDB := filepath.Join(root, "NoteStore.sqlite")
	writeSQLiteFixture(t, sourceDB)
	for _, path := range []string{sourceDB + "-wal", sourceDB + "-shm"} {
		if err := os.WriteFile(path, []byte(filepath.Base(path)+"\n"), 0o600); err != nil {
			t.Fatalf("write sidecar %s: %v", path, err)
		}
	}

	snapshotDir := filepath.Join(root, "snapshot")
	info, cleanup, err := CreateSnapshot(cfg, Options{
		DBPath:      sourceDB,
		SnapshotDir: snapshotDir,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	defer func() {
		if err := cleanup(); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
	}()

	if info.DBPath != filepath.Join(snapshotDir, "NoteStore.sqlite") {
		t.Fatalf("DBPath = %q", info.DBPath)
	}
	if len(info.CopiedFiles) != 3 {
		t.Fatalf("CopiedFiles len = %d, want 3 (%v)", len(info.CopiedFiles), info.CopiedFiles)
	}
	for _, source := range notesTripletPaths(sourceDB) {
		dest := filepath.Join(snapshotDir, filepath.Base(source))
		if _, err := os.Stat(dest); err != nil {
			t.Fatalf("snapshot missing %s: %v", dest, err)
		}
		if sameFile(source, dest) {
			t.Fatalf("snapshot %s aliases source %s", dest, source)
		}
	}
}

func TestProbeSnapshotsAndDoesNotDecodeBodies(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	sourceDB := filepath.Join(root, "NoteStore.sqlite")
	writeSQLiteFixture(t, sourceDB)

	stats, err := Probe(context.Background(), cfg, Options{DBPath: sourceDB})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if stats.SourceDBPath != sourceDB {
		t.Fatalf("SourceDBPath = %q, want %q", stats.SourceDBPath, sourceDB)
	}
	if stats.Snapshot.DBPath == sourceDB {
		t.Fatalf("probe opened source DB instead of snapshot")
	}
	if stats.NoteCount != 1 {
		t.Fatalf("NoteCount = %d, want 1", stats.NoteCount)
	}
	if stats.FolderCount != 1 {
		t.Fatalf("FolderCount = %d, want 1", stats.FolderCount)
	}
	if !stats.Tables["ZICCLOUDSYNCINGOBJECT"].Exists {
		t.Fatalf("expected object table to exist")
	}
	if containsString(stats.Tables["ZICNOTEDATA"].Columns, "ZDATA") == false {
		t.Fatalf("expected ZICNOTEDATA.ZDATA column, got %v", stats.Tables["ZICNOTEDATA"].Columns)
	}
	if _, err := os.Stat(stats.Snapshot.Dir); !os.IsNotExist(err) {
		t.Fatalf("probe should prune temp snapshot, stat err=%v", err)
	}
}

func TestProbeFailsClosedOnInvalidSnapshot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	sourceDB := filepath.Join(root, "NoteStore.sqlite")
	if err := os.WriteFile(sourceDB, []byte("not sqlite"), 0o600); err != nil {
		t.Fatalf("write invalid db: %v", err)
	}

	if _, err := Probe(context.Background(), cfg, Options{DBPath: sourceDB}); err == nil {
		t.Fatalf("expected invalid snapshot to fail")
	}
}

func TestAppleNotesSourcePermissionErrorExplainsFullDiskAccess(t *testing.T) {
	t.Parallel()

	err := appleNotesSourcePermissionError(
		"/Users/example/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite",
		os.ErrPermission,
	)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected wrapped permission error, got %v", err)
	}
	message := err.Error()
	for _, want := range []string{
		"Full Disk Access",
		"System Settings > Privacy & Security > Full Disk Access",
		"quit and reopen",
		"parent terminal/IDE",
		"Local rebuilds may invalidate binary-specific grants",
		"group.com.apple.notes/NoteStore.sqlite",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("permission diagnostic missing %q:\n%s", want, message)
		}
	}
}

func writeSQLiteFixture(t *testing.T, path string) {
	t.Helper()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()
	for _, stmt := range []string{
		`CREATE TABLE ZICCLOUDSYNCINGOBJECT (
			Z_PK INTEGER PRIMARY KEY,
			ZTITLE1 TEXT,
			ZTITLE2 TEXT,
			ZNAME TEXT,
			ZSNIPPET TEXT,
			ZMARKEDFORDELETION INTEGER DEFAULT 0
		);`,
		`CREATE TABLE ZICNOTEDATA (
			Z_PK INTEGER PRIMARY KEY,
			ZDATA BLOB
		);`,
		`CREATE TABLE Z_METADATA (
			Z_VERSION INTEGER
		);`,
		`CREATE TABLE Z_PRIMARYKEY (
			Z_ENT INTEGER,
			Z_NAME TEXT
		);`,
		`INSERT INTO ZICCLOUDSYNCINGOBJECT (Z_PK, ZTITLE1, ZSNIPPET) VALUES (1, 'Probe Note', 'probe snippet');`,
		`INSERT INTO ZICCLOUDSYNCINGOBJECT (Z_PK, ZTITLE2) VALUES (2, 'Folder');`,
		`INSERT INTO ZICNOTEDATA (Z_PK, ZDATA) VALUES (1, X'0A0474657374');`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec fixture stmt %q: %v", stmt, err)
		}
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
