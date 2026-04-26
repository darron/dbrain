package syncjob

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/githubimport"
	"dbrain/internal/linkextract"
	"dbrain/internal/mediaarchive"
	"dbrain/internal/sourceenrich"
	"dbrain/internal/store"
	"dbrain/internal/summarizecli"
	"dbrain/internal/worker"
	"dbrain/internal/xapi"
	"dbrain/internal/xmediatranscribe"
	"dbrain/internal/xphotoocr"
	"dbrain/internal/youtubeimport"
)

var (
	runXBookmarkImport = xapi.RunBookmarks
	runXHydrate        = xapi.Run
	runXMediaStage     = xmediatranscribe.Run
	runXPhotoOCRStage  = xphotoocr.Run
	runLinkExtract     = linkextract.Run
	runGitHubImport    = githubimport.Run
	runYouTubeImport   = youtubeimport.Run
	runSourceWorker    = worker.RunSources
	runMediaArchive    = mediaarchive.Run
	summaryToolVersion = summarizecli.SummaryToolVersion
)

const maxXQuoteDrainPasses = 8
const maxXFrontierSettlePasses = 3

type Options struct {
	XBookmarksEnabled bool
	XBookmarksLimit   int

	XEnabled         bool
	XLimit           int
	XConcurrency     int
	XTimeout         time.Duration
	XMediaEnabled    bool
	XMediaLimit      int
	XPhotoOCREnabled bool
	XPhotoOCRLimit   int

	LinksEnabled      bool
	LinkDiscoverLimit int
	LinkLimit         int
	LinkConcurrency   int

	GitHubEnabled bool
	GitHubLimit   int

	YouTubeEnabled bool
	YouTubeLimit   int
	WatchLater     bool
	Liked          bool

	SourcesEnabled      bool
	SourceLimit         int
	SourceConcurrency   int
	SourceWatch         bool
	SourcePollInterval  time.Duration
	SourceIdleExitAfter time.Duration
	SourceMaxCycles     int

	Browser              string
	Profile              string
	Force                bool
	Summarize            bool
	Model                string
	OCRModel             string
	ArchiveMediaEnabled  bool
	ArchiveMediaLimit    int
	ArchiveProvider      string
	ArchiveBucket        string
	ArchivePublicBaseURL string
	ArchiveEndpoint      string
	ArchiveRegion        string
	ArchiveAccessKeyID   string
	ArchiveSecretKey     string
	ArchiveSessionToken  string
	CLI                  string
	Length               string
	Timeout              time.Duration
	Logger               *slog.Logger
	Progress             io.Writer
}

type Stats struct {
	StartedAt    time.Time          `json:"started_at"`
	CompletedAt  time.Time          `json:"completed_at,omitempty"`
	Duration     time.Duration      `json:"duration"`
	XBookmarks   *XBookmarksStage   `json:"x_bookmarks,omitempty"`
	X            *XStage            `json:"x,omitempty"`
	XMedia       *XMediaStage       `json:"x_media,omitempty"`
	XPhotoOCR    *XPhotoOCRStage    `json:"x_photo_ocr,omitempty"`
	Links        *LinksStage        `json:"links,omitempty"`
	GitHub       *GitHubStage       `json:"github,omitempty"`
	YouTube      *YouTubeStage      `json:"youtube,omitempty"`
	Sources      *SourcesStage      `json:"sources,omitempty"`
	MediaArchive *MediaArchiveStage `json:"media_archive,omitempty"`
}

type XBookmarksStage struct {
	Duration time.Duration      `json:"duration"`
	Stats    xapi.BookmarkStats `json:"stats"`
}

type XStage struct {
	Duration time.Duration `json:"duration"`
	Stats    xapi.Stats    `json:"stats"`
}

type XMediaStage struct {
	Duration time.Duration          `json:"duration"`
	Stats    xmediatranscribe.Stats `json:"stats"`
}

type XPhotoOCRStage struct {
	Duration time.Duration   `json:"duration"`
	Stats    xphotoocr.Stats `json:"stats"`
}

type LinksStage struct {
	Duration time.Duration     `json:"duration"`
	Stats    linkextract.Stats `json:"stats"`
}

