package syncjob

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/githubimport"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/worker"
	"github.com/darron/dbrain/internal/xmediatranscribe"
	"github.com/darron/dbrain/internal/xphotoocr"
	"github.com/darron/dbrain/internal/youtubeimport"
)

func executeXMediaStage(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (*XMediaStage, error) {
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
	if err != nil {
		return stage, fmt.Errorf("transcribe x-media: %w", err)
	}
	progressf(opts.Progress, "X media transcription complete: items_processed=%d items_updated=%d items_skipped=%d media_transcribed=%d items_summarized=%d errors=%d summary_errors=%d (%s)\n", xMediaStats.ItemsProcessed, xMediaStats.ItemsUpdated, xMediaStats.ItemsSkipped, xMediaStats.MediaTranscribed, xMediaStats.ItemsSummarized, xMediaStats.Errors, xMediaStats.SummaryErrors, stage.Duration)
	return stage, nil
}

func executeXPhotoOCRStage(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (*XPhotoOCRStage, error) {
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
	if err != nil {
		return stage, fmt.Errorf("ocr x-photos: %w", err)
	}
	progressf(opts.Progress, "X photo OCR complete: items_processed=%d items_updated=%d items_skipped=%d photos_ocred=%d errors=%d (%s)\n", ocrStats.ItemsProcessed, ocrStats.ItemsUpdated, ocrStats.ItemsSkipped, ocrStats.PhotosOCRed, ocrStats.Errors, stage.Duration)
	return stage, nil
}

func executeGitHubStage(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (*GitHubStage, error) {
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
	if err != nil {
		return stage, fmt.Errorf("import github stars: %w", err)
	}
	progressf(opts.Progress, "GitHub stars complete: stars=%d items_created=%d sources_summarized=%d errors=%d (%s)\n", githubStats.StarsProcessed, githubStats.ItemsCreated, githubStats.SourcesSummarized, githubStats.Errors, stage.Duration)
	return stage, nil
}

func executeYouTubeStage(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (*YouTubeStage, error) {
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
	if err != nil {
		return stage, fmt.Errorf("import youtube: %w", err)
	}
	progressf(opts.Progress, "YouTube import complete: items_processed=%d sources_summarized=%d errors=%d (%s)\n", youtubeStats.ItemsProcessed, youtubeStats.SourcesSummarized, youtubeStats.Errors, stage.Duration)
	return stage, nil
}

func executeSourcesStage(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (*SourcesStage, error) {
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
	if err != nil {
		return stage, fmt.Errorf("worker sources: %w", err)
	}
	progressf(opts.Progress, "Source worker complete: work_cycles=%d sources_summarized=%d errors=%d stopped=%s (%s)\n", sourceStats.WorkCycles, sourceStats.SourcesSummarized, sourceStats.Errors, sourceStats.StoppedReason, stage.Duration)
	return stage, nil
}

func executeMediaArchiveStage(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (*MediaArchiveStage, error) {
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
	if err != nil {
		return stage, fmt.Errorf("archive media: %w", err)
	}
	progressf(opts.Progress, "Media archive complete: candidates=%d uploaded=%d archived=%d unchanged=%d prune_skipped=%d local_files_pruned=%d local_rows_pruned=%d errors=%d (%s)\n", archiveStats.Candidates, archiveStats.Uploaded, archiveStats.Archived, archiveStats.Unchanged, archiveStats.PruneSkipped, archiveStats.LocalFilesPruned, archiveStats.LocalRowsPruned, archiveStats.Errors, stage.Duration)
	return stage, nil
}
