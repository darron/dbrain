package sqlitearchive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/config"
)

func Archive(ctx context.Context, cfg config.Config, opts Options) (ArchiveResult, error) {
	lease, ownedLease, err := operationLease(cfg, opts, "archive")
	if err != nil {
		return ArchiveResult{}, err
	}
	if ownedLease {
		defer func() { _ = lease.Close() }()
	}
	store, err := requireWriter(opts)
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
