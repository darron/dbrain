package sqlitearchive

import (
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"dbrain/internal/config"
)

const (
	DefaultPrefix = "archive/db"

	sqliteDriverName = "sqlite"
	timestampLayout  = "20060102T150405Z"
)

type Options struct {
	Prefix   string
	Now      func() time.Time
	Store    ObjectStore
	Progress func(Event)
}

type ObjectStore interface {
	PutObject(ctx context.Context, key string, body io.Reader, contentType string, contentLength int64) (string, error)
	ListObjects(ctx context.Context, prefix string) ([]Object, error)
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)
}

type Object struct {
	Key          string    `json:"key"`
	LastModified time.Time `json:"last_modified"`
	Size         int64     `json:"size"`
}

type ArchiveResult struct {
	Key          string    `json:"key"`
	LocalDBPath  string    `json:"local_db_path"`
	SnapshotSize int64     `json:"snapshot_size"`
	ArchiveSize  int64     `json:"archive_size"`
	ETag         string    `json:"etag,omitempty"`
	ArchivedAt   time.Time `json:"archived_at"`
}

type RestorePlan struct {
	Object Object `json:"object"`
}

type RestoreResult struct {
	Key          string    `json:"key"`
	RestoredPath string    `json:"restored_path"`
	BackupPaths  []string  `json:"backup_paths,omitempty"`
	RestoredAt   time.Time `json:"restored_at"`
}

type EventKind string

const (
	EventStageStart       EventKind = "stage_start"
	EventStageDone        EventKind = "stage_done"
	EventTransferProgress EventKind = "transfer_progress"
)

type Event struct {
	Kind    EventKind
	Stage   string
	Message string
	Current int64
	Total   int64
}

func Archive(ctx context.Context, cfg config.Config, opts Options) (ArchiveResult, error) {
	store, err := requireStore(opts)
	if err != nil {
		return ArchiveResult{}, err
	}
	if _, err := os.Stat(cfg.DBPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ArchiveResult{}, fmt.Errorf("sqlite database does not exist: %s", cfg.DBPath)
		}
		return ArchiveResult{}, fmt.Errorf("stat sqlite database %s: %w", cfg.DBPath, err)
	}

	now := optionNow(opts).UTC()
	workDir, err := cfg.MkdirTemp("sqlite-archive-*")
	if err != nil {
		return ArchiveResult{}, err
	}
	defer func() {
		_ = os.RemoveAll(workDir)
	}()

	snapshotPath := filepath.Join(workDir, "brain.db")
	emitProgress(opts, Event{Kind: EventStageStart, Stage: "snapshot", Message: "Snapshotting SQLite database"})
	if err := snapshotSQLite(ctx, cfg.DBPath, snapshotPath); err != nil {
		return ArchiveResult{}, err
	}
	snapshotInfo, err := os.Stat(snapshotPath)
	if err != nil {
		return ArchiveResult{}, fmt.Errorf("stat sqlite snapshot %s: %w", snapshotPath, err)
	}
	emitProgress(opts, Event{Kind: EventStageDone, Stage: "snapshot", Message: fmt.Sprintf("Snapshot created (%d bytes)", snapshotInfo.Size())})

	archiveName := fmt.Sprintf("brain-%s.db.gz", now.Format(timestampLayout))
	key := objectKey(effectivePrefix(opts.Prefix), archiveName)
	gzipPath := filepath.Join(workDir, archiveName)
	emitProgress(opts, Event{Kind: EventStageStart, Stage: "compress", Message: "Compressing SQLite snapshot"})
	if err := gzipFile(snapshotPath, gzipPath); err != nil {
		return ArchiveResult{}, err
	}
	gzipInfo, err := os.Stat(gzipPath)
	if err != nil {
		return ArchiveResult{}, fmt.Errorf("stat compressed sqlite archive %s: %w", gzipPath, err)
	}
	emitProgress(opts, Event{Kind: EventStageDone, Stage: "compress", Message: fmt.Sprintf("Compressed archive (%d bytes)", gzipInfo.Size())})

	file, err := os.Open(gzipPath)
	if err != nil {
		return ArchiveResult{}, fmt.Errorf("open compressed sqlite archive %s: %w", gzipPath, err)
	}
	defer func() {
		_ = file.Close()
	}()

	emitProgress(opts, Event{Kind: EventStageStart, Stage: "upload", Message: "Uploading SQLite archive", Total: gzipInfo.Size()})
	reader := &progressReader{
		reader: file,
		total:  gzipInfo.Size(),
		onRead: func(current int64, total int64) {
			emitProgress(opts, Event{Kind: EventTransferProgress, Stage: "upload", Message: "Uploading SQLite archive", Current: current, Total: total})
		},
	}
	etag, err := store.PutObject(ctx, key, reader, "application/gzip", gzipInfo.Size())
	if err != nil {
		return ArchiveResult{}, fmt.Errorf("upload sqlite archive %s: %w", key, err)
	}
	emitProgress(opts, Event{Kind: EventStageDone, Stage: "upload", Message: fmt.Sprintf("Uploaded %s", key), Current: gzipInfo.Size(), Total: gzipInfo.Size()})

	return ArchiveResult{
		Key:          key,
		LocalDBPath:  cfg.DBPath,
		SnapshotSize: snapshotInfo.Size(),
		ArchiveSize:  gzipInfo.Size(),
		ETag:         strings.TrimSpace(etag),
		ArchivedAt:   now,
	}, nil
}

