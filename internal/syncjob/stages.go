package syncjob

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/feedimport"
	"github.com/darron/dbrain/internal/githubimport"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/worker"
	"github.com/darron/dbrain/internal/xmediatranscribe"
	"github.com/darron/dbrain/internal/xphotoocr"
	"github.com/darron/dbrain/internal/youtubeimport"
)

func executeXMediaStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (*XMediaStage, error) {
	common := opts.Common
	stageOpts := opts.XMedia
	xOpts := opts.X
	progressf(common.Progress, "==> transcribe x-media\n")
	start := time.Now()
	xMediaStats, err := runXMediaStage(ctx, cfg, st, xmediatranscribe.Options{
		Limit:         stageOpts.Limit,
		Force:         common.Force,
		Concurrency:   xOpts.Concurrency,
		Timeout:       common.Timeout,
		Summarize:     common.Summarize,
		SummaryModel:  common.Model,
		SummaryCLI:    common.CLI,
		SummaryLength: common.Length,
		Logger:        common.Logger,
	})
	stage := &XMediaStage{Duration: time.Since(start), Stats: xMediaStats}
	if err != nil {
		return stage, fmt.Errorf("transcribe x-media: %w", err)
	}
	progressf(common.Progress, "X media transcription complete (X + Bluesky): items_processed=%d items_updated=%d items_skipped=%d media_transcribed=%d items_summarized=%d errors=%d summary_errors=%d (%s)\n", xMediaStats.ItemsProcessed, xMediaStats.ItemsUpdated, xMediaStats.ItemsSkipped, xMediaStats.MediaTranscribed, xMediaStats.ItemsSummarized, xMediaStats.Errors, xMediaStats.SummaryErrors, stage.Duration)
	return stage, nil
}

func executeXPhotoOCRStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (*XPhotoOCRStage, error) {
	common := opts.Common
	stageOpts := opts.XPhotoOCR
	xOpts := opts.X
	progressf(common.Progress, "==> ocr x-photos\n")
	start := time.Now()
	ocrStats, err := runXPhotoOCRStage(ctx, cfg, st, xphotoocr.Options{
		Limit:       stageOpts.Limit,
		Force:       common.Force,
		Concurrency: xOpts.Concurrency,
		Timeout:     common.Timeout,
		Model:       stageOpts.Model,
		Logger:      common.Logger,
	})
	stage := &XPhotoOCRStage{Duration: time.Since(start), Stats: ocrStats}
	if err != nil {
		return stage, fmt.Errorf("ocr x-photos: %w", err)
	}
	progressf(common.Progress, "X photo OCR complete (X + Bluesky): items_processed=%d items_updated=%d items_skipped=%d photos_ocred=%d errors=%d (%s)\n", ocrStats.ItemsProcessed, ocrStats.ItemsUpdated, ocrStats.ItemsSkipped, ocrStats.PhotosOCRed, ocrStats.Errors, stage.Duration)
	return stage, nil
}

func executeGitHubStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (*GitHubStage, error) {
	common := opts.Common
	stageOpts := opts.GitHub
	progressf(common.Progress, "==> import github stars\n")
	start := time.Now()
	githubStats, err := runGitHubImport(ctx, cfg, st, githubimport.Options{
		Limit:     stageOpts.Limit,
		Force:     common.Force,
		Summarize: common.Summarize,
		Model:     common.Model,
		CLI:       common.CLI,
		Length:    common.Length,
		Timeout:   common.Timeout,
		Logger:    common.Logger,
	})
	stage := &GitHubStage{Duration: time.Since(start), Stats: githubStats}
	if err != nil {
		return stage, fmt.Errorf("import github stars: %w", err)
	}
	progressf(common.Progress, "GitHub stars complete: stars=%d items_created=%d sources_summarized=%d errors=%d (%s)\n", githubStats.StarsProcessed, githubStats.ItemsCreated, githubStats.SourcesSummarized, githubStats.Errors, stage.Duration)
	return stage, nil
}

func executeYouTubeStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (*YouTubeStage, error) {
	common := opts.Common
	stageOpts := opts.YouTube
	progressf(common.Progress, "==> import youtube\n")
	start := time.Now()
	youtubeStats, err := runYouTubeImport(ctx, cfg, st, youtubeimport.Options{
		Browser:    common.Browser,
		Profile:    common.Profile,
		Limit:      stageOpts.Limit,
		WatchLater: stageOpts.WatchLater,
		Liked:      stageOpts.Liked,
		Summarize:  common.Summarize,
		Force:      common.Force,
		Model:      common.Model,
		CLI:        common.CLI,
		Length:     common.Length,
		Timeout:    common.Timeout,
		Logger:     common.Logger,
	})
	stage := &YouTubeStage{Duration: time.Since(start), Stats: youtubeStats}
	if err != nil {
		return stage, fmt.Errorf("import youtube: %w", err)
	}
	progressf(common.Progress, "YouTube import complete: items_processed=%d sources_summarized=%d errors=%d (%s)\n", youtubeStats.ItemsProcessed, youtubeStats.SourcesSummarized, youtubeStats.Errors, stage.Duration)
	return stage, nil
}

func executeFeedsStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (*FeedsStage, error) {
	common := opts.Common
	stageOpts := opts.Feeds
	progressf(common.Progress, "==> import feeds\n")
	start := time.Now()
	feedStats, err := runFeedImport(ctx, cfg, st, feedimport.Options{
		Limit:               stageOpts.Limit,
		Force:               common.Force,
		AllowPrivateNetwork: feedAllowPrivateNetworkFromRuntime(cfg.RootDir),
		Logger:              common.Logger,
	})
	stage := &FeedsStage{Duration: time.Since(start), Stats: feedStats}
	if err != nil {
		return stage, fmt.Errorf("import feeds: %w", err)
	}
	if feedStats.FeedsChecked == 0 {
		progressf(common.Progress, "Feeds import complete: %s (%s)\n", feedNoWorkSummary(ctx, st), stage.Duration)
		return stage, nil
	}
	progressf(common.Progress, "Feeds import complete: feeds_checked=%d changed=%d unchanged=%d entries=%d created=%d updated=%d errors=%d (%s)\n", feedStats.FeedsChecked, feedStats.FeedsChanged, feedStats.FeedsUnchanged, feedStats.EntriesSeen, feedStats.ItemsCreated, feedStats.ItemsUpdated, feedStats.Errors, stage.Duration)
	return stage, nil
}

func feedAllowPrivateNetworkFromRuntime(rootDir string) bool {
	return runtimeenv.FirstBool(rootDir, "DBRAIN_FEEDS_ALLOW_PRIVATE_NETWORK", "DBRAIN_FEEDS_ALLOW_PRIVATE_NETWORKS")
}

func feedNoWorkSummary(ctx context.Context, st *store.Store) string {
	feeds, err := st.ListFeeds(ctx, false)
	if err != nil {
		return fmt.Sprintf("checked=0 due=0 schedule_status=unknown error=%q", err.Error())
	}
	if len(feeds) == 0 {
		return "checked=0 subscribed=0 due=0 no subscribed feeds configured"
	}

	now := time.Now().UTC()
	nextDue := time.Time{}
	var blocked, dead, backingOff int
	var firstError string
	for _, feed := range feeds {
		switch feed.HealthStatus {
		case store.FeedHealthBlocked:
			blocked++
			continue
		case store.FeedHealthDead:
			dead++
			continue
		}
		if feed.HealthStatus == store.FeedHealthError && feed.NextFetchAfter.After(now) {
			backingOff++
			if firstError == "" {
				firstError = feed.LastError
			}
		}
		if feed.NextFetchAfter.IsZero() {
			nextDue = time.Time{}
			break
		}
		if nextDue.IsZero() || feed.NextFetchAfter.Before(nextDue) {
			nextDue = feed.NextFetchAfter
		}
	}

	parts := []string{
		"checked=0",
		fmt.Sprintf("subscribed=%d", len(feeds)),
		"due=0",
	}
	if !nextDue.IsZero() {
		parts = append(parts, "next_due="+nextDue.UTC().Format(time.RFC3339))
	}
	if backingOff > 0 {
		parts = append(parts, fmt.Sprintf("backing_off=%d", backingOff))
	}
	if blocked > 0 {
		parts = append(parts, fmt.Sprintf("blocked=%d", blocked))
	}
	if dead > 0 {
		parts = append(parts, fmt.Sprintf("dead=%d", dead))
	}
	if firstError != "" {
		parts = append(parts, "last_error="+quoteShortFeedError(firstError))
	}
	return strings.Join(parts, " ")
}

