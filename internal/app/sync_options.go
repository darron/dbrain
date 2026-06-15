package app

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/syncjob"
)

type syncAllFlags struct {
	xBookmarksLimit int
	xLimit          int
	xMediaLimit     int
	xPhotoOCRLimit  int
	xConcurrency    int
	xTimeout        time.Duration
	xMediaTimeout   time.Duration
	ocrModel        string

	linkDiscoverLimit int
	linkLimit         int
	linkConcurrency   int

	githubLimit int

	youtubeLimit int
	watchLater   bool
	liked        bool

	appleNotes                   bool
	appleNotesDBPath             string
	appleNotesLimit              int
	appleNotesExcludeFolders     []string
	appleNotesExcludeAccounts    []string
	appleNotesExcludeShared      bool
	appleNotesIncludeLocked      bool
	appleNotesSkipAttachments    bool
	appleNotesSkipAttachmentOCR  bool
	appleNotesAttachmentMaxBytes int64
	appleNotesTesseractBinary    string
	safariTabs                   bool
	safariTabsDBPath             string
	safariTabsDevice             string
	safariTabsLimit              int
	safariTabsOlderThan          time.Duration
	feedLimit                    int
	sourceLimit                  int
	sourceConcurrency            int
	browser                      string
	profile                      string
	watch                        bool
	pollInterval                 time.Duration
	idleExitAfter                time.Duration
	maxCycles                    int
	force                        bool
	summarize                    bool
	model                        string
	cliProvider                  string
	length                       string
	timeout                      time.Duration
	archiveMedia                 bool
	archiveMediaLimit            int
	categorizeLimit              int
	categorizeConcurrency        int
	categorizeModel              string
	categorizeTimeout            time.Duration
	categorizeImages             bool
	okfExport                    bool
	skipXBookmarks               bool
	skipX                        bool
	skipXMedia                   bool
	skipXPhotoOCR                bool
	skipLinks                    bool
	skipGitHub                   bool
	skipYouTube                  bool
	skipAppleNotes               bool
	skipSafariTabs               bool
	skipFeeds                    bool
	skipSources                  bool
	skipCategorize               bool
	skipOKFExport                bool
	jsonOut                      bool
}

