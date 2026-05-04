package syncjob

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/applenotes"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/safaritabs"
	"github.com/darron/dbrain/internal/store"
)

func executeAppleNotesStage(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (*AppleNotesStage, error) {
	progressf(opts.Progress, "==> import apple-notes\n")
	start := time.Now()
	var appleNotesProgress applenotes.ProgressFunc
	if opts.Progress != nil {
		appleNotesProgress = func(event applenotes.ProgressEvent) {
			formatAppleNotesSyncProgress(opts.Progress, event)
		}
	}
	appleStats, err := runAppleNotesImport(ctx, cfg, st, applenotes.Options{
		DBPath:             opts.AppleNotesDBPath,
		Limit:              opts.AppleNotesLimit,
		Force:              opts.Force,
		ExcludeFolders:     opts.AppleNotesExcludeFolders,
		ExcludeAccounts:    opts.AppleNotesExcludeAccounts,
		ExcludeShared:      opts.AppleNotesExcludeShared,
		IncludeLocked:      opts.AppleNotesIncludeLocked,
		SkipAttachments:    opts.AppleNotesSkipAttachments,
		SkipAttachmentOCR:  opts.AppleNotesSkipAttachmentOCR,
		AttachmentMaxBytes: opts.AppleNotesAttachmentMaxBytes,
		TesseractBinary:    opts.AppleNotesTesseractBinary,
		Summarize:          opts.Summarize,
		SummaryModel:       opts.Model,
		SummaryCLI:         opts.CLI,
		SummaryLength:      opts.Length,
		Timeout:            opts.Timeout,
		Progress:           appleNotesProgress,
	})
	stage := &AppleNotesStage{Duration: time.Since(start), Stats: appleStats}
	if err != nil {
		return stage, fmt.Errorf("import apple-notes: %w", err)
	}
	progressf(opts.Progress, "Apple Notes import complete: seen=%d imported=%d rendered=%d skipped=%d blocked=%d attachments=%d extracted=%d ocr=%d summarized=%d errors=%d (%s)\n", appleStats.NotesSeen, appleStats.NotesImported, appleStats.NotesRendered, appleStats.NotesSkipped, appleStats.NotesBlocked, appleStats.AttachmentsIndexed, appleStats.AttachmentsExtracted, appleStats.AttachmentsOCRed, appleStats.SummariesCreated, appleStats.Errors, stage.Duration)
	return stage, nil
}

func executeSafariTabsStage(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (*SafariTabsStage, error) {
	progressf(opts.Progress, "==> import safari-tabs\n")
	start := time.Now()
	var safariTabsProgress safaritabs.ProgressFunc
	if opts.Progress != nil {
		safariTabsProgress = func(event safaritabs.ProgressEvent) {
			formatSafariTabsSyncProgress(opts.Progress, event)
		}
	}
	safariStats, err := runSafariTabsImport(ctx, cfg, st, safaritabs.Options{
		DBPath:    opts.SafariTabsDBPath,
		Device:    opts.SafariTabsDevice,
		Limit:     opts.SafariTabsLimit,
		OlderThan: opts.SafariTabsOlderThan,
		Force:     opts.Force,
		Progress:  safariTabsProgress,
	})
	stage := &SafariTabsStage{Duration: time.Since(start), Stats: safariStats}
	if err != nil {
		return stage, fmt.Errorf("import safari-tabs: %w", err)
	}
	progressf(opts.Progress, "Safari Tabs import complete: device=%s seen=%d matched=%d created=%d updated=%d unchanged=%d rendered=%d skipped=%d links=%d errors=%d (%s)\n", emptyProgressValue(safariStats.DeviceName), safariStats.TabsSeen, safariStats.TabsMatched, safariStats.TabsCreated, safariStats.TabsUpdated, safariStats.TabsUnchanged, safariStats.TabsRendered, safariStats.TabsSkipped, safariStats.LinksFound, safariStats.Errors, stage.Duration)
	return stage, nil
}
