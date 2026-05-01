package applenotes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/ledongthuc/pdf"
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

var errAttachmentTooLarge = errors.New("attachment too large")

func resolveAttachmentSourcePath(value, sourceDBPath string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), true
	}
	if strings.TrimSpace(sourceDBPath) == "" {
		return "", false
	}
	base := filepath.Dir(sourceDBPath)
	return filepath.Clean(filepath.Join(base, value)), true
}

func copyAttachmentFile(sourcePath, tempDir, fileName string, maxBytes int64) (string, func(), error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("attachment source %s is not a regular file", sourcePath)
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return "", nil, fmt.Errorf("%w: %s is %d bytes, limit %d", errAttachmentTooLarge, sourcePath, info.Size(), maxBytes)
	}

	in, err := os.Open(sourcePath)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		_ = in.Close()
	}()

	pattern := "attachment-*"
	if ext := filepath.Ext(fileName); ext != "" {
		pattern += ext
	} else if ext := filepath.Ext(sourcePath); ext != "" {
		pattern += ext
	}
	out, err := os.CreateTemp(tempDir, pattern)
	if err != nil {
		return "", nil, err
	}
	localPath := out.Name()
	cleanup := func() {
		_ = os.Remove(localPath)
	}
	var copied int64
	if maxBytes > 0 {
		copied, err = io.Copy(out, io.LimitReader(in, maxBytes+1))
	} else {
		copied, err = io.Copy(out, in)
	}
	if err != nil {
		_ = out.Close()
		cleanup()
		return "", nil, err
	}
	if maxBytes > 0 && copied > maxBytes {
		_ = out.Close()
		cleanup()
		return "", nil, fmt.Errorf("%w: %s grew beyond limit %d while copying", errAttachmentTooLarge, sourcePath, maxBytes)
	}
	if err := out.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	if sameFile(sourcePath, localPath) {
		cleanup()
		return "", nil, fmt.Errorf("attachment copy %s aliases source %s", localPath, sourcePath)
	}
	return localPath, cleanup, nil
}

func classifyAttachmentKind(attachment Attachment, localPath string) string {
	mimeType := strings.ToLower(strings.TrimSpace(attachment.MIMEType))
	uti := strings.ToLower(strings.TrimSpace(attachment.UTI))
	ext := strings.ToLower(filepath.Ext(firstNonEmpty(attachment.FileName, localPath, attachment.FilePath)))
	if strings.Contains(mimeType, "pdf") || strings.Contains(uti, "pdf") {
		return "pdf"
	}
	if strings.HasPrefix(mimeType, "image/") || strings.Contains(uti, "image") {
		return "image"
	}
	if strings.HasPrefix(mimeType, "text/") || strings.Contains(uti, "plain-text") || strings.Contains(uti, "html") {
		return "text"
	}
	if ext == ".pdf" {
		return "pdf"
	}
	if isImageExt(ext) {
		return "image"
	}
	if isTextExt(ext) {
		return "text"
	}
	if detected := detectFileKind(localPath); detected != "" {
		return detected
	}
	return ""
}

func detectFileKind(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() {
		_ = f.Close()
	}()
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	if n == 0 {
		return ""
	}
	sniffed := strings.ToLower(http.DetectContentType(buf[:n]))
	switch {
	case strings.Contains(sniffed, "pdf"):
		return "pdf"
	case strings.HasPrefix(sniffed, "image/"):
		return "image"
	case strings.HasPrefix(sniffed, "text/"):
		return "text"
	default:
		return ""
	}
}

func extractPlainTextFile(path string, maxBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
	}()
	limit := maxBytes
	if limit <= 0 {
		limit = defaultAttachmentMaxBytes
	}
	return readLimitedText(f, limit)
}

func extractPDFText(path string) (string, error) {
	f, reader, err := pdf.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
	}()
	textReader, err := reader.GetPlainText()
	if err != nil {
		return "", err
	}
	return readLimitedText(textReader, defaultAttachmentMaxBytes)
}

func readLimitedText(reader io.Reader, limit int64) (string, error) {
	if limit <= 0 {
		limit = defaultAttachmentMaxBytes
	}
	var b strings.Builder
	if _, err := io.Copy(&b, io.LimitReader(reader, limit+1)); err != nil {
		return "", err
	}
	if int64(b.Len()) > limit {
		return "", fmt.Errorf("%w: extracted text exceeds %d bytes", errAttachmentTooLarge, limit)
	}
	return strings.TrimSpace(b.String()), nil
}

func ocrImageFile(ctx context.Context, path, binary string, timeout time.Duration) (string, error) {
	ocrCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ocrCtx, binary, path, "stdout")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, context.Canceled) {
			return "", context.Canceled
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", fmt.Errorf("tesseract: %s", errMsg)
	}
	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return "", fmt.Errorf("tesseract returned no text")
	}
	return text, nil
}

func blockAttachment(attachment *Attachment, reason string, err error) {
	attachment.BlockedReason = strings.TrimSpace(reason)
	attachment.ExtractStatus = "blocked"
	if err != nil {
		attachment.ExtractError = err.Error()
	}
}

func refreshDocumentAttachmentFields(doc *NoteDocument) {
	if doc == nil {
		return
	}
	texts := append([]string{}, doc.AttachmentTexts...)
	for _, attachment := range doc.Attachments {
		if strings.TrimSpace(attachment.Text) != "" {
			texts = append(texts, splitAttachmentText(attachment.Text)...)
		}
	}
	doc.AttachmentTexts = dedupeTextValues(texts)
	doc.Links = extractLinks(doc.Text + "\n" + doc.Snippet + "\n" + attachmentLinksInput(doc.Attachments) + "\n" + strings.Join(doc.AttachmentTexts, "\n"))
}

func splitAttachmentText(value string) []string {
	parts := strings.Split(value, "\n\n")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func isImageExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".tif", ".tiff", ".bmp", ".heic", ".heif":
		return true
	default:
		return false
	}
}

func isTextExt(ext string) bool {
	switch ext {
	case ".txt", ".md", ".markdown", ".csv", ".tsv", ".json", ".jsonl", ".yaml", ".yml", ".xml", ".html", ".htm", ".log", ".text":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
