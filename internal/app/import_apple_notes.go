package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/applenotes"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/store"
)

func newImportAppleNotesCommand(root *rootOptions) *cobra.Command {
	var dbPath string
	var limit int
	var dryRun bool
	var force bool
	var showTitles bool
	var excludeFolders []string
	var excludeAccounts []string
	var excludeShared bool
	var includeLocked bool
	var forgetExcluded bool
	var skipAttachments bool
	var skipAttachmentOCR bool
	var attachmentMaxBytes int64
	var tesseractBinary string
	summarize := true
	var summaryModel string
	var summaryCLI string
	var summaryLength string
	var timeout time.Duration
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "apple-notes",
		Short: "Import Apple Notes from the local Notes SQLite store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}
			if strings.TrimSpace(dbPath) == "" {
				dbPath = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_APPLE_NOTES_DB_PATH")
			}
			if len(excludeFolders) == 0 {
				excludeFolders = runtimeenv.FirstList(cfg.RootDir, "DBRAIN_APPLE_NOTES_EXCLUDE_FOLDERS")
			}
			if len(excludeAccounts) == 0 {
				excludeAccounts = runtimeenv.FirstList(cfg.RootDir, "DBRAIN_APPLE_NOTES_EXCLUDE_ACCOUNTS")
			}
			if !excludeShared {
				excludeShared = runtimeenv.FirstBool(cfg.RootDir, "DBRAIN_APPLE_NOTES_EXCLUDE_SHARED")
			}
			if !skipAttachments {
				if indexAttachments, ok := runtimeenv.LookupBool(cfg.RootDir, "DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS"); ok {
					skipAttachments = !indexAttachments
				}
				if runtimeenv.FirstBool(cfg.RootDir, "DBRAIN_APPLE_NOTES_SKIP_ATTACHMENTS") {
					skipAttachments = true
				}
			}
			if !skipAttachmentOCR {
				if attachmentOCR, ok := runtimeenv.LookupBool(cfg.RootDir, "DBRAIN_APPLE_NOTES_ATTACHMENT_OCR"); ok {
					skipAttachmentOCR = !attachmentOCR
				}
				if runtimeenv.FirstBool(cfg.RootDir, "DBRAIN_APPLE_NOTES_SKIP_ATTACHMENT_OCR") {
					skipAttachmentOCR = true
				}
			}
			if attachmentMaxBytes <= 0 {
				if value := runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES"); value != "" {
					parsed, parseErr := strconv.ParseInt(value, 10, 64)
					if parseErr != nil || parsed < 0 {
						return fmt.Errorf("parse DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES: %q", value)
					}
					attachmentMaxBytes = parsed
				}
			}
			if strings.TrimSpace(tesseractBinary) == "" {
				tesseractBinary = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_APPLE_NOTES_TESSERACT_BINARY")
			}

			var st *store.Store
			if !dryRun {
				st, err = store.Open(cfg.DBPath)
				if err != nil {
					return err
				}
				defer func() {
					_ = st.Close()
				}()
			}

			var progress applenotes.ProgressFunc
			if !jsonOut {
				progress = func(event applenotes.ProgressEvent) {
					writeAppleNotesProgress(cmd.OutOrStdout(), event, showTitles)
				}
			}

			stats, err := applenotes.Run(cmd.Context(), cfg, st, applenotes.Options{
				DBPath:             dbPath,
				Limit:              limit,
				DryRun:             dryRun,
				Force:              force,
				ShowTitles:         showTitles,
				ExcludeFolders:     excludeFolders,
				ExcludeAccounts:    excludeAccounts,
				ExcludeShared:      excludeShared,
				IncludeLocked:      includeLocked,
				ForgetExcluded:     forgetExcluded,
				SkipAttachments:    skipAttachments,
				SkipAttachmentOCR:  skipAttachmentOCR,
				AttachmentMaxBytes: attachmentMaxBytes,
				TesseractBinary:    tesseractBinary,
				Summarize:          summarize,
				SummaryModel:       summaryModel,
				SummaryCLI:         summaryCLI,
				SummaryLength:      summaryLength,
				Timeout:            timeout,
				Progress:           progress,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			writeAppleNotesStats(cmd.OutOrStdout(), stats)
			return nil
		},
	}
	cmd.AddCommand(newImportAppleNotesProbeCommand(root), newImportAppleNotesSnapshotCommand(root), newImportAppleNotesDecodeCommand(root))
	cmd.Flags().StringVar(&dbPath, "db", "", "Apple Notes NoteStore.sqlite path override")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum notes to process")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Run the full import decision path without storing note content")
	cmd.Flags().BoolVar(&force, "force", false, "Re-render and re-summarize matching Apple Notes even when imported content is unchanged")
	cmd.Flags().BoolVar(&showTitles, "show-titles", false, "Allow output to show Apple Note titles")
	cmd.Flags().StringArrayVar(&excludeFolders, "exclude-folder", nil, "Exclude an Apple Notes folder/path; repeatable")
	cmd.Flags().StringArrayVar(&excludeAccounts, "exclude-account", nil, "Exclude an Apple Notes account; repeatable")
	cmd.Flags().BoolVar(&excludeShared, "exclude-shared", false, "Exclude shared notes")
	cmd.Flags().BoolVar(&includeLocked, "include-locked", false, "Attempt to include password-protected notes")
	cmd.Flags().BoolVar(&forgetExcluded, "forget-excluded", false, "Purge already imported note content that is now excluded")
	cmd.Flags().BoolVar(&skipAttachments, "skip-attachments", false, "Skip default attachment file extraction/OCR; keep note bodies, metadata, and Notes-provided attachment text")
	cmd.Flags().BoolVar(&skipAttachmentOCR, "skip-attachment-ocr", false, "Skip default local OCR for image attachments")
	cmd.Flags().Int64Var(&attachmentMaxBytes, "attachment-max-bytes", 0, "Maximum attachment file size to extract; 0 uses the default")
	cmd.Flags().StringVar(&tesseractBinary, "tesseract", "", "Tesseract binary for local Apple Notes image OCR")
	cmd.Flags().BoolVar(&summarize, "summarize", true, "Summarize imported Apple Notes locally after materialization; set --summarize=false to skip")
	cmd.Flags().StringVar(&summaryModel, "summary-model", "", "Apple Notes summary model override; defaults to summary.model")
	cmd.Flags().StringVar(&summaryCLI, "summary-cli", defaultCLIProvider, "Summarize CLI provider for Apple Notes")
	cmd.Flags().StringVar(&summaryLength, "summary-length", "medium", "Summary length for Apple Notes")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Timeout for Apple Notes summarization")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print import stats as JSON")
	return cmd
}

