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
