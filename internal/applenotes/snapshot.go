package applenotes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"

	_ "modernc.org/sqlite"
)

const (
	defaultNotesRelPath = "Library/Group Containers/group.com.apple.notes/NoteStore.sqlite"
)

type Options struct {
	DBPath             string
	SnapshotDir        string
	KeepSnapshot       bool
	Limit              int
	DryRun             bool
	ShowTitles         bool
	Force              bool
	SkipAttachments    bool
	SkipAttachmentOCR  bool
	AttachmentMaxBytes int64
	TesseractBinary    string
	ExcludeFolders     []string
	ExcludeAccounts    []string
	ExcludeShared      bool
	IncludeLocked      bool
	ForgetExcluded     bool
	Summarize          bool
	SummaryModel       string
	SummaryCLI         string
	SummaryLength      string
	Timeout            time.Duration
	Progress           ProgressFunc
}

type ProgressFunc func(ProgressEvent)

type ProgressEvent struct {
	Phase           string `json:"phase"`
	Index           int    `json:"index,omitempty"`
	Total           int    `json:"total,omitempty"`
	SourceKey       string `json:"source_key,omitempty"`
	Title           string `json:"title,omitempty"`
	Status          string `json:"status,omitempty"`
	Reason          string `json:"reason,omitempty"`
	Links           int    `json:"links,omitempty"`
	Attachments     int    `json:"attachments,omitempty"`
	TextChars       int    `json:"text_chars,omitempty"`
	AttachmentChars int    `json:"attachment_chars,omitempty"`
	Rendered        bool   `json:"rendered,omitempty"`
	SummaryStatus   string `json:"summary_status,omitempty"`
}

type SnapshotInfo struct {
	SourceDBPath string   `json:"source_db_path"`
	Dir          string   `json:"dir"`
	DBPath       string   `json:"db_path"`
	CopiedFiles  []string `json:"copied_files"`
	Kept         bool     `json:"kept"`
}

type ProbeStats struct {
	SourceDBPath string                `json:"source_db_path"`
	Snapshot     SnapshotInfo          `json:"snapshot"`
	Tables       map[string]TableProbe `json:"tables"`
	NoteCount    int                   `json:"note_count"`
	AccountCount int                   `json:"account_count"`
	FolderCount  int                   `json:"folder_count"`
	Warnings     []string              `json:"warnings,omitempty"`
	Duration     time.Duration         `json:"duration"`
}

type TableProbe struct {
	Exists  bool     `json:"exists"`
	Columns []string `json:"columns,omitempty"`
	Rows    int      `json:"rows,omitempty"`
}

type snapshotCleanup func() error