func Latest(ctx context.Context, opts Options) (RestorePlan, error) {
	store, err := requireStore(opts)
	if err != nil {
		return RestorePlan{}, err
	}
	prefix := effectivePrefix(opts.Prefix)
	objects, err := store.ListObjects(ctx, prefix)
	if err != nil {
		return RestorePlan{}, fmt.Errorf("list sqlite archives under %s: %w", prefix, err)
	}
	var newest Object
	for _, obj := range objects {
		if !isSQLiteArchiveKey(obj.Key, prefix) {
			continue
		}
		if newest.Key == "" || objectNewer(obj, newest) {
			newest = obj
		}
	}
	if newest.Key == "" {
		return RestorePlan{}, fmt.Errorf("no sqlite archives found under %s", prefix)
	}
	return RestorePlan{Object: newest}, nil
}

func Restore(ctx context.Context, cfg config.Config, plan RestorePlan, opts Options) (RestoreResult, error) {
	store, err := requireStore(opts)
	if err != nil {
		return RestoreResult{}, err
	}
	if strings.TrimSpace(plan.Object.Key) == "" {
		return RestoreResult{}, fmt.Errorf("sqlite archive key is required")
	}
	now := optionNow(opts).UTC()
	workDir, err := cfg.MkdirTemp("sqlite-restore-*")
	if err != nil {
		return RestoreResult{}, err
	}
	defer func() {
		_ = os.RemoveAll(workDir)
	}()

	body, err := store.GetObject(ctx, plan.Object.Key)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("download sqlite archive %s: %w", plan.Object.Key, err)
	}
	defer func() {
		_ = body.Close()
	}()

	archiveTemp := filepath.Join(workDir, "archive.db.gz")
	emitProgress(opts, Event{Kind: EventStageStart, Stage: "download", Message: "Downloading SQLite archive", Total: plan.Object.Size})
	if err := copyToFile(&progressReader{
		reader: body,
		total:  plan.Object.Size,
		onRead: func(current int64, total int64) {
			emitProgress(opts, Event{Kind: EventTransferProgress, Stage: "download", Message: "Downloading SQLite archive", Current: current, Total: total})
		},
	}, archiveTemp); err != nil {
		return RestoreResult{}, fmt.Errorf("download sqlite archive %s: %w", plan.Object.Key, err)
	}
	downloadedInfo, err := os.Stat(archiveTemp)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("stat downloaded sqlite archive %s: %w", archiveTemp, err)
	}
	emitProgress(opts, Event{Kind: EventStageDone, Stage: "download", Message: fmt.Sprintf("Downloaded archive (%d bytes)", downloadedInfo.Size()), Current: downloadedInfo.Size(), Total: downloadedInfo.Size()})

	restoredTemp := filepath.Join(workDir, "brain.db")
	emitProgress(opts, Event{Kind: EventStageStart, Stage: "decompress", Message: "Decompressing SQLite archive"})
	archiveFile, err := os.Open(archiveTemp)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("open downloaded sqlite archive %s: %w", archiveTemp, err)
	}
	if err := gunzipToFile(archiveFile, restoredTemp); err != nil {
		_ = archiveFile.Close()
		return RestoreResult{}, fmt.Errorf("decompress sqlite archive %s: %w", plan.Object.Key, err)
	}
	if err := archiveFile.Close(); err != nil {
		return RestoreResult{}, fmt.Errorf("close downloaded sqlite archive %s: %w", archiveTemp, err)
	}
	emitProgress(opts, Event{Kind: EventStageDone, Stage: "decompress", Message: "Decompressed SQLite database"})

	emitProgress(opts, Event{Kind: EventStageStart, Stage: "validate", Message: "Validating restored SQLite database"})
	if err := validateSQLite(ctx, restoredTemp); err != nil {
		return RestoreResult{}, fmt.Errorf("validate sqlite archive %s: %w", plan.Object.Key, err)
	}
	emitProgress(opts, Event{Kind: EventStageDone, Stage: "validate", Message: "SQLite quick_check passed"})

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return RestoreResult{}, fmt.Errorf("create sqlite data dir %s: %w", filepath.Dir(cfg.DBPath), err)
	}
	emitProgress(opts, Event{Kind: EventStageStart, Stage: "install", Message: "Installing restored SQLite database"})
	backupPaths, err := moveExistingSQLiteFiles(cfg.DBPath, now)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := os.Rename(restoredTemp, cfg.DBPath); err != nil {
		return RestoreResult{}, fmt.Errorf("install restored sqlite database %s: %w", cfg.DBPath, err)
	}
	emitProgress(opts, Event{Kind: EventStageDone, Stage: "install", Message: "Installed restored SQLite database"})

	return RestoreResult{
		Key:          plan.Object.Key,
		RestoredPath: cfg.DBPath,
		BackupPaths:  backupPaths,
		RestoredAt:   now,
	}, nil
}