func quoteShortFeedError(value string) string {
	value = strings.TrimSpace(value)
	const limit = 120
	if len(value) > limit {
		value = value[:limit-3] + "..."
	}
	return fmt.Sprintf("%q", value)
}

func executeSourcesStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (*SourcesStage, error) {
	common := opts.Common
	stageOpts := opts.Sources
	progressf(common.Progress, "==> worker sources\n")
	start := time.Now()
	sourceStats, err := runSourceWorker(
		ctx,
		func(ctx context.Context) (store.BacklogStats, error) {
			return st.Backlog(ctx, "", "", "")
		},
		func(ctx context.Context, _ int) (sourceenrich.Stats, error) {
			batchStats, _, err := runSourceEnrichPending(ctx, cfg, st, sourceenrich.Options{
				Limit:       stageOpts.Limit,
				Concurrency: stageOpts.Concurrency,
				Force:       common.Force,
				Summarize:   common.Summarize,
				Model:       common.Model,
				CLI:         common.CLI,
				Length:      common.Length,
				Timeout:     common.Timeout,
				Logger:      common.Logger,
				Metrics:     common.Metrics,
				OnSourceResult: func(result sourceenrich.SourceResult) {
					emitSourceSummaryMetric(common.Metrics, result, common, stageOpts)
				},
			})
			return batchStats, err
		},
		worker.SourceOptions{
			Watch:         stageOpts.Watch,
			PollInterval:  stageOpts.PollInterval,
			IdleExitAfter: stageOpts.IdleExitAfter,
			MaxCycles:     stageOpts.MaxCycles,
			Logger:        common.Logger,
		},
	)
	stage := &SourcesStage{Duration: time.Since(start), Stats: sourceStats}
	if err != nil {
		return stage, fmt.Errorf("worker sources: %w", err)
	}
	progressf(common.Progress, "Source worker complete: work_cycles=%d sources_summarized=%d errors=%d stopped=%s (%s)\n", sourceStats.WorkCycles, sourceStats.SourcesSummarized, sourceStats.Errors, sourceStats.StoppedReason, stage.Duration)
	return stage, nil
}

func executeMediaArchiveStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (*MediaArchiveStage, error) {
	common := opts.Common
	stageOpts := opts.Archive
	progressf(common.Progress, "==> archive media\n")
	start := time.Now()
	archiveStats, err := runMediaArchive(ctx, cfg, st, mediaarchive.Options{
		Limit:         stageOpts.Limit,
		Upload:        true,
		PruneLocal:    true,
		Provider:      stageOpts.Provider,
		Bucket:        stageOpts.Bucket,
		PublicBaseURL: stageOpts.PublicBaseURL,
		Endpoint:      stageOpts.Endpoint,
		Region:        stageOpts.Region,
		AccessKeyID:   stageOpts.AccessKeyID,
		SecretKey:     stageOpts.SecretKey,
		SessionToken:  stageOpts.SessionToken,
		PathStyle:     true,
		Logger:        common.Logger,
	})
	stage := &MediaArchiveStage{Duration: time.Since(start), Stats: archiveStats}
	if err != nil {
		return stage, fmt.Errorf("archive media: %w", err)
	}
	progressf(common.Progress, "Media archive complete: candidates=%d uploaded=%d archived=%d unchanged=%d prune_skipped=%d local_files_pruned=%d local_rows_pruned=%d errors=%d (%s)\n", archiveStats.Candidates, archiveStats.Uploaded, archiveStats.Archived, archiveStats.Unchanged, archiveStats.PruneSkipped, archiveStats.LocalFilesPruned, archiveStats.LocalRowsPruned, archiveStats.Errors, stage.Duration)
	return stage, nil
}
