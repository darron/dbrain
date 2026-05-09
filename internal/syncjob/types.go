package syncjob

import (
	"io"
	"log/slog"
	"time"

	"github.com/darron/dbrain/internal/applenotes"
	"github.com/darron/dbrain/internal/feedimport"
	"github.com/darron/dbrain/internal/githubimport"
	"github.com/darron/dbrain/internal/itemcategorize"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/safaritabs"
	"github.com/darron/dbrain/internal/worker"
	"github.com/darron/dbrain/internal/xapi"
	"github.com/darron/dbrain/internal/xmediatranscribe"
	"github.com/darron/dbrain/internal/xphotoocr"
	"github.com/darron/dbrain/internal/youtubeimport"
)

type Options struct {
	XBookmarksEnabled bool
	XBookmarksLimit   int

	XEnabled         bool
	XLimit           int
	XConcurrency     int
	XTimeout         time.Duration
	XMediaTimeout    time.Duration
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

	AppleNotesEnabled            bool
	AppleNotesDBPath             string
	AppleNotesLimit              int
	AppleNotesExcludeFolders     []string
	AppleNotesExcludeAccounts    []string
	AppleNotesExcludeShared      bool
	AppleNotesIncludeLocked      bool
	AppleNotesSkipAttachments    bool
	AppleNotesSkipAttachmentOCR  bool
	AppleNotesAttachmentMaxBytes int64
	AppleNotesTesseractBinary    string

	SafariTabsEnabled   bool
	SafariTabsDBPath    string
	SafariTabsDevice    string
	SafariTabsLimit     int
	SafariTabsOlderThan time.Duration

	FeedsEnabled bool
	FeedLimit    int

	SourcesEnabled      bool
	SourceLimit         int
	SourceConcurrency   int
	SourceWatch         bool
	SourcePollInterval  time.Duration
	SourceIdleExitAfter time.Duration
	SourceMaxCycles     int

	Browser               string
	Profile               string
	Force                 bool
	Summarize             bool
	Model                 string
	OCRModel              string
	ArchiveMediaEnabled   bool
	ArchiveMediaLimit     int
	ArchiveProvider       string
	ArchiveBucket         string
	ArchivePublicBaseURL  string
	ArchiveEndpoint       string
	ArchiveRegion         string
	ArchiveAccessKeyID    string
	ArchiveSecretKey      string
	ArchiveSessionToken   string
	CategorizeEnabled     bool
	CategorizeLimit       int
	CategorizeConcurrency int
	CategorizeModel       string
	CategorizeTimeout     time.Duration
	CategorizeImages      bool
	CLI                   string
	Length                string
	Timeout               time.Duration
	Logger                *slog.Logger
	Progress              io.Writer
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
	AppleNotes   *AppleNotesStage   `json:"apple_notes,omitempty"`
	SafariTabs   *SafariTabsStage   `json:"safari_tabs,omitempty"`
	Feeds        *FeedsStage        `json:"feeds,omitempty"`
	Sources      *SourcesStage      `json:"sources,omitempty"`
	MediaArchive *MediaArchiveStage `json:"media_archive,omitempty"`
	Categorize   *CategorizeStage   `json:"categorize,omitempty"`
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

type AppleNotesStage struct {
	Duration time.Duration    `json:"duration"`
	Stats    applenotes.Stats `json:"stats"`
}

type SafariTabsStage struct {
	Duration time.Duration    `json:"duration"`
	Stats    safaritabs.Stats `json:"stats"`
}

type FeedsStage struct {
	Duration time.Duration    `json:"duration"`
	Stats    feedimport.Stats `json:"stats"`
}

type SourcesStage struct {
	Duration time.Duration      `json:"duration"`
	Stats    worker.SourceStats `json:"stats"`
}

type MediaArchiveStage struct {
	Duration time.Duration      `json:"duration"`
	Stats    mediaarchive.Stats `json:"stats"`
}

type CategorizeStage struct {
	Duration    time.Duration        `json:"duration"`
	Stats       itemcategorize.Stats `json:"stats"`
	ItemStats   itemcategorize.Stats `json:"item_stats"`
	SourceStats itemcategorize.Stats `json:"source_stats"`
}
