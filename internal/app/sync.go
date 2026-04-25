package app

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"dbrain/internal/store"
	"dbrain/internal/syncjob"
)

func newSyncCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Run multi-stage refresh flows",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newSyncAllCommand(root))
	return cmd
}

func newSyncAllCommand(root *rootOptions) *cobra.Command {
	var xBookmarksLimit int
	var xLimit int
	var xConcurrency int
	var xTimeout time.Duration
	var ocrModel string
	var linkDiscoverLimit int
	var linkLimit int
	var linkConcurrency int
	var githubLimit int
	var youtubeLimit int
	var sourceLimit int
	var sourceConcurrency int
	var browser string
	var profile string
	var watchLater bool
	var liked bool
	var watch bool
	var pollInterval time.Duration
	var idleExitAfter time.Duration
	var maxCycles int
	var force bool
	var summarize bool
	var model string
	var cliProvider string
	var length string
	var timeout time.Duration
	var skipXBookmarks bool
	var skipFT bool
	var skipX bool
	var skipXMedia bool
	var skipXPhotoOCR bool
	var skipLinks bool
	var skipGitHub bool
	var skipYouTube bool
	var skipSources bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "all",
		Short: "Run the incremental brain refresh pipeline end to end",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			stats, err := syncjob.Run(cmd.Context(), cfg, st, syncjob.Options{
				XBookmarksEnabled:   !skipXBookmarks && !skipFT,
				XBookmarksLimit:     xBookmarksLimit,
				XEnabled:            !skipX,
				XLimit:              xLimit,
				XConcurrency:        xConcurrency,
				XTimeout:            xTimeout,
				XMediaEnabled:       !skipXMedia,
				XMediaLimit:         xLimit,
				XPhotoOCREnabled:    !skipXPhotoOCR,
				XPhotoOCRLimit:      xLimit,
				LinksEnabled:        !skipLinks,
				LinkDiscoverLimit:   linkDiscoverLimit,
				LinkLimit:           linkLimit,
				LinkConcurrency:     linkConcurrency,
				GitHubEnabled:       !skipGitHub,
				GitHubLimit:         githubLimit,
				YouTubeEnabled:      !skipYouTube,
				YouTubeLimit:        youtubeLimit,
				WatchLater:          watchLater,
				Liked:               liked,
				SourcesEnabled:      !skipSources,
				SourceLimit:         sourceLimit,
				SourceConcurrency:   sourceConcurrency,
				SourceWatch:         watch,
				SourcePollInterval:  pollInterval,
				SourceIdleExitAfter: idleExitAfter,
				SourceMaxCycles:     maxCycles,
				Browser:             browser,
				Profile:             profile,
				Force:               force,
				Summarize:           summarize,
				Model:               model,
				OCRModel:            ocrModel,
				CLI:                 cliProvider,
				Length:              length,
				Timeout:             timeout,
				Logger:              newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
				Progress:            cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			return writeSyncStats(cmd.OutOrStdout(), stats)
		},
	}

	cmd.Flags().IntVar(&xBookmarksLimit, "x-bookmarks-limit", 0, "Optional direct X bookmark import limit for smoke runs")
	cmd.Flags().IntVar(&xLimit, "x-limit", 100, "Maximum X items to hydrate per run")
	cmd.Flags().IntVar(&xConcurrency, "x-concurrency", 4, "Number of concurrent X post fetches")
	cmd.Flags().DurationVar(&xTimeout, "x-timeout", 30*time.Second, "Timeout for X browser helpers and HTTP requests")
	cmd.Flags().StringVar(&ocrModel, "ocr-model", "openrouter/google/gemini-3.1-flash-lite-preview", "Model override for X photo OCR")
	cmd.Flags().IntVar(&linkDiscoverLimit, "link-discover-limit", 500, "Maximum imported items to scan for outbound links")
	cmd.Flags().IntVar(&linkLimit, "link-limit", 100, "Maximum deduped discovered sources to enrich per link extraction run")
	cmd.Flags().IntVar(&linkConcurrency, "link-concurrency", 4, "Number of concurrent link source extract/summarize jobs")
	cmd.Flags().IntVar(&githubLimit, "github-limit", 0, "Maximum starred repositories to process before stopping")
	cmd.Flags().IntVar(&youtubeLimit, "youtube-limit", 50, "Maximum videos to load from each selected YouTube feed")
	cmd.Flags().IntVar(&sourceLimit, "source-limit", 100, "Maximum queued sources to enrich per source-worker batch")
	cmd.Flags().IntVar(&sourceConcurrency, "source-concurrency", 4, "Number of concurrent source extract/summarize jobs per batch")
	cmd.Flags().StringVar(&browser, "browser", "chrome", "Preferred browser for cookie-backed X and YouTube flows")
	cmd.Flags().StringVar(&profile, "profile", "", "Browser profile override; requires --browser")
	cmd.Flags().BoolVar(&watchLater, "watch-later", true, "Import Watch Later YouTube videos")
	cmd.Flags().BoolVar(&liked, "liked", true, "Import liked YouTube videos")
	cmd.Flags().BoolVar(&watch, "watch", false, "Keep polling the source backlog instead of exiting when drained")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", 30*time.Second, "How often to poll for new source backlog while watching")
	cmd.Flags().DurationVar(&idleExitAfter, "idle-exit-after", 0, "Optional maximum idle watch duration before exiting")
	cmd.Flags().IntVar(&maxCycles, "max-cycles", 0, "Optional maximum source worker cycles before exiting")
	cmd.Flags().BoolVar(&force, "force", false, "Reprocess existing items and sources instead of only incrementally handling new or stale work")
	cmd.Flags().BoolVar(&summarize, "summarize", true, "Run summarize.sh summarization during import and enrichment stages")
	cmd.Flags().StringVar(&model, "model", "", "Optional summarize model override")
	cmd.Flags().StringVar(&cliProvider, "cli", defaultCLIProvider, "Summarize CLI provider")
	cmd.Flags().StringVar(&length, "length", "medium", "Summary length for summarize.sh")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Timeout for summarize-backed extraction and summarization stages")
	cmd.Flags().BoolVar(&skipXBookmarks, "skip-x-bookmarks", false, "Skip direct X bookmark import")
	cmd.Flags().BoolVar(&skipFT, "skip-ft", false, "Deprecated alias for --skip-x-bookmarks")
	_ = cmd.Flags().MarkHidden("skip-ft")
	cmd.Flags().BoolVar(&skipX, "skip-x", false, "Skip X hydration")
	cmd.Flags().BoolVar(&skipXMedia, "skip-x-media", false, "Skip X media audio transcription")
	cmd.Flags().BoolVar(&skipXPhotoOCR, "skip-x-photo-ocr", false, "Skip X photo OCR / vision extraction")
	cmd.Flags().BoolVar(&skipLinks, "skip-links", false, "Skip outbound link discovery and enrichment from imported items")
	cmd.Flags().BoolVar(&skipGitHub, "skip-github", false, "Skip GitHub stars import")
	cmd.Flags().BoolVar(&skipYouTube, "skip-youtube", false, "Skip YouTube signal import")
	cmd.Flags().BoolVar(&skipSources, "skip-sources", false, "Skip the final source backlog worker stage")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print sync stats as JSON")

	return cmd
}

