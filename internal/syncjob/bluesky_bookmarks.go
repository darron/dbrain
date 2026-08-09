package syncjob

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/bskyapi"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func executeBlueskyBookmarksStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (*BlueskyBookmarksStage, error) {
	common := opts.Common
	stageOpts := opts.BlueskyBookmarks
	progressf(common.Progress, "==> import bluesky-bookmarks\n")
	start := time.Now()
	bookmarkStats, err := runBlueskyBookmarkImport(ctx, cfg, st, bskyapi.BookmarkOptions{
		Browser: common.Browser,
		Profile: common.Profile,
		Limit:   stageOpts.Limit,
		Timeout: stageOpts.Timeout,
	})
	stage := &BlueskyBookmarksStage{Duration: time.Since(start), Stats: bookmarkStats}
	if err != nil {
		return stage, fmt.Errorf("import Bluesky bookmarks: %w", err)
	}
	progressf(common.Progress, "Bluesky bookmarks import complete: pages=%d seen=%d processed=%d skipped=%d blocked=%d not_found=%d unsupported=%d malformed=%d created=%d updated=%d unchanged=%d rendered=%d media_discovered=%d media_linked=%d media_unavailable=%d media_downloaded=%d media_gone=%d media_errors=%d media_blocked=%d quote_linked=%d quote_skipped=%d quote_blocked=%d quote_not_found=%d quote_detached=%d quote_unsupported=%d quote_malformed=%d stopped=%s (%s)\n", bookmarkStats.PagesFetched, bookmarkStats.Seen, bookmarkStats.Processed, bookmarkStats.Skipped, bookmarkStats.SkippedBlocked, bookmarkStats.SkippedNotFound, bookmarkStats.SkippedUnsupported, bookmarkStats.SkippedMalformed, bookmarkStats.Created, bookmarkStats.Updated, bookmarkStats.Unchanged, bookmarkStats.Rendered, bookmarkStats.MediaDiscovered, bookmarkStats.MediaLinked, bookmarkStats.MediaUnavailable, bookmarkStats.MediaDownloaded, bookmarkStats.MediaGone, bookmarkStats.MediaErrors, bookmarkStats.MediaBlocked, bookmarkStats.QuoteLinked, bookmarkStats.QuoteSkipped, bookmarkStats.QuoteSkippedBlocked, bookmarkStats.QuoteSkippedNotFound, bookmarkStats.QuoteSkippedDetached, bookmarkStats.QuoteSkippedUnsupported, bookmarkStats.QuoteSkippedMalformed, bookmarkStats.StoppedReason, stage.Duration)
	return stage, nil
}
