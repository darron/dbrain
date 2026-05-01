package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
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
	var appleNotes bool
	var appleNotesDBPath string
	var appleNotesLimit int
	var appleNotesExcludeFolders []string
	var appleNotesExcludeAccounts []string
	var appleNotesExcludeShared bool
	var appleNotesIncludeLocked bool
	var appleNotesSkipAttachments bool
	var appleNotesSkipAttachmentOCR bool
	var appleNotesAttachmentMaxBytes int64
	var appleNotesTesseractBinary string
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
	var archiveMedia bool
	var archiveMediaLimit int
	var categorizeLimit int
	var categorizeConcurrency int
	var categorizeModel string
	var categorizeTimeout time.Duration
	var categorizeImages = true
	var skipXBookmarks bool
	var skipX bool
	var skipXMedia bool
	var skipXPhotoOCR bool
	var skipLinks bool
	var skipGitHub bool
	var skipYouTube bool
	var skipAppleNotes bool
	var skipSources bool
	var skipCategorize bool
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
			if !archiveMedia {
				archiveMedia = firstEnvBool(cfg.RootDir, "DBRAIN_AUTO_ARCHIVE_MEDIA", "DBRAIN_ARCHIVE_AUTO")
			}
			if !appleNotes {
				appleNotes = firstEnvBool(cfg.RootDir, "DBRAIN_APPLE_NOTES_ENABLED")
			}
			if strings.TrimSpace(appleNotesDBPath) == "" {
				appleNotesDBPath = firstNonEmptyEnv(cfg.RootDir, "DBRAIN_APPLE_NOTES_DB_PATH")
			}
			if len(appleNotesExcludeFolders) == 0 {
				appleNotesExcludeFolders = firstEnvList(cfg.RootDir, "DBRAIN_APPLE_NOTES_EXCLUDE_FOLDERS")
			}
			if len(appleNotesExcludeAccounts) == 0 {
				appleNotesExcludeAccounts = firstEnvList(cfg.RootDir, "DBRAIN_APPLE_NOTES_EXCLUDE_ACCOUNTS")
			}
			if !appleNotesExcludeShared {
				appleNotesExcludeShared = firstEnvBool(cfg.RootDir, "DBRAIN_APPLE_NOTES_EXCLUDE_SHARED")
			}
			if !appleNotesSkipAttachments {
				if value := firstNonEmptyEnv(cfg.RootDir, "DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS"); value != "" {
					parsed, parseErr := strconv.ParseBool(value)
					if parseErr != nil {
						return fmt.Errorf("parse DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS: %q", value)
					}
					appleNotesSkipAttachments = !parsed
				}
				if firstEnvBool(cfg.RootDir, "DBRAIN_APPLE_NOTES_SKIP_ATTACHMENTS") {
					appleNotesSkipAttachments = true
				}
			}
			if !appleNotesSkipAttachmentOCR {
				if value := firstNonEmptyEnv(cfg.RootDir, "DBRAIN_APPLE_NOTES_ATTACHMENT_OCR"); value != "" {
					parsed, parseErr := strconv.ParseBool(value)
					if parseErr != nil {
						return fmt.Errorf("parse DBRAIN_APPLE_NOTES_ATTACHMENT_OCR: %q", value)
					}
					appleNotesSkipAttachmentOCR = !parsed
				}
				if firstEnvBool(cfg.RootDir, "DBRAIN_APPLE_NOTES_SKIP_ATTACHMENT_OCR") {
					appleNotesSkipAttachmentOCR = true
				}
			}
			if appleNotesAttachmentMaxBytes <= 0 {
				if value := firstNonEmptyEnv(cfg.RootDir, "DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES"); value != "" {
					parsed, parseErr := strconv.ParseInt(value, 10, 64)
					if parseErr != nil || parsed < 0 {
						return fmt.Errorf("parse DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES: %q", value)
					}
					appleNotesAttachmentMaxBytes = parsed
				}
			}
			if strings.TrimSpace(appleNotesTesseractBinary) == "" {
				appleNotesTesseractBinary = firstNonEmptyEnv(cfg.RootDir, "DBRAIN_APPLE_NOTES_TESSERACT_BINARY")
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			progress := cmd.ErrOrStderr()
			logWriter := cmd.ErrOrStderr()
			var syncUI *syncProgressUI
			if !jsonOut {
				syncUI = newSyncProgressUI(cmd.ErrOrStderr())
				defer syncUI.Close()
				progress = syncUI
				logWriter = syncUI.LogWriter()
			}

			stats, err := syncjob.Run(cmd.Context(), cfg, st, syncjob.Options{
				XBookmarksEnabled:            !skipXBookmarks,
				XBookmarksLimit:              xBookmarksLimit,
				XEnabled:                     !skipX,
				XLimit:                       xLimit,
				XConcurrency:                 xConcurrency,
				XTimeout:                     xTimeout,
				XMediaEnabled:                !skipXMedia,
				XMediaLimit:                  xLimit,
				XPhotoOCREnabled:             !skipXPhotoOCR,
				XPhotoOCRLimit:               xLimit,
				LinksEnabled:                 !skipLinks,
				LinkDiscoverLimit:            linkDiscoverLimit,
				LinkLimit:                    linkLimit,
				LinkConcurrency:              linkConcurrency,
				GitHubEnabled:                !skipGitHub,
				GitHubLimit:                  githubLimit,
				YouTubeEnabled:               !skipYouTube,
				YouTubeLimit:                 youtubeLimit,
				WatchLater:                   watchLater,
				Liked:                        liked,
				AppleNotesEnabled:            appleNotes && !skipAppleNotes,
				AppleNotesDBPath:             appleNotesDBPath,
				AppleNotesLimit:              appleNotesLimit,
				AppleNotesExcludeFolders:     appleNotesExcludeFolders,
				AppleNotesExcludeAccounts:    appleNotesExcludeAccounts,
				AppleNotesExcludeShared:      appleNotesExcludeShared,
				AppleNotesIncludeLocked:      appleNotesIncludeLocked,
				AppleNotesSkipAttachments:    appleNotesSkipAttachments,
				AppleNotesSkipAttachmentOCR:  appleNotesSkipAttachmentOCR,
				AppleNotesAttachmentMaxBytes: appleNotesAttachmentMaxBytes,
				AppleNotesTesseractBinary:    appleNotesTesseractBinary,
				SourcesEnabled:               !skipSources,
				SourceLimit:                  sourceLimit,
				SourceConcurrency:            sourceConcurrency,
				SourceWatch:                  watch,
				SourcePollInterval:           pollInterval,
				SourceIdleExitAfter:          idleExitAfter,
				SourceMaxCycles:              maxCycles,
				Browser:                      browser,
				Profile:                      profile,
				Force:                        force,
				Summarize:                    summarize,
				Model:                        model,
				OCRModel:                     ocrModel,
				ArchiveMediaEnabled:          archiveMedia,
				ArchiveMediaLimit:            archiveMediaLimit,
				ArchiveProvider:              firstNonEmptyEnv(cfg.RootDir, "DBRAIN_ARCHIVE_PROVIDER", "DBRAIN_R2_PROVIDER"),
				ArchiveBucket:                firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_BUCKET", "DBRAIN_ARCHIVE_BUCKET"),
				ArchivePublicBaseURL:         firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_PUBLIC_BASE_URL", "DBRAIN_MEDIA_PUBLIC_BASE_URL"),
				ArchiveEndpoint:              firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_ENDPOINT", "DBRAIN_S3_ENDPOINT"),
				ArchiveRegion:                firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_REGION", "DBRAIN_S3_REGION"),
				ArchiveAccessKeyID:           firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_ACCESS_KEY_ID", "DBRAIN_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID"),
				ArchiveSecretKey:             firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_SECRET_ACCESS_KEY", "DBRAIN_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY"),
				ArchiveSessionToken:          firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_SESSION_TOKEN", "DBRAIN_S3_SESSION_TOKEN", "AWS_SESSION_TOKEN"),
				CategorizeEnabled:            !skipCategorize,
				CategorizeLimit:              categorizeLimit,
				CategorizeConcurrency:        categorizeConcurrency,
				CategorizeModel:              categorizeModel,
				CategorizeTimeout:            categorizeTimeout,
				CategorizeImages:             categorizeImages,
				CLI:                          cliProvider,
				Length:                       length,
				Timeout:                      timeout,
				Logger:                       newLogger(commandDebugEnabled(cmd), logWriter),
				Progress:                     progress,
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
	cmd.Flags().StringVar(&ocrModel, "ocr-model", "", "Model override for X photo OCR; supports ollama/<name> and openrouter/<provider>/<model>")
	cmd.Flags().IntVar(&linkDiscoverLimit, "link-discover-limit", 500, "Maximum imported items to scan for outbound links")
	cmd.Flags().IntVar(&linkLimit, "link-limit", 100, "Maximum deduped discovered sources to enrich per link extraction run")
	cmd.Flags().IntVar(&linkConcurrency, "link-concurrency", 4, "Number of concurrent link source extract/summarize jobs")
	cmd.Flags().IntVar(&githubLimit, "github-limit", 0, "Maximum starred repositories to process before stopping")
	cmd.Flags().IntVar(&youtubeLimit, "youtube-limit", 50, "Maximum videos to load from each selected YouTube feed")
	cmd.Flags().BoolVar(&appleNotes, "apple-notes", false, "Include configured Apple Notes import in sync")
	cmd.Flags().StringVar(&appleNotesDBPath, "apple-notes-db", "", "Apple Notes NoteStore.sqlite path override")
	cmd.Flags().IntVar(&appleNotesLimit, "apple-notes-limit", 0, "Maximum Apple Notes to process")
	cmd.Flags().StringArrayVar(&appleNotesExcludeFolders, "apple-notes-exclude-folder", nil, "Exclude an Apple Notes folder/path during sync; repeatable")
	cmd.Flags().StringArrayVar(&appleNotesExcludeAccounts, "apple-notes-exclude-account", nil, "Exclude an Apple Notes account during sync; repeatable")
	cmd.Flags().BoolVar(&appleNotesExcludeShared, "apple-notes-exclude-shared", false, "Exclude shared Apple Notes during sync")
	cmd.Flags().BoolVar(&appleNotesIncludeLocked, "apple-notes-include-locked", false, "Attempt to include password-protected Apple Notes during sync")
	cmd.Flags().BoolVar(&appleNotesSkipAttachments, "apple-notes-skip-attachments", false, "Skip Apple Notes attachment file extraction/OCR during sync")
	cmd.Flags().BoolVar(&appleNotesSkipAttachmentOCR, "apple-notes-skip-attachment-ocr", false, "Skip local OCR for Apple Notes image attachments during sync")
	cmd.Flags().Int64Var(&appleNotesAttachmentMaxBytes, "apple-notes-attachment-max-bytes", 0, "Maximum Apple Notes attachment file size to extract; 0 uses the default")
	cmd.Flags().StringVar(&appleNotesTesseractBinary, "apple-notes-tesseract", "", "Tesseract binary for Apple Notes image OCR")
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
	cmd.Flags().BoolVar(&archiveMedia, "archive-media", false, "Upload finalized media to configured S3-compatible storage and prune local copies at the end of sync")
	cmd.Flags().IntVar(&archiveMediaLimit, "archive-media-limit", 5000, "Maximum finalized media assets to archive at the end of sync")
	cmd.Flags().IntVar(&categorizeLimit, "categorize-limit", 0, "Maximum uncategorized items and sources to categorize per record type at the end of sync; 0 means all queued records")
	cmd.Flags().IntVar(&categorizeConcurrency, "categorize-concurrency", 2, "Number of concurrent categorization requests")
	cmd.Flags().StringVar(&categorizeModel, "categorize-model", "", "Categorization model override; defaults to DBRAIN_CATEGORIZE_MODEL or the categorizer default")
	cmd.Flags().DurationVar(&categorizeTimeout, "categorize-timeout", 90*time.Second, "Per-record categorization request timeout")
	cmd.Flags().BoolVar(&categorizeImages, "categorize-images", true, "Embed item photos as base64 for vision-capable categorization models; use --categorize-images=false to disable")
	cmd.Flags().BoolVar(&skipXBookmarks, "skip-x-bookmarks", false, "Skip direct X bookmark import")
	cmd.Flags().BoolVar(&skipX, "skip-x", false, "Skip X hydration")
	cmd.Flags().BoolVar(&skipXMedia, "skip-x-media", false, "Skip X media audio transcription")
	cmd.Flags().BoolVar(&skipXPhotoOCR, "skip-x-photo-ocr", false, "Skip X photo OCR / vision extraction")
	cmd.Flags().BoolVar(&skipLinks, "skip-links", false, "Skip outbound link discovery and enrichment from imported items")
	cmd.Flags().BoolVar(&skipGitHub, "skip-github", false, "Skip GitHub stars import")
	cmd.Flags().BoolVar(&skipYouTube, "skip-youtube", false, "Skip YouTube signal import")
	cmd.Flags().BoolVar(&skipAppleNotes, "skip-apple-notes", false, "Skip configured Apple Notes import")
	cmd.Flags().BoolVar(&skipSources, "skip-sources", false, "Skip the final source backlog worker stage")
	cmd.Flags().BoolVar(&skipCategorize, "skip-categorize", false, "Skip final item/source categorization")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print sync stats as JSON")

	return cmd
}

func writeSyncStats(dst interface{ Write([]byte) (int, error) }, stats syncjob.Stats) error {
	if _, err := fmt.Fprintf(dst, "\nSync Summary\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Started:   %s\n", stats.StartedAt.Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Completed: %s\n", stats.CompletedAt.Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Duration:  %s\n\n", stats.Duration); err != nil {
		return err
	}

	rows := syncSummaryRows(stats)
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers("Stage", "Duration", "Primary", "Secondary", "Errors").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return base.Bold(true).Foreground(lipgloss.Color("39"))
			}
			if col == 4 && row >= 0 && rows[row][4] != "0" {
				return base.Foreground(lipgloss.Color("196"))
			}
			if col == 0 {
				return base.Bold(true)
			}
			return base
		})

	if _, err := fmt.Fprintln(dst, t.String()); err != nil {
		return err
	}
	return nil
}

