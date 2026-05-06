package applenotes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
)

const (
	defaultAttachmentMaxBytes = 50 << 20
	attachmentPDFTool         = "pdf-text"
	attachmentTextTool        = "text-file"
	attachmentOCRTool         = "tesseract"
)

func enrichAttachmentFiles(ctx context.Context, cfg config.Config, docs []NoteDocument, opts Options, sourceDBPath string) ([]NoteDocument, error) {
	if len(docs) == 0 {
		return docs, nil
	}
	maxBytes := opts.AttachmentMaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultAttachmentMaxBytes
	}
	tesseractBinary := strings.TrimSpace(opts.TesseractBinary)
	if tesseractBinary == "" {
		tesseractBinary = "tesseract"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	tempDir, err := cfg.MkdirTemp("apple-notes-attachments-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	for docIdx := range docs {
		emitProgress(opts, ProgressEvent{
			Phase:           "attachments",
			Index:           docIdx + 1,
			Total:           len(docs),
			SourceKey:       docs[docIdx].SourceKey,
			Title:           docs[docIdx].Title,
			Status:          "start",
			Links:           len(docs[docIdx].Links),
			Attachments:     len(docs[docIdx].Attachments),
			TextChars:       len(docs[docIdx].Text),
			AttachmentChars: totalAttachmentTextChars(docs[docIdx]),
		})
		for attachmentIdx := range docs[docIdx].Attachments {
			if err := ctx.Err(); err != nil {
				return docs, err
			}
			emitProgress(opts, ProgressEvent{
				Phase:       "attachment",
				Index:       docIdx + 1,
				Total:       len(docs),
				SourceKey:   docs[docIdx].SourceKey,
				Title:       docs[docIdx].Title,
				Status:      "extracting",
				Attachments: attachmentIdx + 1,
			})
			enrichAttachmentFile(ctx, tempDir, sourceDBPath, maxBytes, timeout, tesseractBinary, opts.SkipAttachmentOCR, &docs[docIdx].Attachments[attachmentIdx])
			attachment := docs[docIdx].Attachments[attachmentIdx]
			status := attachment.ExtractStatus
			if status == "" {
				status = "metadata"
			}
			reason := attachment.BlockedReason
			if reason == "" && attachment.ExtractError != "" {
				reason = attachment.ExtractError
			}
			emitProgress(opts, ProgressEvent{
				Phase:           "attachment",
				Index:           docIdx + 1,
				Total:           len(docs),
				SourceKey:       docs[docIdx].SourceKey,
				Title:           docs[docIdx].Title,
				Status:          status,
				Reason:          reason,
				Attachments:     attachmentIdx + 1,
				AttachmentChars: len(attachment.Text),
			})
		}
		refreshDocumentAttachmentFields(&docs[docIdx])
		emitProgress(opts, ProgressEvent{
			Phase:           "attachments",
			Index:           docIdx + 1,
			Total:           len(docs),
			SourceKey:       docs[docIdx].SourceKey,
			Title:           docs[docIdx].Title,
			Status:          "done",
			Links:           len(docs[docIdx].Links),
			Attachments:     len(docs[docIdx].Attachments),
			TextChars:       len(docs[docIdx].Text),
			AttachmentChars: totalAttachmentTextChars(docs[docIdx]),
		})
	}
	return docs, nil
}

func enrichSingleDocumentAttachments(ctx context.Context, cfg config.Config, doc NoteDocument, opts Options, sourceDBPath string) (NoteDocument, error) {
	if opts.SkipAttachments {
		return doc, nil
	}
	docs, err := enrichAttachmentFiles(ctx, cfg, []NoteDocument{doc}, opts, sourceDBPath)
	if err != nil {
		return doc, err
	}
	if len(docs) == 0 {
		return doc, nil
	}
	return docs[0], nil
}

func enrichAttachmentFile(ctx context.Context, tempDir, sourceDBPath string, maxBytes int64, timeout time.Duration, tesseractBinary string, skipOCR bool, attachment *Attachment) {
	if attachment == nil || strings.TrimSpace(attachment.FilePath) == "" {
		return
	}
	sourcePath, ok := resolveAttachmentSourcePath(attachment.FilePath, sourceDBPath)
	if !ok {
		blockAttachment(attachment, "file_unresolved", fmt.Errorf("cannot resolve attachment path %q", attachment.FilePath))
		return
	}
	localPath, cleanup, err := copyAttachmentFile(sourcePath, tempDir, attachment.FileName, maxBytes)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		reason := "extract_failed"
		if errors.Is(err, os.ErrNotExist) {
			reason = "file_missing"
		} else if errors.Is(err, errAttachmentTooLarge) {
			reason = "too_large"
		}
		blockAttachment(attachment, reason, err)
		return
	}

	kind := classifyAttachmentKind(*attachment, localPath)
	var text, tool string
	switch kind {
	case "pdf":
		text, err = extractPDFText(localPath)
		tool = attachmentPDFTool
	case "text":
		text, err = extractPlainTextFile(localPath, maxBytes)
		tool = attachmentTextTool
	case "image":
		if skipOCR {
			attachment.ExtractStatus = "skipped"
			attachment.ExtractError = "attachment OCR disabled"
			return
		}
		text, err = ocrImageFile(ctx, localPath, tesseractBinary, timeout)
		tool = attachmentOCRTool
	default:
		blockAttachment(attachment, "unsupported_type", fmt.Errorf("unsupported attachment type mime=%q uti=%q file=%q", attachment.MIMEType, attachment.UTI, attachment.FileName))
		return
	}
	if err != nil {
		reason := "extract_failed"
		if errors.Is(err, errAttachmentTooLarge) {
			reason = "too_large"
		} else if kind == "image" {
			reason = "ocr_failed"
		}
		blockAttachment(attachment, reason, err)
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		blockAttachment(attachment, "empty_extract", fmt.Errorf("attachment extraction returned no text"))
		return
	}
	attachment.Text = strings.Join(dedupeTextValues(append(splitAttachmentText(attachment.Text), text)), "\n\n")
	attachment.ExtractStatus = "ok"
	attachment.ExtractTool = tool
	attachment.ExtractError = ""
	attachment.BlockedReason = ""
}
