package applenotes

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/config"

	_ "modernc.org/sqlite"
)

func TestReadDocumentsDecodesBodiesAndMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	sourceDB := filepath.Join(root, "NoteStore.sqlite")
	writeReaderFixture(t, sourceDB)

	docs, info, err := ReadDocuments(context.Background(), cfg, Options{DBPath: sourceDB})
	if err != nil {
		t.Fatalf("ReadDocuments: %v", err)
	}
	if info.DBPath == sourceDB {
		t.Fatalf("expected snapshot path, got source path")
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1: %#v", len(docs), docs)
	}
	doc := docs[0]
	if doc.SourceKey != "apple-note:default:note-1" {
		t.Fatalf("SourceKey = %q", doc.SourceKey)
	}
	if doc.Title != "Reader Note" {
		t.Fatalf("Title = %q", doc.Title)
	}
	if doc.Text != "Decoded body\n\nhttps://example.com/page #Research" {
		t.Fatalf("Text = %q", doc.Text)
	}
	if len(doc.Links) != 2 || doc.Links[0] != "https://example.com/page" || doc.Links[1] != "https://attachment.example/file.pdf" {
		t.Fatalf("Links = %#v", doc.Links)
	}
	if len(doc.AppleNoteTags) != 1 || doc.AppleNoteTags[0] != "research" {
		t.Fatalf("AppleNoteTags = %#v", doc.AppleNoteTags)
	}
	if len(doc.Attachments) != 1 || doc.Attachments[0].URL != "https://attachment.example/file.pdf" {
		t.Fatalf("Attachments = %#v", doc.Attachments)
	}
	if len(doc.AttachmentTexts) != 3 || doc.AttachmentTexts[0] != "Attachment OCR text" || doc.AttachmentTexts[1] != "PDF extracted text" || doc.AttachmentTexts[2] != "Plain attachment file text" {
		t.Fatalf("AttachmentTexts = %#v", doc.AttachmentTexts)
	}
	if doc.Attachments[0].ExtractStatus != "ok" || doc.Attachments[0].ExtractTool != attachmentTextTool {
		t.Fatalf("attachment extract status/tool = %q/%q", doc.Attachments[0].ExtractStatus, doc.Attachments[0].ExtractTool)
	}
	if doc.CreatedAt != "2001-01-01T00:01:40Z" {
		t.Fatalf("CreatedAt = %q", doc.CreatedAt)
	}
}

func TestReadDocumentsCanSkipAttachmentFileExtraction(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	sourceDB := filepath.Join(root, "NoteStore.sqlite")
	writeReaderFixture(t, sourceDB)

	docs, _, err := ReadDocuments(context.Background(), cfg, Options{DBPath: sourceDB, SkipAttachments: true})
	if err != nil {
		t.Fatalf("ReadDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d", len(docs))
	}
	doc := docs[0]
	if len(doc.AttachmentTexts) != 2 || doc.AttachmentTexts[0] != "Attachment OCR text" || doc.AttachmentTexts[1] != "PDF extracted text" {
		t.Fatalf("AttachmentTexts = %#v", doc.AttachmentTexts)
	}
	if doc.Attachments[0].ExtractStatus != "" {
		t.Fatalf("ExtractStatus = %q, want empty", doc.Attachments[0].ExtractStatus)
	}
}

func TestReadDocumentsMarksEmptyNotesBlockedAndDefersLockedPolicy(t *testing.T) {
	t.Parallel()

	row := map[string]any{
		"Z_PK":                 int64(9),
		"ZIDENTIFIER":          "locked",
		"ZTITLE1":              "Locked",
		"ZISPASSWORDPROTECTED": int64(1),
	}
	doc := documentFromRow(row, nil, nil, nil)
	if doc.BlockedReason != "empty_decoded" {
		t.Fatalf("BlockedReason = %q, want empty_decoded", doc.BlockedReason)
	}
	if reason := exclusionReason(doc, Options{}); reason != "locked" {
		t.Fatalf("exclusionReason = %q, want locked", reason)
	}
	if reason := exclusionReason(doc, Options{IncludeLocked: true}); reason != "" {
		t.Fatalf("IncludeLocked exclusionReason = %q, want empty", reason)
	}

	row = map[string]any{
		"Z_PK":        int64(10),
		"ZIDENTIFIER": "empty",
		"ZTITLE1":     "Empty",
	}
	doc = documentFromRow(row, nil, nil, nil)
	if doc.BlockedReason != "empty_decoded" {
		t.Fatalf("BlockedReason = %q, want empty_decoded", doc.BlockedReason)
	}
}

