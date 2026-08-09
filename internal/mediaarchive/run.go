package mediaarchive

import (
	"context"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	var err error
	opts, err = normalizeOptions(opts)
	if err != nil {
		return Stats{}, err
	}

	assets, err := st.ListMediaAssetsForArchive(ctx, opts.Limit, opts.Force)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{Candidates: len(assets)}
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
	}

	if !opts.PruneLocal {
		return stats, nil
	}

	pruneAssets, err := st.ListMediaAssetsForPrune(ctx, opts.Limit)
	if err != nil {
		return stats, err
	}
	prunedPaths := map[string]struct{}{}
	for _, asset := range pruneAssets {
		if err := ctx.Err(); err != nil {
			return stats, err
		}

		if strings.TrimSpace(asset.LocalPath) == "" {
			continue
		}
		if _, seen := prunedPaths[asset.LocalPath]; seen {
			continue
		}
		prunedPaths[asset.LocalPath] = struct{}{}

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

func normalizeOptions(opts Options) (Options, error) {
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
		return Options{}, fmt.Errorf("archive bucket is required")
	}
	if opts.Upload {
		if strings.TrimSpace(opts.Endpoint) == "" {
			return Options{}, fmt.Errorf("archive endpoint is required when upload is enabled")
		}
		if strings.TrimSpace(opts.AccessKeyID) == "" || strings.TrimSpace(opts.SecretKey) == "" {
			return Options{}, fmt.Errorf("archive credentials are required when upload is enabled")
		}
		if opts.Uploader == nil {
			uploader, err := NewS3Uploader(opts)
			if err != nil {
				return Options{}, err
			}
			opts.Uploader = uploader
		}
	}
	return opts, nil
}
