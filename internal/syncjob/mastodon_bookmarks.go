package syncjob

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mastodonapi"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/store"
)

func executeMastodonBookmarksStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (*MastodonBookmarksStage, error) {
	common := opts.Common
	stageOpts := opts.MastodonBookmarks
	progressf(common.Progress, "==> import mastodon-bookmarks\n")
	start := time.Now()
	stage := &MastodonBookmarksStage{}
	raw, ok := runtimeenv.ConfigMap(cfg.RootDir, "mastodon")
	if !ok {
		stage.Duration = time.Since(start)
		return stage, fmt.Errorf("mastodon configuration is missing")
	}
	mastodonConfig, err := mastodonapi.ParseConfig(raw)
	if err != nil {
		stage.Duration = time.Since(start)
		return stage, err
	}
	if !mastodonConfig.Enabled {
		stage.Duration = time.Since(start)
		return stage, fmt.Errorf("mastodon is disabled in configuration")
	}
	accountCount := 0
	var stageErr error
	for _, account := range mastodonConfig.Accounts {
		if !account.Enabled {
			continue
		}
		accountCount++
		accessToken, resolveErr := mastodonapi.ResolveTypedSecretRef(ctx, account.AccessTokenRef)
		if resolveErr != nil {
			stage.Accounts = append(stage.Accounts, mastodonapi.BookmarkStats{AccountKey: account.Key, Origin: account.Origin, StoppedReason: "account error"})
			stageErr = errors.Join(stageErr, fmt.Errorf("resolve Mastodon access token for %q: %w", account.Key, resolveErr))
			continue
		}
		client, clientErr := mastodonapi.NewClient(account.Origin, accessToken, nil)
		if clientErr != nil {
			stage.Accounts = append(stage.Accounts, mastodonapi.BookmarkStats{AccountKey: account.Key, Origin: account.Origin, StoppedReason: "account error"})
			stageErr = errors.Join(stageErr, fmt.Errorf("create Mastodon client for %q: %w", account.Key, clientErr))
			continue
		}
		bookmarkStats, importErr := mastodonapi.RunBookmarksWithClient(ctx, cfg, st, client, mastodonapi.BookmarkOptions{
			AccountKey: account.Key,
			Limit:      stageOpts.Limit,
			Timeout:    stageOpts.Timeout,
			Force:      common.Force,
		})
		stage.Accounts = append(stage.Accounts, bookmarkStats)
		addMastodonBookmarkStats(&stage.Stats, bookmarkStats)
		if importErr != nil {
			stageErr = errors.Join(stageErr, fmt.Errorf("import Mastodon account %q: %w", account.Key, importErr))
			continue
		}
		progressf(common.Progress, "Mastodon account %s complete: pages=%d processed=%d created=%d updated=%d unchanged=%d media=%d unavailable=%d api_errors=%d rate_limits=%d retries=%d (%s)\n", account.Key, bookmarkStats.PagesFetched, bookmarkStats.Processed, bookmarkStats.Created, bookmarkStats.Updated, bookmarkStats.Unchanged, bookmarkStats.MediaDownloaded, bookmarkStats.MediaUnavailable, bookmarkStats.APIErrors, bookmarkStats.RateLimits, bookmarkStats.Retries, time.Since(start))
	}
	if accountCount == 0 {
		stage.Duration = time.Since(start)
		return stage, fmt.Errorf("mastodon has no enabled accounts")
	}
	if stageErr != nil {
		stage.Duration = time.Since(start)
		return stage, stageErr
	}
	if len(stage.Accounts) > 1 {
		stage.Stats.AccountKey = "multiple"
		stage.Stats.Origin = ""
		stage.Stats.VerifiedAccountID = ""
		stage.Stats.Handle = ""
	}
	stage.Stats.StoppedReason = "all accounts complete"
	stage.Duration = time.Since(start)
	return stage, nil
}

func addMastodonBookmarkStats(dst *mastodonapi.BookmarkStats, src mastodonapi.BookmarkStats) {
	if dst.AccountKey == "" {
		*dst = src
		return
	}
	dst.PagesFetched += src.PagesFetched
	dst.Seen += src.Seen
	dst.Processed += src.Processed
	dst.Skipped += src.Skipped
	dst.SkippedUnsupported += src.SkippedUnsupported
	dst.SkippedMalformed += src.SkippedMalformed
	dst.Created += src.Created
	dst.Updated += src.Updated
	dst.Unchanged += src.Unchanged
	dst.Rendered += src.Rendered
	dst.MediaDiscovered += src.MediaDiscovered
	dst.MediaLinked += src.MediaLinked
	dst.MediaUnavailable += src.MediaUnavailable
	dst.MediaDownloaded += src.MediaDownloaded
	dst.MediaGone += src.MediaGone
	dst.MediaErrors += src.MediaErrors
	dst.MediaBlocked += src.MediaBlocked
	dst.APIErrors += src.APIErrors
	dst.RateLimits += src.RateLimits
	dst.Retries += src.Retries
}