func writeAppleNotesStats(dst interface{ Write([]byte) (int, error) }, stats applenotes.Stats) {
	mode := "dry-run"
	if stats.Applied {
		mode = "applied"
	}
	_, _ = fmt.Fprintf(dst, "Mode:      %s\n", mode)
	_, _ = fmt.Fprintf(dst, "Seen:      %d\n", stats.NotesSeen)
	_, _ = fmt.Fprintf(dst, "Matched:   %d\n", stats.NotesMatched)
	_, _ = fmt.Fprintf(dst, "Imported:  %d\n", stats.NotesImported)
	_, _ = fmt.Fprintf(dst, "Created:   %d\n", stats.NotesCreated)
	_, _ = fmt.Fprintf(dst, "Updated:   %d\n", stats.NotesUpdated)
	_, _ = fmt.Fprintf(dst, "Unchanged: %d\n", stats.NotesUnchanged)
	_, _ = fmt.Fprintf(dst, "Rendered:  %d\n", stats.NotesRendered)
	_, _ = fmt.Fprintf(dst, "Skipped:   %d\n", stats.NotesSkipped)
	_, _ = fmt.Fprintf(dst, "Blocked:   %d\n", stats.NotesBlocked)
	_, _ = fmt.Fprintf(dst, "Purged:    %d\n", stats.NotesPurged)
	_, _ = fmt.Fprintf(dst, "Attachments: seen=%d indexed=%d extracted=%d ocr=%d blocked=%d\n", stats.AttachmentsSeen, stats.AttachmentsIndexed, stats.AttachmentsExtracted, stats.AttachmentsOCRed, stats.AttachmentsBlocked)
	_, _ = fmt.Fprintf(dst, "Links:     %d\n", stats.LinksDiscovered)
	_, _ = fmt.Fprintf(dst, "Summaries: %d\n", stats.SummariesCreated)
	_, _ = fmt.Fprintf(dst, "Errors:    %d\n", stats.Errors)
	if len(stats.SampleTitles) > 0 {
		_, _ = fmt.Fprintf(dst, "Sample titles:\n")
		for _, title := range stats.SampleTitles {
			_, _ = fmt.Fprintf(dst, "- %s\n", title)
		}
	}
}

