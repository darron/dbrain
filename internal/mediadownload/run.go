package mediadownload

import (
	"context"
	"log/slog"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/store"
)

type Options struct {
	Force bool
	// MediaNamespace controls the vault media namespace. Existing callers default
	// to x; direct Bluesky imports use bsky.
	MediaNamespace string
	// AllowedAssetIDs limits this run to the listed media asset IDs. Empty keeps
	// the existing item-wide behavior used by normal download workflows.
	AllowedAssetIDs  []int64
	Timeout          time.Duration
	ProgressInterval time.Duration
	ProgressBytes    int64
	Logger           *slog.Logger
	HTTPPolicy       *safehttp.Policy
	httpPolicy       *safehttp.Policy
}

const (
	DefaultTimeout          = 30 * time.Minute
	DefaultProgressInterval = 5 * time.Second
	DefaultProgressBytes    = 32 * 1024 * 1024
)

type Stats struct {
	Candidates int `json:"candidates"`
	Requested  int `json:"requested"`
	Downloaded int `json:"downloaded"`
	Gone       int `json:"gone"`
	Errors     int `json:"errors"`
	Blocked    int `json:"blocked"`
	Changed    int `json:"changed"`
}

func RunForItem(ctx context.Context, cfg config.Config, st *store.Store, itemID int64, opts Options) (Stats, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.ProgressInterval <= 0 {
		opts.ProgressInterval = DefaultProgressInterval
	}
	if opts.ProgressBytes <= 0 {
		opts.ProgressBytes = DefaultProgressBytes
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		return Stats{}, err
	}
	if len(opts.AllowedAssetIDs) > 0 {
		allowed := make(map[int64]struct{}, len(opts.AllowedAssetIDs))
		for _, assetID := range opts.AllowedAssetIDs {
			allowed[assetID] = struct{}{}
		}
		filtered := make([]model.ItemMediaRef, 0, len(refs))
		for _, ref := range refs {
			if _, ok := allowed[ref.MediaAssetID]; ok {
				filtered = append(filtered, ref)
			}
		}
		refs = filtered
	}

	stats := Stats{Candidates: len(refs)}
	if len(refs) == 0 {
		return stats, nil
	}

	policy := safehttp.Policy{}
	if opts.HTTPPolicy != nil {
		policy = *opts.HTTPPolicy
	} else if opts.httpPolicy != nil {
		policy = *opts.httpPolicy
	}
	policy.Timeout = opts.Timeout
	policy.AllowedPrivateOrigins = nil
	client := safehttp.NewClient(policy)
	namespace := normalizedMediaNamespace(opts.MediaNamespace)
	for _, ref := range refs {
		if !shouldDownload(ref, cfg, opts.Force) {
			continue
		}

		stats.Requested++
		result, err := downloadRef(ctx, client, cfg, ref, namespace, progressOptions{
			Logger:   opts.Logger,
			Interval: opts.ProgressInterval,
			Bytes:    opts.ProgressBytes,
		})
		if err != nil {
			return stats, err
		}
		result.AttemptedAt = time.Now().UTC()
		result = applyRetryPolicy(ref, result)

		changed, err := st.SaveMediaDownload(ctx, ref.MediaAssetID, result)
		if err != nil {
			return stats, err
		}
		if changed {
			stats.Changed++
		}

		switch result.Status {
		case model.MediaDownloadStatusDownloaded:
			stats.Downloaded++
		case model.MediaDownloadStatusGone:
			stats.Gone++
		case model.MediaDownloadStatusError:
			stats.Errors++
		case model.MediaDownloadStatusBlocked:
			stats.Blocked++
		}

		debugLog(opts.Logger, "x media download result",
			"item_id", itemID,
			"media_asset_id", ref.MediaAssetID,
			"remote_url", ref.RemoteURL,
			"status", result.Status,
			"local_path", result.LocalPath,
			"error", result.Error,
		)
	}

	return stats, nil
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}
