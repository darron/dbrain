package applenotes

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	docs, snapshot, deferAttachmentEnrichment, err := readRunDocuments(ctx, cfg, opts)
	if err != nil {
		return Stats{}, err
	}

	stats := initialRunStats(snapshot, opts)
	now := time.Now().UTC()
	emitProgress(opts, ProgressEvent{Phase: "loaded", Total: len(docs)})

	processedWork := 0
	for index, doc := range docs {
		stats.NotesSeen++
		event := progressEventForDocument(index, len(docs), doc)
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
			event = progressEventForDocument(index, len(docs), doc)
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
		recordAppleNoteUpsertStats(&stats, result.Status)

		shouldRender, err := renderImportedAppleNote(ctx, cfg, st, opts, item, result, plan)
		if err != nil {
			stats.Errors++
			return stats, err
		}
		if shouldRender {
			stats.NotesRendered++
		}
		event.Phase = "imported"
		event.Status = string(result.Status)
		event.Rendered = shouldRender
		event.SummaryStatus = "skipped"
		if !summarizeImportedAppleNote(ctx, cfg, st, opts, result, item, &stats, &event) {
			continue
		}
		event.Phase = "imported"
		emitProgress(opts, event)
	}

	return stats, nil
}
