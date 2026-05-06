package applenotes

import (
	"context"

	"github.com/darron/dbrain/internal/config"
)

func readRunDocuments(ctx context.Context, cfg config.Config, opts Options) ([]NoteDocument, SnapshotInfo, bool, error) {
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
		return nil, SnapshotInfo{}, false, err
	}
	return docs, snapshot, deferAttachmentEnrichment, nil
}

func initialRunStats(snapshot SnapshotInfo, opts Options) Stats {
	return Stats{
		SourceDBPath: snapshot.SourceDBPath,
		Snapshot:     snapshot,
		DryRun:       opts.DryRun,
		Applied:      !opts.DryRun,
	}
}

func progressEventForDocument(index int, total int, doc NoteDocument) ProgressEvent {
	return ProgressEvent{
		Index:           index + 1,
		Total:           total,
		SourceKey:       doc.SourceKey,
		Title:           doc.Title,
		Links:           len(doc.Links),
		Attachments:     len(doc.Attachments),
		TextChars:       len(doc.Text),
		AttachmentChars: totalAttachmentTextChars(doc),
	}
}