type GitHubStage struct {
	Duration time.Duration      `json:"duration"`
	Stats    githubimport.Stats `json:"stats"`
}

type YouTubeStage struct {
	Duration time.Duration       `json:"duration"`
	Stats    youtubeimport.Stats `json:"stats"`
}

type SourcesStage struct {
	Duration time.Duration      `json:"duration"`
	Stats    worker.SourceStats `json:"stats"`
}

type MediaArchiveStage struct {
	Duration time.Duration      `json:"duration"`
	Stats    mediaarchive.Stats `json:"stats"`
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if strings.TrimSpace(opts.Browser) == "" {
		opts.Browser = "chrome"
	}
	if strings.TrimSpace(opts.Length) == "" {
		opts.Length = "medium"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	if opts.XLimit <= 0 {
		opts.XLimit = 100
	}
	if opts.XTimeout <= 0 {
		opts.XTimeout = 30 * time.Second
	}
	if opts.XMediaLimit <= 0 {
		opts.XMediaLimit = opts.XLimit
	}
	if opts.XPhotoOCRLimit <= 0 {
		opts.XPhotoOCRLimit = opts.XLimit
	}
	if opts.XConcurrency <= 0 {
		opts.XConcurrency = 4
	}
	if opts.LinkDiscoverLimit <= 0 {
		opts.LinkDiscoverLimit = 500
	}
	if opts.LinkLimit <= 0 {
		opts.LinkLimit = 100
	}
	if opts.LinkConcurrency <= 0 {
		opts.LinkConcurrency = 4
	}
	if opts.YouTubeLimit <= 0 {
		opts.YouTubeLimit = 50
	}
	if opts.SourceLimit <= 0 {
		opts.SourceLimit = 100
	}
	if opts.SourceConcurrency <= 0 {
		opts.SourceConcurrency = 4
	}
	if opts.ArchiveMediaLimit <= 0 {
		opts.ArchiveMediaLimit = 5000
	}

	stats := Stats{StartedAt: time.Now().UTC()}
	progressf(opts.Progress, "Sync started at %s\n", stats.StartedAt.Format(time.RFC3339))

	runXBookmarksPass := func() (xapi.BookmarkStats, time.Duration, error) {
		progressf(opts.Progress, "==> import x-bookmarks\n")
		start := time.Now()
		bookmarkStats, err := runXBookmarkImport(ctx, cfg, st, xapi.BookmarkOptions{
			Limit:   opts.XBookmarksLimit,
			Browser: opts.Browser,
			Profile: opts.Profile,
			Force:   opts.Force,
			Timeout: opts.XTimeout,
			Logger:  opts.Logger,
		})
		duration := time.Since(start)
		if err == nil {
			progressf(opts.Progress, "X bookmarks import complete: created=%d updated=%d unchanged=%d rendered=%d pages=%d stopped=%s (%s)\n", bookmarkStats.Created, bookmarkStats.Updated, bookmarkStats.Unchanged, bookmarkStats.Rendered, bookmarkStats.PagesFetched, bookmarkStats.StoppedReason, duration)
		}
		return bookmarkStats, duration, err
	}

	runXHydratePass := func() (xapi.Stats, time.Duration, error) {
		progressf(opts.Progress, "==> hydrate x\n")
		start := time.Now()
		xStats, err := runXHydrate(ctx, cfg, st, xapi.Options{
			Limit:       opts.XLimit,
			Force:       opts.Force,
			Concurrency: opts.XConcurrency,
			Browser:     opts.Browser,
			Profile:     opts.Profile,
			Timeout:     opts.XTimeout,
			Logger:      opts.Logger,
		})
		if err != nil {
			return xStats, time.Since(start), err
		}
		if !opts.Force && xStats.Candidates > 0 {
			for pass := 1; pass <= maxXQuoteDrainPasses; pass++ {
				quoteStats, quoteErr := runXHydrate(ctx, cfg, st, xapi.Options{
					Limit:       opts.XLimit,
					Force:       false,
					QuoteOnly:   true,
					Concurrency: opts.XConcurrency,
					Browser:     opts.Browser,
					Profile:     opts.Profile,
					Timeout:     opts.XTimeout,
					Logger:      opts.Logger,
				})
				mergeXStats(&xStats, quoteStats)
				if quoteErr != nil {
					return xStats, time.Since(start), fmt.Errorf("hydrate x quote pass %d: %w", pass, quoteErr)
				}
				if quoteStats.Candidates == 0 {
					break
				}
				progressf(opts.Progress, "X quote hydration pass %d complete: hydrated=%d missing=%d api_errors=%d media_downloaded=%d media_errors=%d rendered=%d\n", pass, quoteStats.Hydrated, quoteStats.Missing, quoteStats.APIErrors, quoteStats.MediaDownloaded, quoteStats.MediaErrors, quoteStats.Rendered)
				if pass == maxXQuoteDrainPasses {
					progressf(opts.Progress, "X quote hydration drain stopped after %d extra passes with candidates still present\n", maxXQuoteDrainPasses)
				}
			}
		}
		duration := time.Since(start)
		progressf(opts.Progress, "X hydration complete: hydrated=%d missing=%d api_errors=%d media_downloaded=%d media_errors=%d rendered=%d (%s)\n", xStats.Hydrated, xStats.Missing, xStats.APIErrors, xStats.MediaDownloaded, xStats.MediaErrors, xStats.Rendered, duration)
		return xStats, duration, nil
	}

	runLinksPass := func() (linkextract.Stats, time.Duration, error) {
		progressf(opts.Progress, "==> extract links\n")
		start := time.Now()
		linkStats, err := runLinkExtract(ctx, cfg, st, linkextract.Options{
			DiscoverLimit: opts.LinkDiscoverLimit,
			Limit:         opts.LinkLimit,
			Concurrency:   opts.LinkConcurrency,
			Force:         opts.Force,
			Summarize:     opts.Summarize,
			Model:         opts.Model,
			CLI:           opts.CLI,
			Length:        opts.Length,
			Timeout:       opts.Timeout,
			Logger:        opts.Logger,
		})
		duration := time.Since(start)
		if err == nil {
			progressf(opts.Progress, "Link extraction complete: items_scanned=%d sources_queued=%d sources_summarized=%d errors=%d (%s)\n", linkStats.ItemsScanned, linkStats.SourcesQueued, linkStats.SourcesSummarized, linkStats.Errors, duration)
		}
		return linkStats, duration, err
	}

	if shouldSettleXFrontier(opts) {
		for pass := 1; pass <= maxXFrontierSettlePasses; pass++ {
			if pass > 1 {
				progressf(opts.Progress, "==> x settle pass %d\n", pass)
			}

			frontierActive := false

			bookmarkStats, bookmarkDuration, err := runXBookmarksPass()
			mergeXBookmarkStage(&stats.XBookmarks, bookmarkDuration, bookmarkStats)
			if err != nil {
				return finishStats(stats), fmt.Errorf("import x-bookmarks: %w", err)
			}
			if bookmarkStats.Created > 0 || bookmarkStats.Updated > 0 {
				frontierActive = true
			}

			xStats, xDuration, err := runXHydratePass()
			mergeXStage(&stats.X, xDuration, xStats)
			if err != nil {
				return finishStats(stats), fmt.Errorf("hydrate x: %w", err)
			}
			if xStats.Candidates > 0 {
				frontierActive = true
			}

			linkStats, linkDuration, err := runLinksPass()
			mergeLinksStage(&stats.Links, linkDuration, linkStats)
			if err != nil {
				return finishStats(stats), fmt.Errorf("extract links: %w", err)
			}
			if linkStats.ItemsScanned > 0 {
				frontierActive = true
			}

			if !frontierActive {
				break
			}
			if pass == maxXFrontierSettlePasses {
				progressf(opts.Progress, "X frontier settle stopped after %d passes with activity still present\n", maxXFrontierSettlePasses)
			}
		}
	} else {
		if opts.XBookmarksEnabled {
			bookmarkStats, bookmarkDuration, err := runXBookmarksPass()
			mergeXBookmarkStage(&stats.XBookmarks, bookmarkDuration, bookmarkStats)
			if err != nil {
				return finishStats(stats), fmt.Errorf("import x-bookmarks: %w", err)
			}
		}

		if opts.XEnabled {
			xStats, xDuration, err := runXHydratePass()
			mergeXStage(&stats.X, xDuration, xStats)
			if err != nil {
				return finishStats(stats), fmt.Errorf("hydrate x: %w", err)
			}
		}

		if opts.LinksEnabled {
			linkStats, linkDuration, err := runLinksPass()
			mergeLinksStage(&stats.Links, linkDuration, linkStats)
			if err != nil {
				return finishStats(stats), fmt.Errorf("extract links: %w", err)
			}
		}
	}

	if opts.XMediaEnabled {
		progressf(opts.Progress, "==> transcribe x-media\n")
		start := time.Now()
		xMediaStats, err := runXMediaStage(ctx, cfg, st, xmediatranscribe.Options{
			Limit:         opts.XMediaLimit,
			Force:         opts.Force,
			Concurrency:   opts.XConcurrency,
			Timeout:       opts.Timeout,
			Summarize:     opts.Summarize,
			SummaryModel:  opts.Model,
			SummaryCLI:    opts.CLI,
			SummaryLength: opts.Length,
			Logger:        opts.Logger,
		})
		stage := &XMediaStage{Duration: time.Since(start), Stats: xMediaStats}
		stats.XMedia = stage
		if err != nil {
			return finishStats(stats), fmt.Errorf("transcribe x-media: %w", err)
		}
		progressf(opts.Progress, "X media transcription complete: items_processed=%d items_updated=%d items_skipped=%d media_transcribed=%d items_summarized=%d errors=%d summary_errors=%d (%s)\n", xMediaStats.ItemsProcessed, xMediaStats.ItemsUpdated, xMediaStats.ItemsSkipped, xMediaStats.MediaTranscribed, xMediaStats.ItemsSummarized, xMediaStats.Errors, xMediaStats.SummaryErrors, stage.Duration)
	}

	if opts.XPhotoOCREnabled {
		progressf(opts.Progress, "==> ocr x-photos\n")
		start := time.Now()
		ocrStats, err := runXPhotoOCRStage(ctx, cfg, st, xphotoocr.Options{
			Limit:       opts.XPhotoOCRLimit,
			Force:       opts.Force,
			Concurrency: opts.XConcurrency,
			Timeout:     opts.Timeout,
			Model:       opts.OCRModel,
			Logger:      opts.Logger,
		})
		stage := &XPhotoOCRStage{Duration: time.Since(start), Stats: ocrStats}
		stats.XPhotoOCR = stage
		if err != nil {
			return finishStats(stats), fmt.Errorf("ocr x-photos: %w", err)
		}
		progressf(opts.Progress, "X photo OCR complete: items_processed=%d items_updated=%d items_skipped=%d photos_ocred=%d errors=%d (%s)\n", ocrStats.ItemsProcessed, ocrStats.ItemsUpdated, ocrStats.ItemsSkipped, ocrStats.PhotosOCRed, ocrStats.Errors, stage.Duration)
	}

	if opts.GitHubEnabled {
		progressf(opts.Progress, "==> import github stars\n")
		start := time.Now()
		githubStats, err := runGitHubImport(ctx, cfg, st, githubimport.Options{
			Limit:     opts.GitHubLimit,
			Force:     opts.Force,
			Summarize: opts.Summarize,
			Model:     opts.Model,
			CLI:       opts.CLI,
			Length:    opts.Length,
			Timeout:   opts.Timeout,
			Logger:    opts.Logger,
		})
		stage := &GitHubStage{Duration: time.Since(start), Stats: githubStats}
		stats.GitHub = stage
		if err != nil {
			return finishStats(stats), fmt.Errorf("import github stars: %w", err)
		}
		progressf(opts.Progress, "GitHub stars complete: stars=%d items_created=%d sources_summarized=%d errors=%d (%s)\n", githubStats.StarsProcessed, githubStats.ItemsCreated, githubStats.SourcesSummarized, githubStats.Errors, stage.Duration)
	}

	if opts.YouTubeEnabled {
		progressf(opts.Progress, "==> import youtube\n")
		start := time.Now()
		youtubeStats, err := runYouTubeImport(ctx, cfg, st, youtubeimport.Options{
			Browser:    opts.Browser,
			Profile:    opts.Profile,
			Limit:      opts.YouTubeLimit,
			WatchLater: opts.WatchLater,
			Liked:      opts.Liked,
			Summarize:  opts.Summarize,
			Force:      opts.Force,
			Model:      opts.Model,
			CLI:        opts.CLI,
			Length:     opts.Length,
			Timeout:    opts.Timeout,
			Logger:     opts.Logger,
		})
		stage := &YouTubeStage{Duration: time.Since(start), Stats: youtubeStats}
		stats.YouTube = stage
		if err != nil {
			return finishStats(stats), fmt.Errorf("import youtube: %w", err)
		}
		progressf(opts.Progress, "YouTube import complete: items_processed=%d sources_summarized=%d errors=%d (%s)\n", youtubeStats.ItemsProcessed, youtubeStats.SourcesSummarized, youtubeStats.Errors, stage.Duration)
	}

	if opts.SourcesEnabled {
		progressf(opts.Progress, "==> worker sources\n")
		start := time.Now()
		toolName := summarizecli.SummaryToolName(opts.Model)
		toolVersion := summaryToolVersion(ctx, "", opts.Model)
		sourceStats, err := runSourceWorker(
			ctx,
			func(ctx context.Context) (store.BacklogStats, error) {
				return st.Backlog(ctx, sourceenrich.SummaryPromptVersion, toolName, toolVersion)
			},
			func(ctx context.Context, _ int) (sourceenrich.Stats, error) {
				batchStats, _, err := sourceenrich.RunPending(ctx, cfg, st, sourceenrich.Options{
					Limit:       opts.SourceLimit,
					Concurrency: opts.SourceConcurrency,
					Force:       opts.Force,
					Summarize:   opts.Summarize,
					Model:       opts.Model,
					CLI:         opts.CLI,
					Length:      opts.Length,
					Timeout:     opts.Timeout,
					Logger:      opts.Logger,
				})
				return batchStats, err
			},
			worker.SourceOptions{
				Watch:         opts.SourceWatch,
				PollInterval:  opts.SourcePollInterval,
				IdleExitAfter: opts.SourceIdleExitAfter,
				MaxCycles:     opts.SourceMaxCycles,
				Logger:        opts.Logger,
			},
		)
		stage := &SourcesStage{Duration: time.Since(start), Stats: sourceStats}
		stats.Sources = stage
		if err != nil {
			return finishStats(stats), fmt.Errorf("worker sources: %w", err)
		}
		progressf(opts.Progress, "Source worker complete: work_cycles=%d sources_summarized=%d errors=%d stopped=%s (%s)\n", sourceStats.WorkCycles, sourceStats.SourcesSummarized, sourceStats.Errors, sourceStats.StoppedReason, stage.Duration)
	}

	if opts.ArchiveMediaEnabled {
		progressf(opts.Progress, "==> archive media\n")
		start := time.Now()
		archiveStats, err := runMediaArchive(ctx, cfg, st, mediaarchive.Options{
			Limit:         opts.ArchiveMediaLimit,
			Upload:        true,
			PruneLocal:    true,
			Provider:      opts.ArchiveProvider,
			Bucket:        opts.ArchiveBucket,
			PublicBaseURL: opts.ArchivePublicBaseURL,
			Endpoint:      opts.ArchiveEndpoint,
			Region:        opts.ArchiveRegion,
			AccessKeyID:   opts.ArchiveAccessKeyID,
			SecretKey:     opts.ArchiveSecretKey,
			SessionToken:  opts.ArchiveSessionToken,
			PathStyle:     true,
			Logger:        opts.Logger,
		})
		stage := &MediaArchiveStage{Duration: time.Since(start), Stats: archiveStats}
		stats.MediaArchive = stage
		if err != nil {
			return finishStats(stats), fmt.Errorf("archive media: %w", err)
		}
		progressf(opts.Progress, "Media archive complete: candidates=%d uploaded=%d archived=%d unchanged=%d prune_skipped=%d local_files_pruned=%d local_rows_pruned=%d errors=%d (%s)\n", archiveStats.Candidates, archiveStats.Uploaded, archiveStats.Archived, archiveStats.Unchanged, archiveStats.PruneSkipped, archiveStats.LocalFilesPruned, archiveStats.LocalRowsPruned, archiveStats.Errors, stage.Duration)
	}

	stats = finishStats(stats)
	progressf(opts.Progress, "Sync completed in %s\n", stats.Duration)
	return stats, nil
}

func finishStats(stats Stats) Stats {
	stats.CompletedAt = time.Now().UTC()
	stats.Duration = stats.CompletedAt.Sub(stats.StartedAt)
	return stats
}

func progressf(dst io.Writer, format string, args ...any) {
	if dst == nil {
		return
	}
	_, _ = fmt.Fprintf(dst, format, args...)
}

func mergeXStats(dst *xapi.Stats, src xapi.Stats) {
	if dst == nil {
		return
	}
	dst.Candidates += src.Candidates
	dst.Requested += src.Requested
	dst.Hydrated += src.Hydrated
	dst.Missing += src.Missing
	dst.APIErrors += src.APIErrors
	dst.Rendered += src.Rendered
	dst.Unchanged += src.Unchanged
	dst.MediaCandidates += src.MediaCandidates
	dst.MediaRequested += src.MediaRequested
	dst.MediaDownloaded += src.MediaDownloaded
	dst.MediaGone += src.MediaGone
	dst.MediaErrors += src.MediaErrors
}

func mergeXBookmarkStage(dst **XBookmarksStage, duration time.Duration, src xapi.BookmarkStats) {
	if *dst == nil {
		*dst = &XBookmarksStage{Duration: duration, Stats: src}
		return
	}
	(*dst).Duration += duration
	mergeXBookmarkStats(&(*dst).Stats, src)
}

func mergeXBookmarkStats(dst *xapi.BookmarkStats, src xapi.BookmarkStats) {
	if dst == nil {
		return
	}
	dst.PagesFetched += src.PagesFetched
	dst.Processed += src.Processed
	dst.Created += src.Created
	dst.Updated += src.Updated
	dst.Unchanged += src.Unchanged
	dst.Rendered += src.Rendered
	dst.StalePages += src.StalePages
	if strings.TrimSpace(src.StoppedReason) != "" {
		dst.StoppedReason = src.StoppedReason
	}
}

func mergeXStage(dst **XStage, duration time.Duration, src xapi.Stats) {
	if *dst == nil {
		*dst = &XStage{Duration: duration, Stats: src}
		return
	}
	(*dst).Duration += duration
	mergeXStats(&(*dst).Stats, src)
}

func mergeLinksStage(dst **LinksStage, duration time.Duration, src linkextract.Stats) {
	if *dst == nil {
		*dst = &LinksStage{Duration: duration, Stats: src}
		return
	}
	(*dst).Duration += duration
	mergeLinkStats(&(*dst).Stats, src)
}

func mergeLinkStats(dst *linkextract.Stats, src linkextract.Stats) {
	if dst == nil {
		return
	}
	dst.ItemsScanned += src.ItemsScanned
	dst.ItemsMarked += src.ItemsMarked
	dst.LinksFound += src.LinksFound
	dst.SourcesCreated += src.SourcesCreated
	dst.LinksCreated += src.LinksCreated
	dst.SourcesQueued += src.SourcesQueued
	dst.SourcesExtracted += src.SourcesExtracted
	dst.SourcesSummarized += src.SourcesSummarized
	dst.SourcesRendered += src.SourcesRendered
	dst.SourcesUnchanged += src.SourcesUnchanged
	dst.Errors += src.Errors
}

func shouldSettleXFrontier(opts Options) bool {
	return !opts.Force &&
		opts.XBookmarksEnabled &&
		opts.XEnabled &&
		opts.LinksEnabled
}
