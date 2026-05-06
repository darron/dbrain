package syncjob

import (
	"io"
	"log/slog"
	"time"
)

type stageOptions struct {
	Common     commonStageOptions
	XBookmarks xBookmarksStageOptions
	X          xHydrationStageOptions
	XMedia     xMediaStageOptions
	XPhotoOCR  xPhotoOCRStageOptions
	Links      linksStageOptions
	GitHub     gitHubStageOptions
	YouTube    youTubeStageOptions
	AppleNotes appleNotesStageOptions
	SafariTabs safariTabsStageOptions
	Sources    sourcesStageOptions
	Archive    archiveStageOptions
	Categorize categorizeStageOptions
}

type commonStageOptions struct {
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

type xBookmarksStageOptions struct {
	Enabled bool
	Limit   int
	Timeout time.Duration
}

type xHydrationStageOptions struct {
	Enabled      bool
	Limit        int
	Concurrency  int
	Timeout      time.Duration
	MediaTimeout time.Duration
}

type xMediaStageOptions struct {
	Enabled bool
	Limit   int
}

type xPhotoOCRStageOptions struct {
	Enabled bool
	Limit   int
	Model   string
}

type linksStageOptions struct {
	Enabled       bool
	DiscoverLimit int
	Limit         int
	Concurrency   int
}

type gitHubStageOptions struct {
	Enabled bool
	Limit   int
}

type youTubeStageOptions struct {
	Enabled    bool
	Limit      int
	WatchLater bool
	Liked      bool
}

type appleNotesStageOptions struct {
	Enabled            bool
	DBPath             string
	Limit              int
	ExcludeFolders     []string
	ExcludeAccounts    []string
	ExcludeShared      bool
	IncludeLocked      bool
	SkipAttachments    bool
	SkipAttachmentOCR  bool
	AttachmentMaxBytes int64
	TesseractBinary    string
}

type safariTabsStageOptions struct {
	Enabled   bool
	DBPath    string
	Device    string
	Limit     int
	OlderThan time.Duration
}

type sourcesStageOptions struct {
	Enabled       bool
	Limit         int
	Concurrency   int
	Watch         bool
	PollInterval  time.Duration
	IdleExitAfter time.Duration
	MaxCycles     int
}

type archiveStageOptions struct {
	Enabled       bool
	Limit         int
	Provider      string
	Bucket        string
	PublicBaseURL string
	Endpoint      string
	Region        string
	AccessKeyID   string
	SecretKey     string
	SessionToken  string
}

type categorizeStageOptions struct {
	Enabled     bool
	Limit       int
	Concurrency int
	Model       string
	Timeout     time.Duration
	Images      bool
}

func newStageOptions(opts Options) stageOptions {
	return stageOptions{
		Common: commonStageOptions{
			Browser:   opts.Browser,
			Profile:   opts.Profile,
			Force:     opts.Force,
			Summarize: opts.Summarize,
			Model:     opts.Model,
			CLI:       opts.CLI,
			Length:    opts.Length,
			Timeout:   opts.Timeout,
			Logger:    opts.Logger,
			Progress:  opts.Progress,
		},
		XBookmarks: xBookmarksStageOptions{
			Enabled: opts.XBookmarksEnabled,
			Limit:   opts.XBookmarksLimit,
			Timeout: opts.XTimeout,
		},
		X: xHydrationStageOptions{
			Enabled:      opts.XEnabled,
			Limit:        opts.XLimit,
			Concurrency:  opts.XConcurrency,
			Timeout:      opts.XTimeout,
			MediaTimeout: opts.XMediaTimeout,
		},
		XMedia: xMediaStageOptions{
			Enabled: opts.XMediaEnabled,
			Limit:   opts.XMediaLimit,
		},
		XPhotoOCR: xPhotoOCRStageOptions{
			Enabled: opts.XPhotoOCREnabled,
			Limit:   opts.XPhotoOCRLimit,
			Model:   opts.OCRModel,
		},
		Links: linksStageOptions{
			Enabled:       opts.LinksEnabled,
			DiscoverLimit: opts.LinkDiscoverLimit,
			Limit:         opts.LinkLimit,
			Concurrency:   opts.LinkConcurrency,
		},
		GitHub: gitHubStageOptions{
			Enabled: opts.GitHubEnabled,
			Limit:   opts.GitHubLimit,
		},
		YouTube: youTubeStageOptions{
			Enabled:    opts.YouTubeEnabled,
			Limit:      opts.YouTubeLimit,
			WatchLater: opts.WatchLater,
			Liked:      opts.Liked,
		},
		AppleNotes: appleNotesStageOptions{
			Enabled:            opts.AppleNotesEnabled,
			DBPath:             opts.AppleNotesDBPath,
			Limit:              opts.AppleNotesLimit,
			ExcludeFolders:     opts.AppleNotesExcludeFolders,
			ExcludeAccounts:    opts.AppleNotesExcludeAccounts,
			ExcludeShared:      opts.AppleNotesExcludeShared,
			IncludeLocked:      opts.AppleNotesIncludeLocked,
			SkipAttachments:    opts.AppleNotesSkipAttachments,
			SkipAttachmentOCR:  opts.AppleNotesSkipAttachmentOCR,
			AttachmentMaxBytes: opts.AppleNotesAttachmentMaxBytes,
			TesseractBinary:    opts.AppleNotesTesseractBinary,
		},
		SafariTabs: safariTabsStageOptions{
			Enabled:   opts.SafariTabsEnabled,
			DBPath:    opts.SafariTabsDBPath,
			Device:    opts.SafariTabsDevice,
			Limit:     opts.SafariTabsLimit,
			OlderThan: opts.SafariTabsOlderThan,
		},
		Sources: sourcesStageOptions{
			Enabled:       opts.SourcesEnabled,
			Limit:         opts.SourceLimit,
			Concurrency:   opts.SourceConcurrency,
			Watch:         opts.SourceWatch,
			PollInterval:  opts.SourcePollInterval,
			IdleExitAfter: opts.SourceIdleExitAfter,
			MaxCycles:     opts.SourceMaxCycles,
		},
		Archive: archiveStageOptions{
			Enabled:       opts.ArchiveMediaEnabled,
			Limit:         opts.ArchiveMediaLimit,
			Provider:      opts.ArchiveProvider,
			Bucket:        opts.ArchiveBucket,
			PublicBaseURL: opts.ArchivePublicBaseURL,
			Endpoint:      opts.ArchiveEndpoint,
			Region:        opts.ArchiveRegion,
			AccessKeyID:   opts.ArchiveAccessKeyID,
			SecretKey:     opts.ArchiveSecretKey,
			SessionToken:  opts.ArchiveSessionToken,
		},
		Categorize: categorizeStageOptions{
			Enabled:     opts.CategorizeEnabled,
			Limit:       opts.CategorizeLimit,
			Concurrency: opts.CategorizeConcurrency,
			Model:       opts.CategorizeModel,
			Timeout:     opts.CategorizeTimeout,
			Images:      opts.CategorizeImages,
		},
	}
}
