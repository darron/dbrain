package syncjob

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/darron/dbrain/internal/applenotes"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/githubimport"
	"github.com/darron/dbrain/internal/itemcategorize"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/safaritabs"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/worker"
	"github.com/darron/dbrain/internal/xapi"
	"github.com/darron/dbrain/internal/xmediatranscribe"
	"github.com/darron/dbrain/internal/xphotoocr"
	"github.com/darron/dbrain/internal/youtubeimport"
)

var (
	runXBookmarkImport  = xapi.RunBookmarks
	runXHydrate         = xapi.Run
	runXMediaStage      = xmediatranscribe.Run
	runXPhotoOCRStage   = xphotoocr.Run
	runLinkExtract      = linkextract.Run
	runGitHubImport     = githubimport.Run
	runYouTubeImport    = youtubeimport.Run
	runSourceWorker     = worker.RunSources
	runMediaArchive     = mediaarchive.Run
	runItemCategorize   = itemcategorize.Batch
	runSourceCategorize = itemcategorize.BatchSources
	runAppleNotesImport = applenotes.Run
	runSafariTabsImport = safaritabs.Run
)

const maxXQuoteDrainPasses = 8
const maxXFrontierSettlePasses = 3

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	opts = normalizeOptions(opts)

	stats := Stats{StartedAt: time.Now().UTC()}
	progressf(opts.Progress, "Sync started at %s\n", stats.StartedAt.Format(time.RFC3339))

	if opts.AppleNotesEnabled {
		progressf(opts.Progress, "==> import apple-notes\n")
		start := time.Now()
		var appleNotesProgress applenotes.ProgressFunc
		if opts.Progress != nil {
			appleNotesProgress = func(event applenotes.ProgressEvent) {
				formatAppleNotesSyncProgress(opts.Progress, event)
			}
		}
		appleStats, err := runAppleNotesImport(ctx, cfg, st, applenotes.Options{
			DBPath:             opts.AppleNotesDBPath,
			Limit:              opts.AppleNotesLimit,
			Force:              opts.Force,
			ExcludeFolders:     opts.AppleNotesExcludeFolders,
			ExcludeAccounts:    opts.AppleNotesExcludeAccounts,
			ExcludeShared:      opts.AppleNotesExcludeShared,
			IncludeLocked:      opts.AppleNotesIncludeLocked,
			SkipAttachments:    opts.AppleNotesSkipAttachments,
			SkipAttachmentOCR:  opts.AppleNotesSkipAttachmentOCR,
			AttachmentMaxBytes: opts.AppleNotesAttachmentMaxBytes,
			TesseractBinary:    opts.AppleNotesTesseractBinary,
			Summarize:          opts.Summarize,
			SummaryModel:       opts.Model,
			SummaryCLI:         opts.CLI,
			SummaryLength:      opts.Length,
			Timeout:            opts.Timeout,
			Progress:           appleNotesProgress,
		})
		stage := &AppleNotesStage{Duration: time.Since(start), Stats: appleStats}
		stats.AppleNotes = stage
		if err != nil {
			return finishStats(stats), fmt.Errorf("import apple-notes: %w", err)
		}
		progressf(opts.Progress, "Apple Notes import complete: seen=%d imported=%d rendered=%d skipped=%d blocked=%d attachments=%d extracted=%d ocr=%d summarized=%d errors=%d (%s)\n", appleStats.NotesSeen, appleStats.NotesImported, appleStats.NotesRendered, appleStats.NotesSkipped, appleStats.NotesBlocked, appleStats.AttachmentsIndexed, appleStats.AttachmentsExtracted, appleStats.AttachmentsOCRed, appleStats.SummariesCreated, appleStats.Errors, stage.Duration)
	}

	if opts.SafariTabsEnabled {
		progressf(opts.Progress, "==> import safari-tabs\n")
		start := time.Now()
		var safariTabsProgress safaritabs.ProgressFunc
		if opts.Progress != nil {
			safariTabsProgress = func(event safaritabs.ProgressEvent) {
				formatSafariTabsSyncProgress(opts.Progress, event)
			}
		}
		safariStats, err := runSafariTabsImport(ctx, cfg, st, safaritabs.Options{
			DBPath:    opts.SafariTabsDBPath,
			Device:    opts.SafariTabsDevice,
			Limit:     opts.SafariTabsLimit,
			OlderThan: opts.SafariTabsOlderThan,
			Force:     opts.Force,
			Progress:  safariTabsProgress,
		})
		stage := &SafariTabsStage{Duration: time.Since(start), Stats: safariStats}
		stats.SafariTabs = stage
		if err != nil {
			return finishStats(stats), fmt.Errorf("import safari-tabs: %w", err)
		}
		progressf(opts.Progress, "Safari Tabs import complete: device=%s seen=%d matched=%d created=%d updated=%d unchanged=%d rendered=%d skipped=%d links=%d errors=%d (%s)\n", emptyProgressValue(safariStats.DeviceName), safariStats.TabsSeen, safariStats.TabsMatched, safariStats.TabsCreated, safariStats.TabsUpdated, safariStats.TabsUnchanged, safariStats.TabsRendered, safariStats.TabsSkipped, safariStats.LinksFound, safariStats.Errors, stage.Duration)
	}

	if shouldSettleXFrontier(opts) {
		for pass := 1; pass <= maxXFrontierSettlePasses; pass++ {
			if pass > 1 {
				progressf(opts.Progress, "==> x settle pass %d\n", pass)
			}

			frontierActive := false

			bookmarkStats, bookmarkDuration, err := runXBookmarksPass(ctx, cfg, st, opts)
			mergeXBookmarkStage(&stats.XBookmarks, bookmarkDuration, bookmarkStats)
			if err != nil {
				return finishStats(stats), fmt.Errorf("import x-bookmarks: %w", err)
			}
			if bookmarkStats.Created > 0 || bookmarkStats.Updated > 0 {
				frontierActive = true
			}

			xStats, xDuration, err := runXHydratePass(ctx, cfg, st, opts)
			mergeXStage(&stats.X, xDuration, xStats)
			if err != nil {
				return finishStats(stats), fmt.Errorf("hydrate x: %w", err)
			}
			if xStats.Candidates > 0 {
				frontierActive = true
			}

			linkStats, linkDuration, err := runLinksPass(ctx, cfg, st, opts)
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
			bookmarkStats, bookmarkDuration, err := runXBookmarksPass(ctx, cfg, st, opts)
			mergeXBookmarkStage(&stats.XBookmarks, bookmarkDuration, bookmarkStats)
			if err != nil {
				return finishStats(stats), fmt.Errorf("import x-bookmarks: %w", err)
			}
		}

		if opts.XEnabled {
			xStats, xDuration, err := runXHydratePass(ctx, cfg, st, opts)
			mergeXStage(&stats.X, xDuration, xStats)
			if err != nil {
				return finishStats(stats), fmt.Errorf("hydrate x: %w", err)
			}
		}

		if opts.LinksEnabled {
			linkStats, linkDuration, err := runLinksPass(ctx, cfg, st, opts)
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
		sourceStats, err := runSourceWorker(
			ctx,
			func(ctx context.Context) (store.BacklogStats, error) {
				return st.Backlog(ctx, "", "", "")
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

	if opts.CategorizeEnabled {
		progressf(opts.Progress, "==> categorize items and sources\n")
		start := time.Now()
		var itemProcessed atomic.Int64
		var itemErrors atomic.Int64
		var itemTotal atomic.Int64
		itemStats, _, err := runItemCategorize(ctx, cfg, st, itemcategorize.Options{
			Model:           opts.CategorizeModel,
			Timeout:         opts.CategorizeTimeout,
			Concurrency:     opts.CategorizeConcurrency,
			Limit:           opts.CategorizeLimit,
			Force:           opts.Force,
			Apply:           true,
			IncludeImages:   opts.CategorizeImages,
			S3Endpoint:      opts.ArchiveEndpoint,
			S3Region:        opts.ArchiveRegion,
			S3AccessKey:     opts.ArchiveAccessKeyID,
			S3SecretKey:     opts.ArchiveSecretKey,
			OpenRouterTitle: "dbrain sync categorize",
			OnStart: func(total int) {
				itemTotal.Store(int64(total))
				if opts.Logger != nil {
					opts.Logger.Debug("item categorization candidates loaded",
						"processed", 0,
						"total", total,
						"remaining", total,
						"items", total,
						"limit", opts.CategorizeLimit,
						"force", opts.Force,
						"concurrency", opts.CategorizeConcurrency,
					)
				}
			},
			OnResult: func(ir itemcategorize.ItemResult) {
				processed := itemProcessed.Add(1)
				total := itemTotal.Load()
				remaining := total - processed
				if remaining < 0 {
					remaining = 0
				}
				if opts.Logger == nil {
					return
				}
				if ir.Error != "" {
					errors := itemErrors.Add(1)
					opts.Logger.Debug("item categorization failed",
						"source_key", ir.Item.SourceKey,
						"item_id", ir.Item.ID,
						"processed", processed,
						"total", total,
						"remaining", remaining,
						"errors", errors,
						"error", ir.Error,
					)
					return
				}
				opts.Logger.Debug("item categorized",
					"source_key", ir.Item.SourceKey,
					"item_id", ir.Item.ID,
					"processed", processed,
					"total", total,
					"remaining", remaining,
					"errors", itemErrors.Load(),
					"tags", strings.Join(ir.Result.Tags, ","),
					"categories", strings.Join(ir.Result.Categories, ","),
				)
			},
		})
		if err != nil {
			return finishStats(stats), fmt.Errorf("categorize items: %w", err)
		}

		var sourceProcessed atomic.Int64
		var sourceErrors atomic.Int64
		var sourceTotal atomic.Int64
		sourceStats, _, err := runSourceCategorize(ctx, cfg, st, itemcategorize.Options{
			Model:           opts.CategorizeModel,
			Timeout:         opts.CategorizeTimeout,
			Concurrency:     opts.CategorizeConcurrency,
			Limit:           opts.CategorizeLimit,
			Force:           opts.Force,
			Apply:           true,
			OpenRouterTitle: "dbrain sync categorize",
			OnStart: func(total int) {
				sourceTotal.Store(int64(total))
				if opts.Logger != nil {
					opts.Logger.Debug("source categorization candidates loaded",
						"processed", 0,
						"total", total,
						"remaining", total,
						"sources", total,
						"limit", opts.CategorizeLimit,
						"force", opts.Force,
						"concurrency", opts.CategorizeConcurrency,
					)
				}
			},
			OnSourceResult: func(sr itemcategorize.SourceResult) {
				processed := sourceProcessed.Add(1)
				total := sourceTotal.Load()
				remaining := total - processed
				if remaining < 0 {
					remaining = 0
				}
				if opts.Logger == nil {
					return
				}
				if sr.Error != "" {
					errors := sourceErrors.Add(1)
					opts.Logger.Debug("source categorization failed",
						"source_key", sr.Source.SourceKey,
						"source_id", sr.Source.ID,
						"processed", processed,
						"total", total,
						"remaining", remaining,
						"errors", errors,
						"error", sr.Error,
					)
					return
				}
				opts.Logger.Debug("source categorized",
					"source_key", sr.Source.SourceKey,
					"source_id", sr.Source.ID,
					"processed", processed,
					"total", total,
					"remaining", remaining,
					"errors", sourceErrors.Load(),
					"tags", strings.Join(sr.Result.Tags, ","),
					"categories", strings.Join(sr.Result.Categories, ","),
				)
			},
		})
		if err != nil {
			return finishStats(stats), fmt.Errorf("categorize sources: %w", err)
		}

		categorizeStats := mergeCategorizeStats(itemStats, sourceStats)
		stage := &CategorizeStage{
			Duration:    time.Since(start),
			Stats:       categorizeStats,
			ItemStats:   itemStats,
			SourceStats: sourceStats,
		}
		stats.Categorize = stage
		progressf(opts.Progress, "Categorization complete: item_queued=%d item_applied=%d source_queued=%d source_applied=%d succeeded=%d skipped=%d errors=%d (%s)\n", itemStats.Queued, itemStats.Applied, sourceStats.Queued, sourceStats.Applied, categorizeStats.Succeeded, categorizeStats.Skipped, categorizeStats.Errors, stage.Duration)
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
