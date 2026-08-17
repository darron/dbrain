package syncjob

import (
	"context"
	"errors"
	"fmt"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

type syncStageID string

const (
	syncStageAppleNotes        syncStageID = "apple_notes"
	syncStageSafariTabs        syncStageID = "safari_tabs"
	syncStageBlueskyBookmarks  syncStageID = "bluesky_bookmarks"
	syncStageMastodonBookmarks syncStageID = "mastodon_bookmarks"
	syncStageXFrontier         syncStageID = "x_frontier"
	syncStageXMedia            syncStageID = "x_media"
	syncStageXPhotoOCR         syncStageID = "x_photo_ocr"
	syncStageGitHub            syncStageID = "github"
	syncStageYouTube           syncStageID = "youtube"
	syncStageFeeds             syncStageID = "feeds"
	syncStageSources           syncStageID = "sources"
	syncStageCategorize        syncStageID = "categorize"
	syncStageMediaArchive      syncStageID = "media_archive"
	syncStageOKFExport         syncStageID = "okf_export"
)

type syncStage struct {
	ID      syncStageID
	After   []syncStageID
	Enabled func(stageOptions) bool
	Run     func(context.Context, config.Config, *store.Store, stageOptions, *Stats) error
}

func defaultSyncStagePlan() []syncStage {
	return []syncStage{
		{
			ID:      syncStageAppleNotes,
			Enabled: func(opts stageOptions) bool { return opts.AppleNotes.Enabled },
			Run:     runAppleNotesSyncStage,
		},
		{
			ID:      syncStageSafariTabs,
			After:   []syncStageID{syncStageAppleNotes},
			Enabled: func(opts stageOptions) bool { return opts.SafariTabs.Enabled },
			Run:     runSafariTabsSyncStage,
		},
		{
			ID: syncStageBlueskyBookmarks,
			After: []syncStageID{
				syncStageAppleNotes,
				syncStageSafariTabs,
			},
			Enabled: func(opts stageOptions) bool { return opts.BlueskyBookmarks.Enabled },
			Run:     runBlueskyBookmarksSyncStage,
		},
		{
			ID: syncStageMastodonBookmarks,
			After: []syncStageID{
				syncStageAppleNotes,
				syncStageSafariTabs,
				syncStageBlueskyBookmarks,
			},
			Enabled: func(opts stageOptions) bool { return opts.MastodonBookmarks.Enabled },
			Run:     runMastodonBookmarksSyncStage,
		},
		{
			ID: syncStageXFrontier,
			After: []syncStageID{
				syncStageAppleNotes,
				syncStageSafariTabs,
				syncStageBlueskyBookmarks,
				syncStageMastodonBookmarks,
			},
			Enabled: syncXFrontierEnabled,
			Run:     runXFrontierSyncStage,
		},
		{
			ID: syncStageXMedia,
			After: []syncStageID{
				syncStageBlueskyBookmarks,
				syncStageMastodonBookmarks,
				syncStageXFrontier,
			},
			Enabled: func(opts stageOptions) bool { return opts.XMedia.Enabled },
			Run:     runXMediaSyncStage,
		},
		{
			ID: syncStageXPhotoOCR,
			After: []syncStageID{
				syncStageBlueskyBookmarks,
				syncStageMastodonBookmarks,
				syncStageXFrontier,
			},
			Enabled: func(opts stageOptions) bool { return opts.XPhotoOCR.Enabled },
			Run:     runXPhotoOCRSyncStage,
		},
		{
			ID:      syncStageGitHub,
			Enabled: func(opts stageOptions) bool { return opts.GitHub.Enabled },
			Run:     runGitHubSyncStage,
		},
		{
			ID:      syncStageYouTube,
			Enabled: func(opts stageOptions) bool { return opts.YouTube.Enabled },
			Run:     runYouTubeSyncStage,
		},
		{
			ID: syncStageFeeds,
			After: []syncStageID{
				syncStageGitHub,
				syncStageYouTube,
			},
			Enabled: func(opts stageOptions) bool { return opts.Feeds.Enabled },
			Run:     runFeedsSyncStage,
		},
		{
			ID:      syncStageSources,
			After:   []syncStageID{syncStageBlueskyBookmarks, syncStageMastodonBookmarks, syncStageXFrontier, syncStageGitHub, syncStageYouTube, syncStageFeeds},
			Enabled: func(opts stageOptions) bool { return opts.Sources.Enabled },
			Run:     runSourcesSyncStage,
		},
		{
			ID:      syncStageCategorize,
			After:   []syncStageID{syncStageXMedia, syncStageXPhotoOCR, syncStageSources},
			Enabled: func(opts stageOptions) bool { return opts.Categorize.Enabled },
			Run:     runCategorizeSyncStage,
		},
		{
			ID: syncStageMediaArchive,
			After: []syncStageID{
				syncStageXMedia,
				syncStageXPhotoOCR,
				syncStageCategorize,
			},
			Enabled: func(opts stageOptions) bool { return opts.Archive.Enabled },
			Run:     runMediaArchiveSyncStage,
		},
		{
			ID:      syncStageOKFExport,
			After:   []syncStageID{syncStageCategorize, syncStageMediaArchive},
			Enabled: func(opts stageOptions) bool { return opts.OKFExport.Enabled },
			Run:     runOKFExportSyncStage,
		},
	}
}

func runSyncStagePlan(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	return runSyncStagePlanWithPlan(ctx, cfg, st, opts, stats, defaultSyncStagePlan())
}

func runSyncStagePlanWithPlan(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats, plan []syncStage) error {
	var stageErrors []error
	for _, stage := range plan {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !stage.Enabled(opts) {
			continue
		}
		// After is an ordering constraint validated by validateSyncStagePlan, not
		// a success gate: later enabled stages still drain their own work after
		// an ordinary upstream stage error.
		if err := stage.Run(ctx, cfg, st, opts, stats); err != nil {
			stageErr := WrapStageError(string(stage.ID), err)
			emitSyncStageMetrics(opts.Common.Metrics, opts, stats, stage.ID, stageErr)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			stageErrors = append(stageErrors, stageErr)
			continue
		}
		emitSyncStageMetrics(opts.Common.Metrics, opts, stats, stage.ID, nil)
	}
	return errors.Join(stageErrors...)
}

func validateSyncStagePlan(plan []syncStage) error {
	seen := map[syncStageID]struct{}{}
	for _, stage := range plan {
		if _, ok := seen[stage.ID]; ok {
			return fmt.Errorf("duplicate sync stage %q", stage.ID)
		}
		for _, dependency := range stage.After {
			if _, ok := seen[dependency]; !ok {
				return fmt.Errorf("sync stage %q must run after unknown or later stage %q", stage.ID, dependency)
			}
		}
		seen[stage.ID] = struct{}{}
	}
	return nil
}

func syncXFrontierEnabled(opts stageOptions) bool {
	return opts.XBookmarks.Enabled || opts.X.Enabled || opts.Links.Enabled
}

func runAppleNotesSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	stage, err := executeAppleNotesStage(ctx, cfg, st, opts)
	stats.AppleNotes = stage
	return err
}

func runSafariTabsSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	stage, err := executeSafariTabsStage(ctx, cfg, st, opts)
	stats.SafariTabs = stage
	return err
}

func runBlueskyBookmarksSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	stage, err := executeBlueskyBookmarksStage(ctx, cfg, st, opts)
	stats.BlueskyBookmarks = stage
	return err
}

func runMastodonBookmarksSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	stage, err := executeMastodonBookmarksStage(ctx, cfg, st, opts)
	stats.MastodonBookmarks = stage
	return err
}

func runXFrontierSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	return executeXFrontierStages(ctx, cfg, st, opts, stats)
}

func runXMediaSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	stage, err := executeXMediaStage(ctx, cfg, st, opts)
	stats.XMedia = stage
	return err
}

func runXPhotoOCRSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	stage, err := executeXPhotoOCRStage(ctx, cfg, st, opts)
	stats.XPhotoOCR = stage
	return err
}

func runGitHubSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	stage, err := executeGitHubStage(ctx, cfg, st, opts)
	stats.GitHub = stage
	return err
}

func runYouTubeSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	stage, err := executeYouTubeStage(ctx, cfg, st, opts)
	stats.YouTube = stage
	return err
}

func runFeedsSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	stage, err := executeFeedsStage(ctx, cfg, st, opts)
	stats.Feeds = stage
	return err
}

func runSourcesSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	stage, err := executeSourcesStage(ctx, cfg, st, opts)
	stats.Sources = stage
	return err
}

func runCategorizeSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	stage, err := executeCategorizeStage(ctx, cfg, st, opts)
	stats.Categorize = stage
	return err
}

func runMediaArchiveSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	stage, err := executeMediaArchiveStage(ctx, cfg, st, opts)
	stats.MediaArchive = stage
	return err
}

func runOKFExportSyncStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions, stats *Stats) error {
	stage, err := executeOKFExportStage(ctx, cfg, st, opts)
	stats.OKFExport = stage
	return err
}
