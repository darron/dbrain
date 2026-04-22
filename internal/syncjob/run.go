package syncjob

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/ftimport"
	"dbrain/internal/githubimport"
	"dbrain/internal/linkextract"
	"dbrain/internal/sourceenrich"
	"dbrain/internal/store"
	"dbrain/internal/summarizecli"
	"dbrain/internal/worker"
	"dbrain/internal/xapi"
	"dbrain/internal/youtubeimport"
)

var (
	runFTImport      = ftimport.Run
	runXHydrate      = xapi.Run
	runLinkExtract   = linkextract.Run
	runGitHubImport  = githubimport.Run
	runYouTubeImport = youtubeimport.Run
	runSourceWorker  = worker.RunSources
	summarizeVersion = summarizecli.Version
)

type Options struct {
	FTEnabled bool
	FTSource  string
	FTLimit   int

	XEnabled     bool
	XLimit       int
	XConcurrency int
	XTimeout     time.Duration

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

	Browser   string
	Profile   string
	Force     bool
	Summarize bool
	Model     string
	CLI       string
	Length    string
	Timeout   time.Duration
	Logger    *slog.Logger
	Progress  io.Writer
}

type Stats struct {
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt time.Time     `json:"completed_at,omitempty"`
	Duration    time.Duration `json:"duration"`
	FT          *FTStage      `json:"ft,omitempty"`
	X           *XStage       `json:"x,omitempty"`
	Links       *LinksStage   `json:"links,omitempty"`
	GitHub      *GitHubStage  `json:"github,omitempty"`
	YouTube     *YouTubeStage `json:"youtube,omitempty"`
	Sources     *SourcesStage `json:"sources,omitempty"`
}

type FTStage struct {
	Duration time.Duration  `json:"duration"`
	Stats    ftimport.Stats `json:"stats"`
}

type XStage struct {
	Duration time.Duration `json:"duration"`
	Stats    xapi.Stats    `json:"stats"`
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

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if strings.TrimSpace(opts.FTSource) == "" {
		home, _ := os.UserHomeDir()
		opts.FTSource = filepath.Join(home, ".ft-bookmarks", "bookmarks.db")
	}
	if strings.TrimSpace(opts.Browser) == "" {
		opts.Browser = "chrome"
	}
	if strings.TrimSpace(opts.Length) == "" {
		opts.Length = "medium"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	if opts.XTimeout <= 0 {
		opts.XTimeout = 30 * time.Second
	}
	if opts.XLimit <= 0 {
		opts.XLimit = 100
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

	stats := Stats{StartedAt: time.Now().UTC()}
	progressf(opts.Progress, "Sync started at %s\n", stats.StartedAt.Format(time.RFC3339))

	if opts.FTEnabled {
		progressf(opts.Progress, "==> import ft\n")
		start := time.Now()
		ftStats, err := runFTImport(ctx, cfg, st, ftimport.Options{
			SourcePath: opts.FTSource,
			Limit:      opts.FTLimit,
		})
		stage := &FTStage{Duration: time.Since(start), Stats: ftStats}
		stats.FT = stage
		if err != nil {
			return finishStats(stats), fmt.Errorf("import ft: %w", err)
		}
		progressf(opts.Progress, "FT import complete: created=%d updated=%d unchanged=%d rendered=%d (%s)\n", ftStats.Created, ftStats.Updated, ftStats.Unchanged, ftStats.Rendered, stage.Duration)
	}

	if opts.XEnabled {
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
		stage := &XStage{Duration: time.Since(start), Stats: xStats}
		stats.X = stage
		if err != nil {
			return finishStats(stats), fmt.Errorf("hydrate x: %w", err)
		}
		progressf(opts.Progress, "X hydration complete: hydrated=%d missing=%d api_errors=%d media_downloaded=%d media_errors=%d rendered=%d (%s)\n", xStats.Hydrated, xStats.Missing, xStats.APIErrors, xStats.MediaDownloaded, xStats.MediaErrors, xStats.Rendered, stage.Duration)
	}

	if opts.LinksEnabled {
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
		stage := &LinksStage{Duration: time.Since(start), Stats: linkStats}
		stats.Links = stage
		if err != nil {
			return finishStats(stats), fmt.Errorf("extract links: %w", err)
		}
		progressf(opts.Progress, "Link extraction complete: items_scanned=%d sources_queued=%d sources_summarized=%d errors=%d (%s)\n", linkStats.ItemsScanned, linkStats.SourcesQueued, linkStats.SourcesSummarized, linkStats.Errors, stage.Duration)
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
		toolVersion := summarizeVersion(ctx, "")
		sourceStats, err := runSourceWorker(
			ctx,
			func(ctx context.Context) (store.BacklogStats, error) {
				return st.Backlog(ctx, sourceenrich.SummaryPromptVersion, summarizecli.ToolName, toolVersion)
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
