package sqlitearchive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/config"
)

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
