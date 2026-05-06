package applenotes

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/config"
)

func ReadDocuments(ctx context.Context, cfg config.Config, opts Options) ([]NoteDocument, SnapshotInfo, error) {
	if sourcePath, err := resolveNotesDBPath(opts.DBPath); err == nil {
		emitProgress(opts, ProgressEvent{Phase: "snapshotting", Reason: sourcePath})
	} else {
		emitProgress(opts, ProgressEvent{Phase: "snapshotting"})
	}
	info, cleanup, err := CreateSnapshot(cfg, opts)
	if err != nil {
		return nil, SnapshotInfo{}, err
	}
	emitProgress(opts, ProgressEvent{Phase: "snapshot", Reason: info.DBPath})
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	db, err := openSnapshotDB(info.DBPath)
	if err != nil {
		return nil, info, err
	}
	defer func() {
		_ = db.Close()
	}()
	if err := validateSnapshotDB(ctx, db); err != nil {
		return nil, info, err
	}

	docs, err := readDocumentsFromDB(ctx, db, opts)
	if err != nil {
		return nil, info, err
	}
	emitProgress(opts, ProgressEvent{Phase: "decoded", Total: len(docs)})
	for index, doc := range docs {
		emitProgress(opts, ProgressEvent{
			Phase:           "decoded_note",
			Index:           index + 1,
			Total:           len(docs),
			SourceKey:       doc.SourceKey,
			Title:           doc.Title,
			Links:           len(doc.Links),
			Attachments:     len(doc.Attachments),
			TextChars:       len(doc.Text),
			AttachmentChars: totalAttachmentTextChars(doc),
		})
	}
	if !opts.SkipAttachments {
		docs, err = enrichAttachmentFiles(ctx, cfg, docs, opts, info.SourceDBPath)
		if err != nil {
			return nil, info, err
		}
	}
	return docs, info, nil
}

func DecodeNote(ctx context.Context, cfg config.Config, opts Options, noteID string) (NoteDocument, SnapshotInfo, error) {
	noteID = strings.TrimSpace(noteID)
	if noteID == "" {
		return NoteDocument{}, SnapshotInfo{}, fmt.Errorf("note id is required")
	}
	docs, info, err := ReadDocuments(ctx, cfg, opts)
	if err != nil {
		return NoteDocument{}, info, err
	}
	for _, doc := range docs {
		if doc.ExternalID == noteID || doc.SourceKey == noteID || sanitizeIdentity(doc.ExternalID) == sanitizeIdentity(noteID) {
			return doc, info, nil
		}
	}
	return NoteDocument{}, info, fmt.Errorf("apple note %q not found", noteID)
}

func readDocumentsFromDB(ctx context.Context, db *sql.DB, opts Options) ([]NoteDocument, error) {
	objectColumns, err := tableColumns(ctx, db, "ZICCLOUDSYNCINGOBJECT")
	if err != nil {
		return nil, err
	}
	if len(objectColumns) == 0 {
		return nil, fmt.Errorf("apple notes object table ZICCLOUDSYNCINGOBJECT has no columns")
	}
	bodyByNotePK, bodyByDataPK := loadNoteData(ctx, db)
	objectRows, err := loadObjectRows(ctx, db)
	if err != nil {
		return nil, err
	}
	attachmentsByNotePK := loadAttachmentsFromRows(objectRows)

	var docs []NoteDocument
	for _, row := range objectRows {
		if !rowLooksLikeNote(row) {
			continue
		}
		pk, _ := int64Value(row, "Z_PK")
		doc := documentFromRow(row, bodyByNotePK, bodyByDataPK, attachmentsByNotePK[pk])
		if doc.Deleted {
			continue
		}
		docs = append(docs, doc)
		if opts.Limit > 0 && len(docs) >= opts.Limit {
			break
		}
	}
	return docs, nil
}