func writeAppleNotesProgress(dst interface{ Write([]byte) (int, error) }, event applenotes.ProgressEvent, showTitles bool) {
	if event.Phase == "" {
		return
	}
	if event.Phase == "loaded" {
		_, _ = fmt.Fprintf(dst, "Apple Notes loaded: candidates=%d\n", event.Total)
		return
	}
	if event.Phase == "snapshotting" {
		_, _ = fmt.Fprintf(dst, "Apple Notes snapshotting source=%s\n", emptyDash(event.Reason))
		return
	}
	if event.Phase == "snapshot" {
		_, _ = fmt.Fprintf(dst, "Apple Notes snapshot ready path=%s\n", emptyDash(event.Reason))
		return
	}
	if event.Phase == "decoded" {
		_, _ = fmt.Fprintf(dst, "Apple Notes decoded: candidates=%d\n", event.Total)
		return
	}

	position := ""
	if event.Index > 0 && event.Total > 0 {
		position = fmt.Sprintf(" %d/%d", event.Index, event.Total)
	}
	source := event.SourceKey
	if source == "" {
		source = "unknown"
	}
	title := ""
	if showTitles && strings.TrimSpace(event.Title) != "" {
		title = fmt.Sprintf(" title=%q", event.Title)
	}

	switch event.Phase {
	case "decoded_note", "unchanged":
		return
	case "attachments":
		if event.Total > 1 {
			return
		}
		_, _ = fmt.Fprintf(dst, "Apple Note%s attachments source=%s status=%s links=%d attachments=%d attachment_chars=%d%s\n",
			position, source, emptyDash(event.Status), event.Links, event.Attachments, event.AttachmentChars, title)
	case "attachment":
		if event.Total > 1 {
			return
		}
		_, _ = fmt.Fprintf(dst, "Apple Note%s attachment source=%s ordinal=%d status=%s reason=%s attachment_chars=%d%s\n",
			position, source, event.Attachments, emptyDash(event.Status), emptyDash(event.Reason), event.AttachmentChars, title)
	case "processing":
		if event.Reason == "summary" {
			return
		}
		_, _ = fmt.Fprintf(dst, "Apple Note%s processing source=%s reason=%s links=%d attachments=%d text_chars=%d attachment_chars=%d%s\n",
			position, source, emptyDash(event.Reason), event.Links, event.Attachments, event.TextChars, event.AttachmentChars, title)
	case "summarizing":
		_, _ = fmt.Fprintf(dst, "Apple Note%s summarizing source=%s status=%s%s\n",
			position, source, emptyDash(event.Status), title)
	case "imported":
		if event.Status == "unchanged" && !event.Rendered && (event.SummaryStatus == "ok" || event.SummaryStatus == "current") {
			return
		}
		_, _ = fmt.Fprintf(dst, "Apple Note%s imported source=%s status=%s rendered=%t summary=%s links=%d attachments=%d%s\n",
			position, source, emptyDash(event.Status), event.Rendered, emptyDash(event.SummaryStatus), event.Links, event.Attachments, title)
	case "skipped", "blocked":
		_, _ = fmt.Fprintf(dst, "Apple Note%s %s source=%s reason=%s%s\n",
			position, event.Phase, source, emptyDash(event.Reason), title)
	case "dry_run":
		_, _ = fmt.Fprintf(dst, "Apple Note%s dry-run source=%s status=%s links=%d attachments=%d text_chars=%d attachment_chars=%d%s\n",
			position, source, emptyDash(event.Status), event.Links, event.Attachments, event.TextChars, event.AttachmentChars, title)
	default:
		_, _ = fmt.Fprintf(dst, "Apple Note%s %s source=%s status=%s reason=%s%s\n",
			position, event.Phase, source, emptyDash(event.Status), emptyDash(event.Reason), title)
	}
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func newImportAppleNotesProbeCommand(root *rootOptions) *cobra.Command {
	var dbPath string
	var snapshotDir string
	var keepSnapshot bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "probe",
		Short: "Probe Apple Notes database access and schema without decoding note bodies",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}

			stats, err := applenotes.Probe(cmd.Context(), cfg, applenotes.Options{
				DBPath:       dbPath,
				SnapshotDir:  snapshotDir,
				KeepSnapshot: keepSnapshot,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source DB: %s\n", stats.SourceDBPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Snapshot:  %s\n", stats.Snapshot.DBPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Notes:     %d\n", stats.NoteCount)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Folders:   %d\n", stats.FolderCount)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Accounts:  %d\n", stats.AccountCount)
			for name, table := range stats.Tables {
				status := "missing"
				if table.Exists {
					status = fmt.Sprintf("rows=%d columns=%d", table.Rows, len(table.Columns))
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Table %-24s %s\n", name+":", status)
			}
			if len(stats.Warnings) > 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Warnings:  %s\n", strings.Join(stats.Warnings, "; "))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Apple Notes NoteStore.sqlite path override")
	cmd.Flags().StringVar(&snapshotDir, "snapshot-dir", "", "Keep snapshot in this directory instead of a temporary path")
	cmd.Flags().BoolVar(&keepSnapshot, "keep-snapshot", false, "Keep the temporary snapshot after probing")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print probe stats as JSON")
	return cmd
}

func newImportAppleNotesSnapshotCommand(root *rootOptions) *cobra.Command {
	var dbPath string
	var dir string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "snapshot --dir <path>",
		Short: "Create a read-only Apple Notes DB/WAL/SHM snapshot for debugging",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(dir) == "" {
				return fmt.Errorf("--dir is required")
			}
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}
			info, cleanup, err := applenotes.CreateSnapshot(cfg, applenotes.Options{
				DBPath:      dbPath,
				SnapshotDir: dir,
			})
			if err != nil {
				return err
			}
			if cleanup != nil {
				defer func() {
					_ = cleanup()
				}()
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), info)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source DB: %s\n", info.SourceDBPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Snapshot:  %s\n", info.DBPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Files:\n")
			for _, path := range info.CopiedFiles {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "- %s\n", path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Apple Notes NoteStore.sqlite path override")
	cmd.Flags().StringVar(&dir, "dir", "", "Directory where the snapshot should be copied")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print snapshot metadata as JSON")
	return cmd
}

func newImportAppleNotesDecodeCommand(root *rootOptions) *cobra.Command {
	var dbPath string
	var noteID string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:    "decode --note <id>",
		Short:  "Decode one Apple Note body from a snapshot for local debugging",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(noteID) == "" {
				return fmt.Errorf("--note is required")
			}
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}
			doc, _, err := applenotes.DecodeNote(cmd.Context(), cfg, applenotes.Options{
				DBPath: dbPath,
			}, noteID)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), doc)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "# %s\n\n%s\n", doc.Title, doc.Text)
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Apple Notes NoteStore.sqlite path override")
	cmd.Flags().StringVar(&noteID, "note", "", "Apple Notes identifier/source key to decode")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print decoded note as JSON")
	return cmd
}