func snapshotSQLite(ctx context.Context, dbPath string, snapshotPath string) error {
	db, err := sql.Open(sqliteDriverName, dbPath)
	if err != nil {
		return fmt.Errorf("open sqlite database %s: %w", dbPath, err)
	}
	defer func() {
		_ = db.Close()
	}()
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 60000;"); err != nil {
		return fmt.Errorf("set sqlite busy timeout: %w", err)
	}
	stmt := "VACUUM INTO " + sqliteStringLiteral(snapshotPath)
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("snapshot sqlite database %s: %w", dbPath, err)
	}
	return nil
}

func validateSQLite(ctx context.Context, dbPath string) error {
	db, err := sql.Open(sqliteDriverName, dbPath)
	if err != nil {
		return fmt.Errorf("open sqlite database %s: %w", dbPath, err)
	}
	defer func() {
		_ = db.Close()
	}()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check;").Scan(&result); err != nil {
		return fmt.Errorf("run quick_check: %w", err)
	}
	if strings.TrimSpace(strings.ToLower(result)) != "ok" {
		return fmt.Errorf("quick_check returned %q", result)
	}
	return nil
}

func gzipFile(srcPath string, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", srcPath, err)
	}
	defer func() {
		_ = src.Close()
	}()
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", dstPath, err)
	}
	defer func() {
		_ = dst.Close()
	}()
	gw := gzip.NewWriter(dst)
	if _, err := io.Copy(gw, src); err != nil {
		_ = gw.Close()
		return fmt.Errorf("compress %s: %w", srcPath, err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("finish gzip %s: %w", dstPath, err)
	}
	return nil
}

func gunzipToFile(src io.Reader, dstPath string) error {
	gr, err := gzip.NewReader(src)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer func() {
		_ = gr.Close()
	}()
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", dstPath, err)
	}
	defer func() {
		_ = dst.Close()
	}()
	if _, err := io.Copy(dst, gr); err != nil {
		return fmt.Errorf("write decompressed sqlite database %s: %w", dstPath, err)
	}
	return nil
}

func copyToFile(src io.Reader, dstPath string) error {
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", dstPath, err)
	}
	defer func() {
		_ = dst.Close()
	}()
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("write %s: %w", dstPath, err)
	}
	return nil
}

func moveExistingSQLiteFiles(dbPath string, now time.Time) ([]string, error) {
	suffix := ".pre-restore-" + now.UTC().Format(timestampLayout)
	var backups []string
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return backups, fmt.Errorf("stat existing sqlite file %s: %w", path, err)
		}
		backupPath := path + suffix
		if err := os.Rename(path, backupPath); err != nil {
			return backups, fmt.Errorf("move existing sqlite file %s to %s: %w", path, backupPath, err)
		}
		backups = append(backups, backupPath)
	}
	return backups, nil
}

func requireStore(opts Options) (ObjectStore, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("object store is required")
	}
	return opts.Store, nil
}

func optionNow(opts Options) time.Time {
	if opts.Now != nil {
		return opts.Now()
	}
	return time.Now()
}

func objectKey(prefix string, name string) string {
	prefix = normalizePrefix(prefix)
	name = strings.TrimLeft(filepath.ToSlash(strings.TrimSpace(name)), "/")
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

func normalizePrefix(prefix string) string {
	prefix = filepath.ToSlash(strings.TrimSpace(prefix))
	return strings.Trim(prefix, "/")
}

func effectivePrefix(prefix string) string {
	prefix = normalizePrefix(prefix)
	if prefix == "" {
		return DefaultPrefix
	}
	return prefix
}

func isSQLiteArchiveKey(key string, prefix string) bool {
	key = filepath.ToSlash(strings.TrimSpace(key))
	if prefix != "" && !strings.HasPrefix(key, prefix+"/") {
		return false
	}
	base := filepath.Base(key)
	return strings.HasPrefix(base, "brain-") && strings.HasSuffix(base, ".db.gz")
}

func objectNewer(a Object, b Object) bool {
	if !a.LastModified.Equal(b.LastModified) {
		return a.LastModified.After(b.LastModified)
	}
	return a.Key > b.Key
}

func sqliteStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

type progressReader struct {
	reader  io.Reader
	current int64
	total   int64
	onRead  func(current int64, total int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.current += int64(n)
		if r.onRead != nil {
			r.onRead(r.current, r.total)
		}
	}
	return n, err
}

func emitProgress(opts Options, event Event) {
	if opts.Progress != nil {
		opts.Progress(event)
	}
}