func Probe(ctx context.Context, cfg config.Config, opts Options) (ProbeStats, error) {
	start := time.Now()
	info, cleanup, err := CreateSnapshot(cfg, opts)
	if err != nil {
		return ProbeStats{}, err
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	db, err := openSnapshotDB(info.DBPath)
	if err != nil {
		return ProbeStats{}, err
	}
	defer func() {
		_ = db.Close()
	}()

	if err := validateSnapshotDB(ctx, db); err != nil {
		return ProbeStats{}, err
	}

	stats := ProbeStats{
		SourceDBPath: info.SourceDBPath,
		Snapshot:     info,
		Tables:       map[string]TableProbe{},
	}
	for _, table := range []string{"ZICCLOUDSYNCINGOBJECT", "ZICNOTEDATA", "Z_METADATA", "Z_PRIMARYKEY"} {
		probe, err := probeTable(ctx, db, table)
		if err != nil {
			return stats, err
		}
		stats.Tables[table] = probe
	}

	stats.NoteCount = estimateEntityCount(ctx, db, stats.Tables["ZICCLOUDSYNCINGOBJECT"], "note")
	stats.FolderCount = estimateEntityCount(ctx, db, stats.Tables["ZICCLOUDSYNCINGOBJECT"], "folder")
	stats.AccountCount = estimateEntityCount(ctx, db, stats.Tables["ZICCLOUDSYNCINGOBJECT"], "account")
	if !stats.Tables["ZICCLOUDSYNCINGOBJECT"].Exists {
		stats.Warnings = append(stats.Warnings, "ZICCLOUDSYNCINGOBJECT table not found")
	}
	if !stats.Tables["ZICNOTEDATA"].Exists {
		stats.Warnings = append(stats.Warnings, "ZICNOTEDATA table not found")
	}
	stats.Duration = time.Since(start)
	return stats, nil
}

func CreateSnapshot(cfg config.Config, opts Options) (SnapshotInfo, snapshotCleanup, error) {
	sourcePath, err := resolveNotesDBPath(opts.DBPath)
	if err != nil {
		return SnapshotInfo{}, nil, err
	}
	if strings.TrimSpace(sourcePath) == "" {
		return SnapshotInfo{}, nil, fmt.Errorf("resolve Apple Notes database path: empty path")
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return SnapshotInfo{}, nil, appleNotesSourcePermissionError(sourcePath, fmt.Errorf("access Apple Notes database %s: %w", sourcePath, err))
	}

	keep := opts.KeepSnapshot || strings.TrimSpace(opts.SnapshotDir) != ""
	snapshotDir := strings.TrimSpace(opts.SnapshotDir)
	if snapshotDir == "" {
		snapshotDir, err = cfg.MkdirTemp("apple-notes-snapshot-*")
		if err != nil {
			return SnapshotInfo{}, nil, err
		}
	} else if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return SnapshotInfo{}, nil, fmt.Errorf("create snapshot dir %s: %w", snapshotDir, err)
	}

	info := SnapshotInfo{
		SourceDBPath: sourcePath,
		Dir:          snapshotDir,
		DBPath:       filepath.Join(snapshotDir, filepath.Base(sourcePath)),
		Kept:         keep,
	}

	for _, source := range notesTripletPaths(sourcePath) {
		if _, err := os.Stat(source); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return info, cleanupForSnapshot(snapshotDir, keep), appleNotesSourcePermissionError(source, fmt.Errorf("stat notes snapshot source %s: %w", source, err))
		}
		dest := filepath.Join(snapshotDir, filepath.Base(source))
		if err := copyRegularFile(source, dest); err != nil {
			return info, cleanupForSnapshot(snapshotDir, keep), err
		}
		if sameFile(source, dest) {
			return info, cleanupForSnapshot(snapshotDir, keep), fmt.Errorf("snapshot file %s aliases live Notes file %s", dest, source)
		}
		info.CopiedFiles = append(info.CopiedFiles, dest)
	}
	if len(info.CopiedFiles) == 0 {
		return info, cleanupForSnapshot(snapshotDir, keep), fmt.Errorf("no Apple Notes files copied from %s", sourcePath)
	}

	return info, cleanupForSnapshot(snapshotDir, keep), nil
}

func resolveNotesDBPath(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return filepath.Abs(strings.TrimSpace(override))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("resolve home directory: empty HOME")
	}
	return filepath.Join(home, defaultNotesRelPath), nil
}

func notesTripletPaths(dbPath string) []string {
	return []string{dbPath, dbPath + "-wal", dbPath + "-shm"}
}

func copyRegularFile(source string, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return appleNotesSourcePermissionError(source, fmt.Errorf("open snapshot source %s: %w", source, err))
	}
	defer func() {
		_ = in.Close()
	}()

	info, err := in.Stat()
	if err != nil {
		return appleNotesSourcePermissionError(source, fmt.Errorf("stat snapshot source %s: %w", source, err))
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("snapshot source %s is not a regular file", source)
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create snapshot file %s: %w", dest, err)
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy snapshot file %s to %s: %w", source, dest, err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync snapshot file %s: %w", dest, err)
	}
	return nil
}