func syncOptionsFromFlags(ctx context.Context, cfg config.Config, flags syncAllFlags, logger *slog.Logger, progress io.Writer) (syncjob.Options, error) {
	if !flags.skipGitHub {
		if err := preflightRequireGitHub(ctx, cfg); err != nil {
			return syncjob.Options{}, err
		}
	}
	if !flags.skipXPhotoOCR {
		if err := preflightOCRModel(ctx, cfg, flags.ocrModel); err != nil {
			return syncjob.Options{}, err
		}
	}
	if flags.summarize && !flags.skipSources {
		if err := preflightSummaryModel(ctx, cfg, flags.model); err != nil {
			return syncjob.Options{}, err
		}
	}
	if !flags.skipCategorize {
		if err := preflightRequireOpenRouterForModel(ctx, cfg, flags.categorizeModel); err != nil {
			return syncjob.Options{}, err
		}
	}
	if flags.archiveMedia || preflightR2Configured(cfg) {
		if err := preflightRequireR2(ctx, cfg); err != nil {
			return syncjob.Options{}, err
		}
	}

	var archiveAccessKeyID string
	var archiveSecretKey string
	var archiveSessionToken string
	if flags.archiveMedia {
		var err error
		archiveAccessKeyID, err = firstNonEmptySecret(ctx, cfg.RootDir, "DBRAIN_R2_ACCESS_KEY_ID", "DBRAIN_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")
		if err != nil {
			return syncjob.Options{}, err
		}
		archiveSecretKey, err = firstNonEmptySecret(ctx, cfg.RootDir, "DBRAIN_R2_SECRET_ACCESS_KEY", "DBRAIN_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")
		if err != nil {
			return syncjob.Options{}, err
		}
		archiveSessionToken, err = firstNonEmptySecret(ctx, cfg.RootDir, "DBRAIN_R2_SESSION_TOKEN", "DBRAIN_S3_SESSION_TOKEN", "AWS_SESSION_TOKEN")
		if err != nil {
			return syncjob.Options{}, err
		}
	}
	return syncjob.Options{
		XBookmarksEnabled:            !flags.skipXBookmarks,
		XBookmarksLimit:              flags.xBookmarksLimit,
		XEnabled:                     !flags.skipX,
		XLimit:                       flags.xLimit,
		XConcurrency:                 flags.xConcurrency,
		XTimeout:                     flags.xTimeout,
		XMediaTimeout:                flags.xMediaTimeout,
		XMediaEnabled:                !flags.skipXMedia,
		XMediaLimit:                  flags.xMediaLimit,
		XPhotoOCREnabled:             !flags.skipXPhotoOCR,
		XPhotoOCRLimit:               flags.xPhotoOCRLimit,
		LinksEnabled:                 !flags.skipLinks,
		LinkDiscoverLimit:            flags.linkDiscoverLimit,
		LinkLimit:                    flags.linkLimit,
		LinkConcurrency:              flags.linkConcurrency,
		GitHubEnabled:                !flags.skipGitHub,
		GitHubLimit:                  flags.githubLimit,
		YouTubeEnabled:               !flags.skipYouTube,
		YouTubeLimit:                 flags.youtubeLimit,
		WatchLater:                   flags.watchLater,
		Liked:                        flags.liked,
		AppleNotesEnabled:            flags.appleNotes && !flags.skipAppleNotes,
		AppleNotesDBPath:             flags.appleNotesDBPath,
		AppleNotesLimit:              flags.appleNotesLimit,
		AppleNotesExcludeFolders:     flags.appleNotesExcludeFolders,
		AppleNotesExcludeAccounts:    flags.appleNotesExcludeAccounts,
		AppleNotesExcludeShared:      flags.appleNotesExcludeShared,
		AppleNotesIncludeLocked:      flags.appleNotesIncludeLocked,
		AppleNotesSkipAttachments:    flags.appleNotesSkipAttachments,
		AppleNotesSkipAttachmentOCR:  flags.appleNotesSkipAttachmentOCR,
		AppleNotesAttachmentMaxBytes: flags.appleNotesAttachmentMaxBytes,
		AppleNotesTesseractBinary:    flags.appleNotesTesseractBinary,
		SafariTabsEnabled:            flags.safariTabs && !flags.skipSafariTabs,
		SafariTabsDBPath:             flags.safariTabsDBPath,
		SafariTabsDevice:             flags.safariTabsDevice,
		SafariTabsLimit:              flags.safariTabsLimit,
		SafariTabsOlderThan:          flags.safariTabsOlderThan,
		FeedsEnabled:                 !flags.skipFeeds,
		FeedLimit:                    flags.feedLimit,
		SourcesEnabled:               !flags.skipSources,
		SourceLimit:                  flags.sourceLimit,
		SourceConcurrency:            flags.sourceConcurrency,
		SourceWatch:                  flags.watch,
		SourcePollInterval:           flags.pollInterval,
		SourceIdleExitAfter:          flags.idleExitAfter,
		SourceMaxCycles:              flags.maxCycles,
		Browser:                      flags.browser,
		Profile:                      flags.profile,
		Force:                        flags.force,
		Summarize:                    flags.summarize,
		Model:                        flags.model,
		OCRModel:                     flags.ocrModel,
		ArchiveMediaEnabled:          flags.archiveMedia,
		ArchiveMediaLimit:            flags.archiveMediaLimit,
		ArchiveProvider:              firstNonEmptyEnv(cfg.RootDir, "DBRAIN_ARCHIVE_PROVIDER", "DBRAIN_R2_PROVIDER"),
		ArchiveBucket:                firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_BUCKET", "DBRAIN_ARCHIVE_BUCKET"),
		ArchivePublicBaseURL:         firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_PUBLIC_BASE_URL", "DBRAIN_MEDIA_PUBLIC_BASE_URL"),
		ArchiveEndpoint:              firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_ENDPOINT", "DBRAIN_S3_ENDPOINT"),
		ArchiveRegion:                firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_REGION", "DBRAIN_S3_REGION"),
		ArchiveAccessKeyID:           archiveAccessKeyID,
		ArchiveSecretKey:             archiveSecretKey,
		ArchiveSessionToken:          archiveSessionToken,
		CategorizeEnabled:            !flags.skipCategorize,
		CategorizeLimit:              flags.categorizeLimit,
		CategorizeConcurrency:        flags.categorizeConcurrency,
		CategorizeModel:              flags.categorizeModel,
		CategorizeTimeout:            flags.categorizeTimeout,
		CategorizeImages:             flags.categorizeImages,
		OKFExportEnabled:             flags.okfExport && !flags.skipOKFExport,
		OKFMediaProxyBaseURL:         firstNonEmptyEnv(cfg.RootDir, "DBRAIN_MEDIA_PROXY_BASE_URL", "DBRAIN_WEB_BASE_URL", "DBRAIN_AUTH_BASE_URL"),
		CLI:                          flags.cliProvider,
		Length:                       flags.length,
		Timeout:                      flags.timeout,
		Logger:                       logger,
		Progress:                     progress,
	}, nil
}
