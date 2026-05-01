package applenotes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
)

func TestEnrichAttachmentFilesOCRsImagesWithLocalTesseract(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	imagePath := filepath.Join(root, "photo.png")
	if err := os.WriteFile(imagePath, []byte("fake image bytes"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	tesseract := filepath.Join(root, "tesseract")
	if err := os.WriteFile(tesseract, []byte("#!/bin/sh\nprintf 'image ocr text\\n'\n"), 0o700); err != nil {
		t.Fatalf("write fake tesseract: %v", err)
	}

	docs := []NoteDocument{{
		SourceKey:  "apple-note:default:ocr",
		ExternalID: "ocr",
		Title:      "OCR",
		Text:       "body",
		Attachments: []Attachment{{
			ID:       "a1",
			FileName: "photo.png",
			FilePath: imagePath,
			MIMEType: "image/png",
		}},
	}}
	enriched, err := enrichAttachmentFiles(context.Background(), cfg, docs, Options{
		TesseractBinary: tesseract,
		Timeout:         5 * time.Second,
	}, "")
	if err != nil {
		t.Fatalf("enrichAttachmentFiles: %v", err)
	}
	attachment := enriched[0].Attachments[0]
	if attachment.ExtractStatus != "ok" || attachment.ExtractTool != attachmentOCRTool {
		t.Fatalf("attachment extract status/tool = %q/%q", attachment.ExtractStatus, attachment.ExtractTool)
	}
	if !strings.Contains(attachment.Text, "image ocr text") {
		t.Fatalf("attachment text = %q", attachment.Text)
	}
	if len(enriched[0].AttachmentTexts) != 1 || enriched[0].AttachmentTexts[0] != "image ocr text" {
		t.Fatalf("AttachmentTexts = %#v", enriched[0].AttachmentTexts)
	}
}

func TestEnrichAttachmentFilesMarksMissingFilesBlocked(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	docs := []NoteDocument{{
		SourceKey:  "apple-note:default:missing",
		ExternalID: "missing",
		Title:      "Missing",
		Text:       "body",
		Attachments: []Attachment{{
			ID:       "a1",
			FileName: "missing.txt",
			FilePath: filepath.Join(root, "missing.txt"),
			MIMEType: "text/plain",
		}},
	}}
	enriched, err := enrichAttachmentFiles(context.Background(), cfg, docs, Options{}, "")
	if err != nil {
		t.Fatalf("enrichAttachmentFiles: %v", err)
	}
	attachment := enriched[0].Attachments[0]
	if attachment.ExtractStatus != "blocked" || attachment.BlockedReason != "file_missing" {
		t.Fatalf("attachment blocked state = %q/%q error=%q", attachment.ExtractStatus, attachment.BlockedReason, attachment.ExtractError)
	}
}

func TestEnrichAttachmentFilesMarksExtractionTooLargeBlocked(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	textPath := filepath.Join(root, "note.txt")
	if err := os.WriteFile(textPath, []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("write text: %v", err)
	}
	docs := []NoteDocument{{
		SourceKey:  "apple-note:default:too-large",
		ExternalID: "too-large",
		Title:      "Too Large",
		Text:       "body",
		Attachments: []Attachment{{
			ID:       "a1",
			FileName: "note.txt",
			FilePath: textPath,
			MIMEType: "text/plain",
		}},
	}}
	enriched, err := enrichAttachmentFiles(context.Background(), cfg, docs, Options{AttachmentMaxBytes: 5}, "")
	if err != nil {
		t.Fatalf("enrichAttachmentFiles: %v", err)
	}
	attachment := enriched[0].Attachments[0]
	if attachment.ExtractStatus != "blocked" || attachment.BlockedReason != "too_large" {
		t.Fatalf("attachment blocked state = %q/%q error=%q", attachment.ExtractStatus, attachment.BlockedReason, attachment.ExtractError)
	}
}