func appleNotesSourcePermissionError(source string, err error) error {
	if err == nil || !errors.Is(err, os.ErrPermission) {
		return err
	}
	return fmt.Errorf("%w\n\nApple Notes import could not read the Notes SQLite store because macOS denied access.\nGrant Full Disk Access to the app or executable macOS associates with this run, then quit and reopen it before retrying.\nPath: System Settings > Privacy & Security > Full Disk Access\nTry adding the dbrain binary first if macOS allows it. If access still fails, grant the parent terminal/IDE instead, such as Terminal, iTerm2, Ghostty, Warp, VS Code, or Codex. Local rebuilds may invalidate binary-specific grants.\nSource: %s", err, source)
}

func sameFile(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}

func cleanupForSnapshot(dir string, keep bool) snapshotCleanup {
	return func() error {
		if keep || strings.TrimSpace(dir) == "" {
			return nil
		}
		return os.RemoveAll(dir)
	}
}

func openSnapshotDB(path string) (*sql.DB, error) {
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("mode", "ro")
	query.Set("_pragma", "query_only(1)")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("open Notes snapshot: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func validateSnapshotDB(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return fmt.Errorf("validate Notes snapshot: %w", err)
	}
	if strings.TrimSpace(strings.ToLower(result)) != "ok" {
		return fmt.Errorf("validate Notes snapshot: quick_check returned %q", result)
	}
	return nil
}

func probeTable(ctx context.Context, db *sql.DB, name string) (TableProbe, error) {
	var exists int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type IN ('table', 'virtual table') AND name = ? LIMIT 1`, name).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return TableProbe{}, nil
	}
	if err != nil {
		return TableProbe{}, fmt.Errorf("check Notes table %s: %w", name, err)
	}

	columns, err := tableColumns(ctx, db, name)
	if err != nil {
		return TableProbe{}, err
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+name+`"`).Scan(&rows); err != nil {
		return TableProbe{}, fmt.Errorf("count Notes table %s: %w", name, err)
	}
	return TableProbe{Exists: true, Columns: columns, Rows: rows}, nil
}

func tableColumns(ctx context.Context, db *sql.DB, name string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info("`+strings.ReplaceAll(name, `"`, `""`)+`")`)
	if err != nil {
		return nil, fmt.Errorf("load Notes table info %s: %w", name, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var columns []string
	for rows.Next() {
		var cid int
		var colName string
		var colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("scan Notes table info %s: %w", name, err)
		}
		columns = append(columns, colName)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Notes table info %s: %w", name, err)
	}
	return columns, nil
}

func estimateEntityCount(ctx context.Context, db *sql.DB, objectTable TableProbe, kind string) int {
	if !objectTable.Exists {
		return 0
	}
	column := firstColumn(objectTable.Columns, kindEntityTitleColumns(kind)...)
	if column == "" {
		if kind == "note" {
			if firstColumn(objectTable.Columns, "ZTITLE1", "ZSNIPPET", "ZNOTEDATA") != "" {
				var count int
				if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ZICCLOUDSYNCINGOBJECT WHERE COALESCE(ZMARKEDFORDELETION, 0) = 0`).Scan(&count); err == nil {
					return count
				}
			}
		}
		return 0
	}
	var count int
	query := fmt.Sprintf(`SELECT COUNT(*) FROM ZICCLOUDSYNCINGOBJECT WHERE %s IS NOT NULL`, quoteIdent(column))
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0
	}
	return count
}

func kindEntityTitleColumns(kind string) []string {
	switch kind {
	case "note":
		return []string{"ZTITLE1", "ZSNIPPET"}
	case "folder":
		return []string{"ZTITLE2"}
	case "account":
		return []string{"ZNAME"}
	default:
		return nil
	}
}

func firstColumn(columns []string, names ...string) string {
	for _, want := range names {
		for _, have := range columns {
			if strings.EqualFold(have, want) {
				return have
			}
		}
	}
	return ""
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func PlatformSupported() bool {
	return runtime.GOOS == "darwin"
}