func TestRowLooksLikeNoteDoesNotTreatParentedNoteAsAttachment(t *testing.T) {
	t.Parallel()

	row := map[string]any{
		"Z_PK":        int64(7),
		"ZIDENTIFIER": "note-with-parent",
		"ZTITLE1":     "Note With Parent",
		"ZSNIPPET":    "body",
		"ZPARENT":     int64(2),
	}
	if !rowLooksLikeNote(row) {
		t.Fatalf("row with note title/body and parent metadata should still be treated as a note")
	}
	if rowLooksLikeAttachment(row) {
		t.Fatalf("row with only broad parent metadata should not be treated as an attachment")
	}
}

func writeReaderFixture(t *testing.T, path string) {
	t.Helper()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()

	for _, stmt := range []string{
		`CREATE TABLE ZICCLOUDSYNCINGOBJECT (
			Z_PK INTEGER PRIMARY KEY,
			ZIDENTIFIER TEXT,
			ZTITLE1 TEXT,
			ZSNIPPET TEXT,
			ZNOTEDATA INTEGER,
			ZCREATIONDATE1 REAL,
			ZMODIFICATIONDATE1 REAL,
			ZFOLDERPATH TEXT,
			ZNOTE INTEGER,
			ZURL TEXT,
			ZFILENAME TEXT,
			ZFILEURLSTRING TEXT,
			ZMIMETYPE TEXT,
			ZFILESIZE INTEGER,
			ZADDITIONALINDEXABLETEXT TEXT,
			ZISPASSWORDPROTECTED INTEGER DEFAULT 0,
			ZMARKEDFORDELETION INTEGER DEFAULT 0
		);`,
		`CREATE TABLE ZICNOTEDATA (
			Z_PK INTEGER PRIMARY KEY,
			ZDATA BLOB
		);`,
		`INSERT INTO ZICCLOUDSYNCINGOBJECT (
			Z_PK, ZIDENTIFIER, ZTITLE1, ZSNIPPET, ZNOTEDATA, ZCREATIONDATE1, ZMODIFICATIONDATE1, ZFOLDERPATH, ZADDITIONALINDEXABLETEXT
		) VALUES (
			1, 'note-1', 'Reader Note', 'snippet', 11, 100, 200, 'iCloud/Private', 'Attachment OCR text'
		);`,
		`INSERT INTO ZICCLOUDSYNCINGOBJECT (
			Z_PK, ZIDENTIFIER, ZTITLE1, ZSNIPPET, ZNOTEDATA, ZMARKEDFORDELETION
		) VALUES (
			2, 'deleted-note', 'Deleted Note', 'deleted', 12, 1
		);`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec fixture stmt %q: %v", stmt, err)
		}
	}
	body := []byte{0x0a, 0x0c}
	body = append(body, []byte("Decoded body")...)
	linkAndTag := []byte("https://example.com/page #Research")
	body = append(body, 0x12, byte(len(linkAndTag)))
	body = append(body, linkAndTag...)
	if _, err := db.Exec(`INSERT INTO ZICNOTEDATA (Z_PK, ZDATA) VALUES (?, ?)`, 11, body); err != nil {
		t.Fatalf("insert body: %v", err)
	}
	attachmentPath := filepath.Join(filepath.Dir(path), "attachment.txt")
	if err := os.WriteFile(attachmentPath, []byte("Plain attachment file text"), 0o600); err != nil {
		t.Fatalf("write attachment file: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO ZICCLOUDSYNCINGOBJECT (
		Z_PK, ZIDENTIFIER, ZNOTE, ZURL, ZFILENAME, ZFILEURLSTRING, ZMIMETYPE, ZFILESIZE, ZADDITIONALINDEXABLETEXT
	) VALUES (
		3, 'attachment-1', 1, 'https://attachment.example/file.pdf', 'file.pdf', ?, 'text/plain', 1234, 'PDF extracted text'
	)`, attachmentPath); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
}
