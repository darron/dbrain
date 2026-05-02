package safaritabs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"

	_ "modernc.org/sqlite"
)

const defaultCloudTabsRelPath = "Library/Containers/com.apple.Safari/Data/Library/Safari/CloudTabs.db"

type Options struct {
	DBPath       string
	SnapshotDir  string
	KeepSnapshot bool
	Device       string
	Limit        int
	OlderThan    time.Duration
	DryRun       bool
	ShowTitles   bool
	Force        bool
	Progress     ProgressFunc
}

type SnapshotInfo struct {
	SourceDBPath string   `json:"source_db_path"`
	Dir          string   `json:"dir"`
	DBPath       string   `json:"db_path"`
	CopiedFiles  []string `json:"copied_files"`
	Kept         bool     `json:"kept"`
}

type snapshotCleanup func() error

func createSnapshot(cfg config.Config, opts Options) (SnapshotInfo, snapshotCleanup, error) {
	sourcePath, err := resolveCloudTabsDBPath(opts.DBPath)
	if err != nil {
		return SnapshotInfo{}, nil, err
	}
	if strings.TrimSpace(sourcePath) == "" {
		return SnapshotInfo{}, nil, fmt.Errorf("resolve Safari CloudTabs database path: empty path")
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return SnapshotInfo{}, nil, safariTabsSourcePermissionError(sourcePath, fmt.Errorf("access Safari CloudTabs database %s: %w", sourcePath, err))
	}

	keep := opts.KeepSnapshot || strings.TrimSpace(opts.SnapshotDir) != ""
	snapshotDir := strings.TrimSpace(opts.SnapshotDir)
	if snapshotDir == "" {
		snapshotDir, err = cfg.MkdirTemp("safari-tabs-snapshot-*")
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

	for _, source := range sqliteTripletPaths(sourcePath) {
		if _, err := os.Stat(source); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return info, cleanupForSnapshot(snapshotDir, keep), safariTabsSourcePermissionError(source, fmt.Errorf("stat Safari snapshot source %s: %w", source, err))
		}
		dest := filepath.Join(snapshotDir, filepath.Base(source))
		if err := copyRegularFile(source, dest); err != nil {
			return info, cleanupForSnapshot(snapshotDir, keep), err
		}
		if sameFile(source, dest) {
			return info, cleanupForSnapshot(snapshotDir, keep), fmt.Errorf("snapshot file %s aliases live Safari file %s", dest, source)
		}
		info.CopiedFiles = append(info.CopiedFiles, dest)
	}
	if len(info.CopiedFiles) == 0 {
		return info, cleanupForSnapshot(snapshotDir, keep), fmt.Errorf("no Safari CloudTabs files copied from %s", sourcePath)
	}
	return info, cleanupForSnapshot(snapshotDir, keep), nil
}

func resolveCloudTabsDBPath(override string) (string, error) {
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
	return filepath.Join(home, defaultCloudTabsRelPath), nil
}

func sqliteTripletPaths(dbPath string) []string {
	return []string{dbPath, dbPath + "-wal", dbPath + "-shm"}
}

func copyRegularFile(source string, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return safariTabsSourcePermissionError(source, fmt.Errorf("open snapshot source %s: %w", source, err))
	}
	defer func() {
		_ = in.Close()
	}()

	info, err := in.Stat()
	if err != nil {
		return safariTabsSourcePermissionError(source, fmt.Errorf("stat snapshot source %s: %w", source, err))
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

func safariTabsSourcePermissionError(source string, err error) error {
	if err == nil || !errors.Is(err, os.ErrPermission) {
		return err
	}
	return fmt.Errorf("%w\n\nSafari tab import could not read the Safari CloudTabs SQLite store because macOS denied access.\nGrant Full Disk Access to the app or executable macOS associates with this run, then quit and reopen it before retrying.\nPath: System Settings > Privacy & Security > Full Disk Access\nTry adding the dbrain binary first if macOS allows it. If access still fails, grant the parent terminal/IDE instead, such as Terminal, iTerm2, Ghostty, Warp, VS Code, or Codex. Local rebuilds may invalidate binary-specific grants.\nSource: %s", err, source)
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
		return nil, fmt.Errorf("open Safari CloudTabs snapshot: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func validateSnapshotDB(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{"cloud_tab_devices", "cloud_tabs"} {
		var found int
		err := db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ? LIMIT 1`, table).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("safari CloudTabs snapshot missing required table %s", table)
		}
		if err != nil {
			return fmt.Errorf("check Safari CloudTabs table %s: %w", table, err)
		}
	}
	return nil
}