func syncSummaryRows(stats syncjob.Stats) [][]string {
	rows := make([][]string, 0, 10)
	if stats.AppleNotes != nil {
		s := stats.AppleNotes.Stats
		rows = append(rows, []string{"Apple Notes", stats.AppleNotes.Duration.String(), fmt.Sprintf("imported=%d rendered=%d", s.NotesImported, s.NotesRendered), fmt.Sprintf("skipped=%d blocked=%d attachments=%d extracted=%d summarized=%d", s.NotesSkipped, s.NotesBlocked, s.AttachmentsIndexed, s.AttachmentsExtracted, s.SummariesCreated), strconv.Itoa(s.Errors)})
	}
	if stats.XBookmarks != nil {
		s := stats.XBookmarks.Stats
		rows = append(rows, []string{"X Bookmarks", stats.XBookmarks.Duration.String(), fmt.Sprintf("created=%d updated=%d", s.Created, s.Updated), fmt.Sprintf("unchanged=%d pages=%d stopped=%s", s.Unchanged, s.PagesFetched, s.StoppedReason), "0"})
	}
	if stats.X != nil {
		s := stats.X.Stats
		rows = append(rows, []string{"X Hydration", stats.X.Duration.String(), fmt.Sprintf("hydrated=%d rendered=%d", s.Hydrated, s.Rendered), fmt.Sprintf("media_downloaded=%d missing=%d", s.MediaDownloaded, s.Missing), strconv.Itoa(s.APIErrors + s.MediaErrors)})
	}
	if stats.XMedia != nil {
		s := stats.XMedia.Stats
		rows = append(rows, []string{"X Media", stats.XMedia.Duration.String(), fmt.Sprintf("processed=%d transcribed=%d", s.ItemsProcessed, s.MediaTranscribed), fmt.Sprintf("summarized=%d skipped=%d", s.ItemsSummarized, s.ItemsSkipped), strconv.Itoa(s.Errors + s.SummaryErrors)})
	}
	if stats.XPhotoOCR != nil {
		s := stats.XPhotoOCR.Stats
		rows = append(rows, []string{"X Photo OCR", stats.XPhotoOCR.Duration.String(), fmt.Sprintf("processed=%d ocr=%d", s.ItemsProcessed, s.PhotosOCRed), fmt.Sprintf("updated=%d skipped=%d", s.ItemsUpdated, s.ItemsSkipped), strconv.Itoa(s.Errors)})
	}
	if stats.Links != nil {
		s := stats.Links.Stats
		rows = append(rows, []string{"Links", stats.Links.Duration.String(), fmt.Sprintf("items_scanned=%d", s.ItemsScanned), fmt.Sprintf("queued=%d summarized=%d", s.SourcesQueued, s.SourcesSummarized), strconv.Itoa(s.Errors)})
	}
	if stats.GitHub != nil {
		s := stats.GitHub.Stats
		rows = append(rows, []string{"GitHub", stats.GitHub.Duration.String(), fmt.Sprintf("stars=%d created=%d", s.StarsProcessed, s.ItemsCreated), fmt.Sprintf("summarized=%d", s.SourcesSummarized), strconv.Itoa(s.Errors)})
	}
	if stats.YouTube != nil {
		s := stats.YouTube.Stats
		rows = append(rows, []string{"YouTube", stats.YouTube.Duration.String(), fmt.Sprintf("items=%d", s.ItemsProcessed), fmt.Sprintf("summarized=%d", s.SourcesSummarized), strconv.Itoa(s.Errors)})
	}
	if stats.Sources != nil {
		s := stats.Sources.Stats
		rows = append(rows, []string{"Sources", stats.Sources.Duration.String(), fmt.Sprintf("cycles=%d summarized=%d", s.WorkCycles, s.SourcesSummarized), fmt.Sprintf("stopped=%s", s.StoppedReason), strconv.Itoa(s.Errors)})
	}
	if stats.Categorize != nil {
		s := stats.Categorize.Stats
		items := stats.Categorize.ItemStats
		sources := stats.Categorize.SourceStats
		rows = append(rows, []string{"Categorize", stats.Categorize.Duration.String(), fmt.Sprintf("items=%d/%d sources=%d/%d", items.Applied, items.Queued, sources.Applied, sources.Queued), fmt.Sprintf("succeeded=%d skipped=%d", s.Succeeded, s.Skipped), strconv.Itoa(s.Errors)})
	}
	if stats.MediaArchive != nil {
		s := stats.MediaArchive.Stats
		rows = append(rows, []string{"Media Archive", stats.MediaArchive.Duration.String(), fmt.Sprintf("uploaded=%d archived=%d", s.Uploaded, s.Archived), fmt.Sprintf("pruned_files=%d unchanged=%d", s.LocalFilesPruned, s.Unchanged), strconv.Itoa(s.Errors)})
	}
	return rows
}
