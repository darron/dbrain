package applenotes

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	readOpts := opts
	deferAttachmentEnrichment := false
	if !opts.DryRun {
		// In applied mode, read all candidate notes so unchanged-current rows do
		// not consume the batch and repeated runs advance. Attachment file work
		// is deferred until candidate planning says work may be needed, then the
		// plan is rechecked after enrichment before any unchanged row is written.
		readOpts.Limit = 0
		if !opts.SkipAttachments {
			readOpts.SkipAttachments = true
			deferAttachmentEnrichment = true
		}
	}
	docs, snapshot, err := ReadDocuments(ctx, cfg, readOpts)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{
		SourceDBPath: snapshot.SourceDBPath,
		Snapshot:     snapshot,
		DryRun:       opts.DryRun,
		Applied:      !opts.DryRun,
	}
	now := time.Now().UTC()
	emitProgress(opts, ProgressEvent{Phase: "loaded", Total: len(docs)})

	processedWork := 0
	for index, doc := range docs {
		stats.NotesSeen++
		event := ProgressEvent{
			Index:           index + 1,
			Total:           len(docs),
			SourceKey:       doc.SourceKey,
			Title:           doc.Title,
			Links:           len(doc.Links),
			Attachments:     len(doc.Attachments),
			TextChars:       len(doc.Text),
			AttachmentChars: totalAttachmentTextChars(doc),
		}
		if skipReason := exclusionReason(doc, opts); skipReason != "" {
			countAttachments(&stats, doc.Attachments)
			if opts.ForgetExcluded && stats.Applied {
				purged, err := purgeExcluded(ctx, cfg, st, doc, skipReason)
				if err != nil {
					stats.Errors++
					return stats, err
				}
				if purged {
					stats.NotesPurged++
				}
			}
			countSkip(&stats, skipReason)
			event.Phase = "skipped"
			event.Reason = skipReason
			emitProgress(opts, event)
			continue
		}
		if doc.BlockedReason != "" {
			countAttachments(&stats, doc.Attachments)
			countSkip(&stats, doc.BlockedReason)
			event.Phase = "blocked"
			event.Reason = doc.BlockedReason
			emitProgress(opts, event)
			continue
		}
		if containsIgnoreMarker(doc.Text) {
			countAttachments(&stats, doc.Attachments)
			if opts.ForgetExcluded && stats.Applied {
				purged, err := purgeExcluded(ctx, cfg, st, doc, "ignore_marker")
				if err != nil {
					stats.Errors++
					return stats, err
				}
				if purged {
					stats.NotesPurged++
				}
			}
			stats.NotesSkipped++
			event.Phase = "skipped"
			event.Reason = "ignore_marker"
			emitProgress(opts, event)
			continue
		}

		stats.NotesMatched++
		stats.LinksDiscovered += len(doc.Links)
		if stats.DryRun && opts.ShowTitles {
			stats.SampleTitles = appendSampleTitle(stats.SampleTitles, doc.Title)
		}
		if stats.DryRun {
			countAttachments(&stats, doc.Attachments)
			event.Phase = "dry_run"
			event.Status = "would_import"
			emitProgress(opts, event)
			continue
		}
		if st == nil {
			return stats, fmt.Errorf("store is required for Apple Notes import")
		}

		item, err := itemFromDocument(doc, now)
		if err != nil {
			stats.Errors++
			return stats, err
		}
		plan, err := planAppleNoteWork(ctx, cfg, st, opts, item)
		if err != nil {
			stats.Errors++
			return stats, err
		}
		if !plan.Actionable {
			countAttachments(&stats, doc.Attachments)
			stats.NotesUnchanged++
			event.Phase = "unchanged"
			event.Status = "current"
			emitProgress(opts, event)
			continue
		}
		if opts.Limit > 0 && processedWork >= opts.Limit {
			break
		}
		if deferAttachmentEnrichment {
			enriched, err := enrichSingleDocumentAttachments(ctx, cfg, doc, opts, snapshot.SourceDBPath)
			if err != nil {
				stats.Errors++
				return stats, err
			}
			doc = enriched
			item, err = itemFromDocument(doc, now)
			if err != nil {
				stats.Errors++
				return stats, err
			}
			plan, err = planAppleNoteWork(ctx, cfg, st, opts, item)
			if err != nil {
				stats.Errors++
				return stats, err
			}
			event.Links = len(doc.Links)
			event.Attachments = len(doc.Attachments)
			event.TextChars = len(doc.Text)
			event.AttachmentChars = totalAttachmentTextChars(doc)
			if !plan.Actionable {
				countAttachments(&stats, doc.Attachments)
				stats.NotesUnchanged++
				event.Phase = "unchanged"
				event.Status = "current"
				emitProgress(opts, event)
				continue
			}
		}
		countAttachments(&stats, doc.Attachments)
		processedWork++

		event.Phase = "processing"
		if plan.Reason != "" {
			event.Reason = plan.Reason
		}
		emitProgress(opts, event)

		result, err := st.UpsertItem(ctx, item)
		if err != nil {
			stats.Errors++
			return stats, err
		}
		stats.NotesImported++
		switch result.Status {
		case model.UpsertCreated:
			stats.NotesCreated++
		case model.UpsertUpdated:
			stats.NotesUpdated++
		case model.UpsertUnchanged:
			stats.NotesUnchanged++
		}

		shouldRender := opts.Force || result.Status != model.UpsertUnchanged || plan.RenderNeeded
		if !shouldRender {
			if _, err := vault.StatNote(cfg, item.NotePath); err != nil {
				shouldRender = true
			}
		}
		if shouldRender {
			if err := vault.WriteItem(cfg, item); err != nil {
				stats.Errors++
				return stats, fmt.Errorf("render apple note %s: %w", item.SourceKey, err)
			}
			stats.NotesRendered++
		}
		event.Phase = "imported"
		event.Status = string(result.Status)
		event.Rendered = shouldRender
		event.SummaryStatus = "skipped"
		if opts.Summarize {
			event.Phase = "summarizing"
			event.Status = string(result.Status)
			event.Rendered = shouldRender
			event.SummaryStatus = "running"
			emitProgress(opts, event)

			summarized, err := summarizeAppleNote(ctx, cfg, st, opts, result.ItemID, item)
			if err != nil {
				stats.SummaryErrors++
				stats.Errors++
				event.Phase = "imported"
				event.SummaryStatus = "error"
				event.Reason = err.Error()
				emitProgress(opts, event)
				continue
			}
			if summarized {
				stats.SummariesCreated++
				stats.NotesRendered++
				event.SummaryStatus = "ok"
				event.SummaryChanged = true
			} else {
				event.SummaryStatus = "current"
			}
		}
		event.Phase = "imported"
		emitProgress(opts, event)
	}

	return stats, nil
}