func writeSyncStats(dst interface{ Write([]byte) (int, error) }, stats syncjob.Stats) error {
	if _, err := fmt.Fprintf(dst, "Started: %s\n", stats.StartedAt.Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Completed: %s\n", stats.CompletedAt.Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Duration: %s\n", stats.Duration); err != nil {
		return err
	}

	if stats.XBookmarks != nil {
		if _, err := fmt.Fprintf(dst, "X Bookmarks: created=%d updated=%d unchanged=%d rendered=%d pages=%d stopped=%s\n", stats.XBookmarks.Stats.Created, stats.XBookmarks.Stats.Updated, stats.XBookmarks.Stats.Unchanged, stats.XBookmarks.Stats.Rendered, stats.XBookmarks.Stats.PagesFetched, stats.XBookmarks.Stats.StoppedReason); err != nil {
			return err
		}
	}
	if stats.X != nil {
		if _, err := fmt.Fprintf(dst, "X: hydrated=%d missing=%d api_errors=%d media_downloaded=%d media_errors=%d rendered=%d\n", stats.X.Stats.Hydrated, stats.X.Stats.Missing, stats.X.Stats.APIErrors, stats.X.Stats.MediaDownloaded, stats.X.Stats.MediaErrors, stats.X.Stats.Rendered); err != nil {
			return err
		}
	}
	if stats.XMedia != nil {
		if _, err := fmt.Fprintf(dst, "X Media: items_processed=%d items_updated=%d items_skipped=%d media_transcribed=%d items_summarized=%d errors=%d summary_errors=%d\n", stats.XMedia.Stats.ItemsProcessed, stats.XMedia.Stats.ItemsUpdated, stats.XMedia.Stats.ItemsSkipped, stats.XMedia.Stats.MediaTranscribed, stats.XMedia.Stats.ItemsSummarized, stats.XMedia.Stats.Errors, stats.XMedia.Stats.SummaryErrors); err != nil {
			return err
		}
	}
	if stats.XPhotoOCR != nil {
		if _, err := fmt.Fprintf(dst, "X Photo OCR: items_processed=%d items_updated=%d items_skipped=%d photos_ocred=%d errors=%d\n", stats.XPhotoOCR.Stats.ItemsProcessed, stats.XPhotoOCR.Stats.ItemsUpdated, stats.XPhotoOCR.Stats.ItemsSkipped, stats.XPhotoOCR.Stats.PhotosOCRed, stats.XPhotoOCR.Stats.Errors); err != nil {
			return err
		}
	}
	if stats.Links != nil {
		if _, err := fmt.Fprintf(dst, "Links: items_scanned=%d sources_queued=%d sources_summarized=%d errors=%d\n", stats.Links.Stats.ItemsScanned, stats.Links.Stats.SourcesQueued, stats.Links.Stats.SourcesSummarized, stats.Links.Stats.Errors); err != nil {
			return err
		}
	}
	if stats.GitHub != nil {
		if _, err := fmt.Fprintf(dst, "GitHub: stars=%d items_created=%d sources_summarized=%d errors=%d\n", stats.GitHub.Stats.StarsProcessed, stats.GitHub.Stats.ItemsCreated, stats.GitHub.Stats.SourcesSummarized, stats.GitHub.Stats.Errors); err != nil {
			return err
		}
	}
	if stats.YouTube != nil {
		if _, err := fmt.Fprintf(dst, "YouTube: items_processed=%d sources_summarized=%d errors=%d\n", stats.YouTube.Stats.ItemsProcessed, stats.YouTube.Stats.SourcesSummarized, stats.YouTube.Stats.Errors); err != nil {
			return err
		}
	}
	if stats.Sources != nil {
		if _, err := fmt.Fprintf(dst, "Sources: work_cycles=%d sources_summarized=%d errors=%d stopped=%s\n", stats.Sources.Stats.WorkCycles, stats.Sources.Stats.SourcesSummarized, stats.Sources.Stats.Errors, stats.Sources.Stats.StoppedReason); err != nil {
			return err
		}
	}

	return nil
}
