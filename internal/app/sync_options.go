package app

import (
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
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
	skipXBookmarks               bool
	skipX                        bool
	skipXMedia                   bool
	skipXPhotoOCR                bool
	skipLinks                    bool
	skipGitHub                   bool
	skipYouTube                  bool
	skipAppleNotes               bool
	skipSafariTabs               bool
	skipSources                  bool
	skipCategorize               bool
	jsonOut                      bool
}

func resolveSyncAllFlags(rootDir string, flags syncAllFlags) (syncAllFlags, error) {
	if !flags.archiveMedia {
		flags.archiveMedia = firstEnvBool(rootDir, "DBRAIN_AUTO_ARCHIVE_MEDIA", "DBRAIN_ARCHIVE_AUTO")
	}
	if !flags.appleNotes {
		flags.appleNotes = firstEnvBool(rootDir, "DBRAIN_APPLE_NOTES_ENABLED")
	}
	if strings.TrimSpace(flags.appleNotesDBPath) == "" {
		flags.appleNotesDBPath = firstNonEmptyEnv(rootDir, "DBRAIN_APPLE_NOTES_DB_PATH")
	}
	if len(flags.appleNotesExcludeFolders) == 0 {
		flags.appleNotesExcludeFolders = firstEnvList(rootDir, "DBRAIN_APPLE_NOTES_EXCLUDE_FOLDERS")
	}
	if len(flags.appleNotesExcludeAccounts) == 0 {
		flags.appleNotesExcludeAccounts = firstEnvList(rootDir, "DBRAIN_APPLE_NOTES_EXCLUDE_ACCOUNTS")
	}
	if !flags.appleNotesExcludeShared {
		flags.appleNotesExcludeShared = firstEnvBool(rootDir, "DBRAIN_APPLE_NOTES_EXCLUDE_SHARED")
	}
	if !flags.appleNotesSkipAttachments {
		if value := firstNonEmptyEnv(rootDir, "DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS"); value != "" {
			parsed, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				return syncAllFlags{}, fmt.Errorf("parse DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS: %q", value)
			}
			flags.appleNotesSkipAttachments = !parsed
		}
		if firstEnvBool(rootDir, "DBRAIN_APPLE_NOTES_SKIP_ATTACHMENTS") {
			flags.appleNotesSkipAttachments = true
		}
	}
	if !flags.appleNotesSkipAttachmentOCR {
		if value := firstNonEmptyEnv(rootDir, "DBRAIN_APPLE_NOTES_ATTACHMENT_OCR"); value != "" {
			parsed, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				return syncAllFlags{}, fmt.Errorf("parse DBRAIN_APPLE_NOTES_ATTACHMENT_OCR: %q", value)
			}
			flags.appleNotesSkipAttachmentOCR = !parsed
		}
		if firstEnvBool(rootDir, "DBRAIN_APPLE_NOTES_SKIP_ATTACHMENT_OCR") {
			flags.appleNotesSkipAttachmentOCR = true
		}
	}
	if flags.appleNotesAttachmentMaxBytes <= 0 {
		if value := firstNonEmptyEnv(rootDir, "DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES"); value != "" {
			parsed, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr != nil || parsed < 0 {
				return syncAllFlags{}, fmt.Errorf("parse DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES: %q", value)
			}
			flags.appleNotesAttachmentMaxBytes = parsed
		}
	}
	if strings.TrimSpace(flags.appleNotesTesseractBinary) == "" {
		flags.appleNotesTesseractBinary = firstNonEmptyEnv(rootDir, "DBRAIN_APPLE_NOTES_TESSERACT_BINARY")
	}
	if !flags.safariTabs {
		flags.safariTabs = firstEnvBool(rootDir, "DBRAIN_SAFARI_TABS_ENABLED")
	}
	if strings.TrimSpace(flags.safariTabsDBPath) == "" {
		flags.safariTabsDBPath = firstNonEmptyEnv(rootDir, "DBRAIN_SAFARI_TABS_DB_PATH")
	}
	if strings.TrimSpace(flags.safariTabsDevice) == "" {
		flags.safariTabsDevice = firstNonEmptyEnv(rootDir, "DBRAIN_SAFARI_TABS_DEVICE")
	}
	if flags.safariTabsLimit <= 0 {
		if value := firstNonEmptyEnv(rootDir, "DBRAIN_SAFARI_TABS_LIMIT"); value != "" {
			parsed, parseErr := strconv.Atoi(value)
			if parseErr != nil || parsed < 0 {
				return syncAllFlags{}, fmt.Errorf("parse DBRAIN_SAFARI_TABS_LIMIT: %q", value)
			}
			flags.safariTabsLimit = parsed
		}
	}
	if flags.safariTabsOlderThan <= 0 {
		if value := firstNonEmptyEnv(rootDir, "DBRAIN_SAFARI_TABS_OLDER_THAN"); value != "" {
			parsed, parseErr := time.ParseDuration(value)
			if parseErr != nil || parsed < 0 {
				return syncAllFlags{}, fmt.Errorf("parse DBRAIN_SAFARI_TABS_OLDER_THAN: %q", value)
			}
			flags.safariTabsOlderThan = parsed
		}
	}
	return flags, nil
}

func syncOptionsFromFlags(cfg config.Config, flags syncAllFlags, logger *slog.Logger, progress io.Writer) syncjob.Options {
	return syncjob.Options{
		XBookmarksEnabled:            !flags.skipXBookmarks,
		XBookmarksLimit:              flags.xBookmarksLimit,
		XEnabled:                     !flags.skipX,
		XLimit:                       flags.xLimit,
		XConcurrency:                 flags.xConcurrency,
		XTimeout:                     flags.xTimeout,
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
		ArchiveAccessKeyID:           firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_ACCESS_KEY_ID", "DBRAIN_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID"),
		ArchiveSecretKey:             firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_SECRET_ACCESS_KEY", "DBRAIN_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY"),
		ArchiveSessionToken:          firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_SESSION_TOKEN", "DBRAIN_S3_SESSION_TOKEN", "AWS_SESSION_TOKEN"),
		CategorizeEnabled:            !flags.skipCategorize,
		CategorizeLimit:              flags.categorizeLimit,
		CategorizeConcurrency:        flags.categorizeConcurrency,
		CategorizeModel:              flags.categorizeModel,
		CategorizeTimeout:            flags.categorizeTimeout,
		CategorizeImages:             flags.categorizeImages,
		CLI:                          flags.cliProvider,
		Length:                       flags.length,
		Timeout:                      flags.timeout,
		Logger:                       logger,
		Progress:                     progress,
	}
}
