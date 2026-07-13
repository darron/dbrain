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

	cfg, container, sourceDBPath := appleNotesAttachmentTestContainer(t)
	imagePath := filepath.Join(container, "photo.png")
	if err := os.WriteFile(imagePath, []byte("fake image bytes"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	tesseract := filepath.Join(t.TempDir(), "tesseract")
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
	}, sourceDBPath)
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

	cfg, _, sourceDBPath := appleNotesAttachmentTestContainer(t)
	docs := []NoteDocument{{
		SourceKey:  "apple-note:default:missing",
		ExternalID: "missing",
		Title:      "Missing",
		Text:       "body",
		Attachments: []Attachment{{
			ID:       "a1",
			FileName: "missing.txt",
			FilePath: "missing.txt",
			MIMEType: "text/plain",
		}},
	}}
	enriched, err := enrichAttachmentFiles(context.Background(), cfg, docs, Options{}, sourceDBPath)
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

	cfg, container, sourceDBPath := appleNotesAttachmentTestContainer(t)
	textPath := filepath.Join(container, "note.txt")
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
	enriched, err := enrichAttachmentFiles(context.Background(), cfg, docs, Options{AttachmentMaxBytes: 5}, sourceDBPath)
	if err != nil {
		t.Fatalf("enrichAttachmentFiles: %v", err)
	}
	attachment := enriched[0].Attachments[0]
	if attachment.ExtractStatus != "blocked" || attachment.BlockedReason != "too_large" {
		t.Fatalf("attachment blocked state = %q/%q error=%q", attachment.ExtractStatus, attachment.BlockedReason, attachment.ExtractError)
	}
}

func TestEnrichAttachmentFilesRejectsContainerEscapes(t *testing.T) {
	t.Parallel()

	cfg, container, sourceDBPath := appleNotesAttachmentTestContainer(t)
	outsideDir := t.TempDir()
	absOutsidePath := filepath.Join(outsideDir, "absolute-outside.txt")
	traversalPath := filepath.Join(filepath.Dir(container), "traversal-outside.txt")
	parentOutsidePath := filepath.Join(outsideDir, "parent-outside.txt")
	for path, content := range map[string]string{
		absOutsidePath:    "ABSOLUTE_OUTSIDE_SENTINEL",
		traversalPath:     "TRAVERSAL_OUTSIDE_SENTINEL",
		parentOutsidePath: "PARENT_SYMLINK_OUTSIDE_SENTINEL",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write outside sentinel %s: %v", path, err)
		}
	}
	if err := os.Symlink(absOutsidePath, filepath.Join(container, "leaf-link.txt")); err != nil {
		t.Fatalf("create escaping leaf symlink: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(container, "parent-link")); err != nil {
		t.Fatalf("create escaping parent symlink: %v", err)
	}

	docs := []NoteDocument{{
		SourceKey:  "apple-note:default:escapes",
		ExternalID: "escapes",
		Title:      "Escapes",
		Text:       "body",
		Attachments: []Attachment{
			{ID: "absolute", FileName: "absolute.txt", FilePath: absOutsidePath, MIMEType: "text/plain"},
			{ID: "traversal", FileName: "traversal.txt", FilePath: "../traversal-outside.txt", MIMEType: "text/plain"},
			{ID: "leaf-symlink", FileName: "leaf-link.txt", FilePath: "leaf-link.txt", MIMEType: "text/plain"},
			{ID: "parent-symlink", FileName: "parent-outside.txt", FilePath: "parent-link/parent-outside.txt", MIMEType: "text/plain"},
		},
	}}

	enriched, err := enrichAttachmentFiles(context.Background(), cfg, docs, Options{}, sourceDBPath)
	if err != nil {
		t.Fatalf("enrichAttachmentFiles: %v", err)
	}
	for _, attachment := range enriched[0].Attachments {
		if attachment.ExtractStatus != "blocked" || attachment.BlockedReason != "outside_notes_container" {
			t.Fatalf("attachment %s blocked state = %q/%q error=%q", attachment.ID, attachment.ExtractStatus, attachment.BlockedReason, attachment.ExtractError)
		}
		for _, sentinel := range []string{"ABSOLUTE_OUTSIDE_SENTINEL", "TRAVERSAL_OUTSIDE_SENTINEL", "PARENT_SYMLINK_OUTSIDE_SENTINEL"} {
			if strings.Contains(attachment.Text, sentinel) {
				t.Fatalf("attachment %s copied outside sentinel %q: %q", attachment.ID, sentinel, attachment.Text)
			}
		}
	}
}

