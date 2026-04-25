package mediaarchive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
	"dbrain/internal/vault"
)

const defaultProvider = "cloudflare_r2"

type Options struct {
	Limit         int
	Force         bool
	Upload        bool
	PruneLocal    bool
	Provider      string
	Bucket        string
	PublicBaseURL string
	Endpoint      string
	Region        string
	AccessKeyID   string
	SecretKey     string
	SessionToken  string
	PathStyle     bool
	Uploader      Uploader
	Logger        *slog.Logger
}

type Stats struct {
	Candidates       int   `json:"candidates"`
	Uploaded         int   `json:"uploaded"`
	Archived         int   `json:"archived"`
	Unchanged        int   `json:"unchanged"`
	PruneSkipped     int   `json:"prune_skipped"`
	LocalFilesPruned int   `json:"local_files_pruned"`
	LocalRowsPruned  int64 `json:"local_rows_pruned"`
	Errors           int   `json:"errors"`
}

type Uploader interface {
	Upload(ctx context.Context, cfg config.Config, asset model.MediaAsset, opts Options) (model.MediaArchiveResult, bool, error)
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if opts.Limit <= 0 {
		opts.Limit = 5000
	}
	if strings.TrimSpace(opts.Provider) == "" {
		opts.Provider = defaultProvider
	}
	if strings.TrimSpace(opts.Region) == "" {
		opts.Region = "auto"
	}
	if strings.TrimSpace(opts.Bucket) == "" {
		return Stats{}, fmt.Errorf("archive bucket is required")
	}
	if opts.Upload {
		if strings.TrimSpace(opts.Endpoint) == "" {
			return Stats{}, fmt.Errorf("archive endpoint is required when upload is enabled")
		}
		if strings.TrimSpace(opts.AccessKeyID) == "" || strings.TrimSpace(opts.SecretKey) == "" {
			return Stats{}, fmt.Errorf("archive credentials are required when upload is enabled")
		}
		if opts.Uploader == nil {
			uploader, err := NewS3Uploader(opts)
			if err != nil {
				return Stats{}, err
			}
			opts.Uploader = uploader
		}
	}

	selectionForce := opts.Force || opts.PruneLocal
	assets, err := st.ListMediaAssetsForArchive(ctx, opts.Limit, selectionForce)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{Candidates: len(assets)}
	prunedPaths := map[string]struct{}{}
	for _, asset := range assets {
		if err := ctx.Err(); err != nil {
			return stats, err
		}

		result := archiveResultForAsset(asset, opts)
		if opts.Upload {
			uploadedResult, uploaded, err := opts.Uploader.Upload(ctx, cfg, asset, opts)
			if err != nil {
				stats.Errors++
				debugLog(opts.Logger, "media archive upload failed", "asset_id", asset.ID, "local_path", asset.LocalPath, "error", err.Error())
				continue
			}
			result = uploadedResult
			if uploaded {
				stats.Uploaded++
			}
		}
		changed, err := st.SaveMediaArchive(ctx, asset.ID, result)
		if err != nil {
			stats.Errors++
			debugLog(opts.Logger, "media archive state save failed", "asset_id", asset.ID, "local_path", asset.LocalPath, "error", err.Error())
			continue
		}
		if changed {
			stats.Archived++
		} else {
			stats.Unchanged++
		}

		debugLog(opts.Logger, "media archive marked", "asset_id", asset.ID, "local_path", asset.LocalPath, "archive_key", result.Key, "archive_url", result.URL)

		if !opts.PruneLocal || asset.LocalPath == "" {
			continue
		}
		if _, seen := prunedPaths[asset.LocalPath]; seen {
			continue
		}

		pruned, rows, err := pruneLocalPathIfSafe(ctx, cfg, st, asset.LocalPath, opts.Logger)
		if err != nil {
			stats.Errors++
			debugLog(opts.Logger, "media local prune failed", "local_path", asset.LocalPath, "error", err.Error())
			continue
		}
		if !pruned {
			stats.PruneSkipped++
			continue
		}
		prunedPaths[asset.LocalPath] = struct{}{}
		stats.LocalFilesPruned++
		stats.LocalRowsPruned += rows
		debugLog(opts.Logger, "media local file pruned", "local_path", asset.LocalPath, "rows_marked", rows)
		if err := refreshNotesForLocalPath(ctx, cfg, st, asset.LocalPath, opts.Logger); err != nil {
			stats.Errors++
			debugLog(opts.Logger, "media prune note refresh failed", "local_path", asset.LocalPath, "error", err.Error())
		}
	}

	return stats, nil
}

func archiveResultForAsset(asset model.MediaAsset, opts Options) model.MediaArchiveResult {
	archivedAt := time.Now().UTC()
	if asset.ArchiveStatus == "archived" && !asset.ArchivedAt.IsZero() {
		archivedAt = asset.ArchivedAt
	}
	key := filepath.ToSlash(strings.TrimSpace(asset.LocalPath))
	url := buildArchiveURL(strings.TrimSpace(opts.PublicBaseURL), key)
	if url == "" {
		url = strings.TrimSpace(asset.ArchiveURL)
	}
	return model.MediaArchiveResult{
		Provider:   strings.TrimSpace(opts.Provider),
		Bucket:     strings.TrimSpace(opts.Bucket),
		Key:        key,
		URL:        url,
		Status:     "archived",
		Error:      "",
		ArchivedAt: archivedAt,
	}
}

func buildArchiveURL(baseURL, key string) string {
	baseURL = strings.TrimSpace(baseURL)
	key = strings.TrimLeft(filepath.ToSlash(strings.TrimSpace(key)), "/")
	if baseURL == "" || key == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/" + key
}

func pruneLocalPathIfSafe(ctx context.Context, cfg config.Config, st *store.Store, localPath string, logger *slog.Logger) (bool, int64, error) {
	assets, err := st.ListMediaAssetsByLocalPath(ctx, localPath)
	if err != nil {
		return false, 0, err
	}
	if len(assets) == 0 {
		return false, 0, nil
	}
	for _, asset := range assets {
		if strings.TrimSpace(asset.ArchiveStatus) != "archived" {
			debugLog(logger, "media local prune deferred", "local_path", localPath, "blocking_asset_id", asset.ID, "archive_status", asset.ArchiveStatus)
			return false, 0, nil
		}
	}

	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(localPath))
	if err := os.Remove(fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, 0, fmt.Errorf("remove local media %s: %w", fullPath, err)
	}

	rows, err := st.MarkMediaLocalPrunedByPath(ctx, localPath, time.Now().UTC())
	if err != nil {
		return false, 0, err
	}
	return true, rows, nil
}

func refreshNotesForLocalPath(ctx context.Context, cfg config.Config, st *store.Store, localPath string, logger *slog.Logger) error {
	sourceKeys, err := st.ListItemSourceKeysByMediaLocalPath(ctx, localPath)
	if err != nil {
		return err
	}
	for _, sourceKey := range sourceKeys {
		item, err := st.GetItem(ctx, sourceKey)
		if err != nil {
			return fmt.Errorf("load item %s for media note refresh: %w", sourceKey, err)
		}
		if item.NotePath == "" {
			continue
		}
		if err := vault.WriteItem(cfg, item); err != nil {
			return fmt.Errorf("write note %s for media note refresh: %w", item.NotePath, err)
		}
		debugLog(logger, "media prune note refreshed", "source_key", sourceKey, "note_path", item.NotePath, "local_path", localPath)
	}
	return nil
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}
