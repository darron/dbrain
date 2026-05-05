package mediadownload

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

type Options struct {
	Force   bool
	Timeout time.Duration
	Logger  *slog.Logger
}

type Stats struct {
	Candidates int `json:"candidates"`
	Requested  int `json:"requested"`
	Downloaded int `json:"downloaded"`
	Gone       int `json:"gone"`
	Errors     int `json:"errors"`
	Changed    int `json:"changed"`
}

func RunForItem(ctx context.Context, cfg config.Config, st *store.Store, itemID int64, opts Options) (Stats, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{Candidates: len(refs)}
	if len(refs) == 0 {
		return stats, nil
	}

	client := &http.Client{Timeout: opts.Timeout}
	for _, ref := range refs {
		if !shouldDownload(ref, cfg, opts.Force) {
			continue
		}

		stats.Requested++
		result, err := downloadRef(ctx, client, cfg, ref)
		if err != nil {
			return stats, err
		}

		changed, err := st.SaveMediaDownload(ctx, ref.MediaAssetID, result)
		if err != nil {
			return stats, err
		}
		if changed {
			stats.Changed++
		}

		switch result.Status {
		case "downloaded":
			stats.Downloaded++
		case "gone":
			stats.Gone++
		case "error":
			stats.Errors++
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