func TestEnrichAttachmentFilesExtractsContainedPaths(t *testing.T) {
	t.Parallel()

	cfg, container, sourceDBPath := appleNotesAttachmentTestContainer(t)
	mediaDir := filepath.Join(container, "Media")
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		t.Fatalf("create media dir: %v", err)
	}
	relativePath := filepath.Join(mediaDir, "relative.txt")
	absolutePath := filepath.Join(mediaDir, "absolute.txt")
	if err := os.WriteFile(relativePath, []byte("RELATIVE_CONTAINED_TEXT"), 0o600); err != nil {
		t.Fatalf("write relative attachment: %v", err)
	}
	if err := os.WriteFile(absolutePath, []byte("ABSOLUTE_CONTAINED_TEXT"), 0o600); err != nil {
		t.Fatalf("write absolute attachment: %v", err)
	}

	docs := []NoteDocument{{
		SourceKey:  "apple-note:default:contained",
		ExternalID: "contained",
		Title:      "Contained",
		Text:       "body",
		Attachments: []Attachment{
			{ID: "relative", FileName: "relative.txt", FilePath: filepath.Join("Media", "relative.txt"), MIMEType: "text/plain"},
			{ID: "absolute", FileName: "absolute.txt", FilePath: absolutePath, MIMEType: "text/plain"},
		},
	}}

	enriched, err := enrichAttachmentFiles(context.Background(), cfg, docs, Options{}, sourceDBPath)
	if err != nil {
		t.Fatalf("enrichAttachmentFiles: %v", err)
	}
	for index, want := range []string{"RELATIVE_CONTAINED_TEXT", "ABSOLUTE_CONTAINED_TEXT"} {
		attachment := enriched[0].Attachments[index]
		if attachment.ExtractStatus != "ok" || attachment.BlockedReason != "" || !strings.Contains(attachment.Text, want) {
			t.Fatalf("attachment %s state=%q/%q text=%q", attachment.ID, attachment.ExtractStatus, attachment.BlockedReason, attachment.Text)
		}
	}
}

func TestEnrichAttachmentFilesClassifiesContainedMalformedPathAsExtractFailed(t *testing.T) {
	t.Parallel()

	cfg, container, sourceDBPath := appleNotesAttachmentTestContainer(t)
	regularPath := filepath.Join(container, "regular.txt")
	if err := os.WriteFile(regularPath, []byte("contained regular file"), 0o600); err != nil {
		t.Fatalf("write contained regular file: %v", err)
	}
	docs := []NoteDocument{{
		SourceKey:  "apple-note:default:malformed-path",
		ExternalID: "malformed-path",
		Title:      "Malformed Path",
		Text:       "body",
		Attachments: []Attachment{{
			ID:       "not-a-directory",
			FileName: "child.txt",
			FilePath: filepath.Join("regular.txt", "child.txt"),
			MIMEType: "text/plain",
		}},
	}}

	enriched, err := enrichAttachmentFiles(context.Background(), cfg, docs, Options{}, sourceDBPath)
	if err != nil {
		t.Fatalf("enrichAttachmentFiles: %v", err)
	}
	attachment := enriched[0].Attachments[0]
	if attachment.ExtractStatus != "blocked" || attachment.BlockedReason != "extract_failed" {
		t.Fatalf("attachment blocked state = %q/%q error=%q", attachment.ExtractStatus, attachment.BlockedReason, attachment.ExtractError)
	}
}

func appleNotesAttachmentTestContainer(t *testing.T) (config.Config, string, string) {
	t.Helper()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	container := filepath.Join(root, "group.com.apple.notes")
	if err := os.MkdirAll(container, 0o700); err != nil {
		t.Fatalf("create Notes container: %v", err)
	}
	sourceDBPath := filepath.Join(container, "NoteStore.sqlite")
	if err := os.WriteFile(sourceDBPath, []byte("synthetic Notes database"), 0o600); err != nil {
		t.Fatalf("write synthetic Notes database: %v", err)
	}
	return cfg, container, sourceDBPath
}
