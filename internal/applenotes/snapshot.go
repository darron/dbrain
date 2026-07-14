package applenotes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"

	_ "modernc.org/sqlite"
)

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
	return CreateSnapshotContext(context.Background(), cfg, opts)
}

// CreateSnapshotContext copies the live Notes SQLite triplet into a private
// snapshot and permits cancellation between copy chunks.
func CreateSnapshotContext(ctx context.Context, cfg config.Config, opts Options) (SnapshotInfo, snapshotCleanup, error) {
	if err := ctx.Err(); err != nil {
		return SnapshotInfo{}, nil, err
	}
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
		if err := ctx.Err(); err != nil {
			return info, cleanupForSnapshot(snapshotDir, keep), err
		}
		if _, err := os.Stat(source); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return info, cleanupForSnapshot(snapshotDir, keep), appleNotesSourcePermissionError(source, fmt.Errorf("stat notes snapshot source %s: %w", source, err))
		}
		dest := filepath.Join(snapshotDir, filepath.Base(source))
		if err := copyRegularFileContext(ctx, source, dest); err != nil {
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

func PlatformSupported() bool {
	return runtime.GOOS == "darwin"
}
