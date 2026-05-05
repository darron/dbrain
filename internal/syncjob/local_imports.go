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

func executeAppleNotesStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (*AppleNotesStage, error) {
	common := opts.Common
	stageOpts := opts.AppleNotes
	progressf(common.Progress, "==> import apple-notes\n")
	start := time.Now()
	var appleNotesProgress applenotes.ProgressFunc
	if common.Progress != nil {
		appleNotesProgress = func(event applenotes.ProgressEvent) {
			formatAppleNotesSyncProgress(common.Progress, event)
		}
	}
	appleStats, err := runAppleNotesImport(ctx, cfg, st, applenotes.Options{
		DBPath:             stageOpts.DBPath,
		Limit:              stageOpts.Limit,
		Force:              common.Force,
		ExcludeFolders:     stageOpts.ExcludeFolders,
		ExcludeAccounts:    stageOpts.ExcludeAccounts,
		ExcludeShared:      stageOpts.ExcludeShared,
		IncludeLocked:      stageOpts.IncludeLocked,
		SkipAttachments:    stageOpts.SkipAttachments,
		SkipAttachmentOCR:  stageOpts.SkipAttachmentOCR,
		AttachmentMaxBytes: stageOpts.AttachmentMaxBytes,
		TesseractBinary:    stageOpts.TesseractBinary,
		Summarize:          common.Summarize,
		SummaryModel:       common.Model,
		SummaryCLI:         common.CLI,
		SummaryLength:      common.Length,
		Timeout:            common.Timeout,
		Progress:           appleNotesProgress,
	})
	stage := &AppleNotesStage{Duration: time.Since(start), Stats: appleStats}
	if err != nil {
		return stage, fmt.Errorf("import apple-notes: %w", err)
	}
	progressf(common.Progress, "Apple Notes import complete: seen=%d imported=%d rendered=%d skipped=%d blocked=%d attachments=%d extracted=%d ocr=%d summarized=%d errors=%d (%s)\n", appleStats.NotesSeen, appleStats.NotesImported, appleStats.NotesRendered, appleStats.NotesSkipped, appleStats.NotesBlocked, appleStats.AttachmentsIndexed, appleStats.AttachmentsExtracted, appleStats.AttachmentsOCRed, appleStats.SummariesCreated, appleStats.Errors, stage.Duration)
	return stage, nil
}

func executeSafariTabsStage(ctx context.Context, cfg config.Config, st *store.Store, opts stageOptions) (*SafariTabsStage, error) {
	common := opts.Common
	stageOpts := opts.SafariTabs
	progressf(common.Progress, "==> import safari-tabs\n")
	start := time.Now()
	var safariTabsProgress safaritabs.ProgressFunc
	if common.Progress != nil {
		safariTabsProgress = func(event safaritabs.ProgressEvent) {
			formatSafariTabsSyncProgress(common.Progress, event)
		}
	}
	safariStats, err := runSafariTabsImport(ctx, cfg, st, safaritabs.Options{
		DBPath:    stageOpts.DBPath,
		Device:    stageOpts.Device,
		Limit:     stageOpts.Limit,
		OlderThan: stageOpts.OlderThan,
		Force:     common.Force,
		Progress:  safariTabsProgress,
	})
	stage := &SafariTabsStage{Duration: time.Since(start), Stats: safariStats}
	if err != nil {
		return stage, fmt.Errorf("import safari-tabs: %w", err)
	}
	progressf(common.Progress, "Safari Tabs import complete: device=%s seen=%d matched=%d created=%d updated=%d unchanged=%d rendered=%d skipped=%d links=%d errors=%d (%s)\n", emptyProgressValue(safariStats.DeviceName), safariStats.TabsSeen, safariStats.TabsMatched, safariStats.TabsCreated, safariStats.TabsUpdated, safariStats.TabsUnchanged, safariStats.TabsRendered, safariStats.TabsSkipped, safariStats.LinksFound, safariStats.Errors, stage.Duration)
	return stage, nil
}
