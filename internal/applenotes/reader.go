package applenotes

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/darron/dbrain/internal/config"
)

var urlPattern = regexp.MustCompile(`https?://[^\s<>"')\]]+`)

type NoteDocument struct {
	SourceKey         string         `json:"source_key"`
	ExternalID        string         `json:"external_id"`
	CanonicalURL      string         `json:"canonical_url"`
	Title             string         `json:"title"`
	Text              string         `json:"text"`
	Snippet           string         `json:"snippet,omitempty"`
	AccountName       string         `json:"account_name,omitempty"`
	FolderPath        string         `json:"folder_path,omitempty"`
	CreatedAt         string         `json:"created_at,omitempty"`
	UpdatedAt         string         `json:"updated_at,omitempty"`
	PasswordProtected bool           `json:"password_protected"`
	Shared            bool           `json:"shared"`
	Deleted           bool           `json:"deleted"`
	BlockedReason     string         `json:"blocked_reason,omitempty"`
	Links             []string       `json:"links,omitempty"`
	AppleNoteTags     []string       `json:"apple_note_tags,omitempty"`
	Attachments       []Attachment   `json:"attachments,omitempty"`
	AttachmentTexts   []string       `json:"attachment_texts,omitempty"`
	Raw               map[string]any `json:"raw,omitempty"`
}

type Attachment struct {
	ID            string         `json:"id,omitempty"`
	Name          string         `json:"name,omitempty"`
	ContentID     string         `json:"content_id,omitempty"`
	URL           string         `json:"url,omitempty"`
	FileName      string         `json:"file_name,omitempty"`
	FilePath      string         `json:"file_path,omitempty"`
	MIMEType      string         `json:"mime_type,omitempty"`
	UTI           string         `json:"uti,omitempty"`
	ByteSize      int64          `json:"byte_size,omitempty"`
	CreatedAt     string         `json:"created_at,omitempty"`
	UpdatedAt     string         `json:"updated_at,omitempty"`
	Shared        bool           `json:"shared,omitempty"`
	Text          string         `json:"text,omitempty"`
	ExtractStatus string         `json:"extract_status,omitempty"`
	ExtractTool   string         `json:"extract_tool,omitempty"`
	ExtractedAt   string         `json:"extracted_at,omitempty"`
	ExtractError  string         `json:"extract_error,omitempty"`
	BlockedReason string         `json:"blocked_reason,omitempty"`
	Raw           map[string]any `json:"raw,omitempty"`
}

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

func loadObjectRows(ctx context.Context, db *sql.DB) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `SELECT * FROM ZICCLOUDSYNCINGOBJECT`)
	if err != nil {
		return nil, fmt.Errorf("read Apple Notes objects: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("load Apple Notes object columns: %w", err)
	}
	var objectRows []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		scan := make([]any, len(columns))
		for i := range values {
			scan[i] = &values[i]
		}
		if err := rows.Scan(scan...); err != nil {
			return nil, fmt.Errorf("scan Apple Notes object: %w", err)
		}
		objectRows = append(objectRows, valuesByColumn(columns, values))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Apple Notes objects: %w", err)
	}
	return objectRows, nil
}

func loadNoteData(ctx context.Context, db *sql.DB) (map[int64][]byte, map[int64][]byte) {
	columns, err := tableColumns(ctx, db, "ZICNOTEDATA")
	if err != nil || len(columns) == 0 {
		return nil, nil
	}
	if firstColumn(columns, "ZDATA") == "" {
		return nil, nil
	}

	rows, err := db.QueryContext(ctx, `SELECT * FROM ZICNOTEDATA`)
	if err != nil {
		return nil, nil
	}
	defer func() {
		_ = rows.Close()
	}()
	resultColumns, err := rows.Columns()
	if err != nil {
		return nil, nil
	}
	byNotePK := map[int64][]byte{}
	byDataPK := map[int64][]byte{}
	for rows.Next() {
		values := make([]any, len(resultColumns))
		scan := make([]any, len(resultColumns))
		for i := range values {
			scan[i] = &values[i]
		}
		if err := rows.Scan(scan...); err != nil {
			return nil, nil
		}
		row := valuesByColumn(resultColumns, values)
		data := bytesValue(row, "ZDATA")
		if len(data) == 0 {
			continue
		}
		if pk, ok := int64Value(row, "Z_PK"); ok {
			byDataPK[pk] = data
		}
		if notePK, ok := int64Value(row, "ZNOTE"); ok {
			byNotePK[notePK] = data
		}
	}
	return byNotePK, byDataPK
}

func documentFromRow(row map[string]any, bodyByNotePK map[int64][]byte, bodyByDataPK map[int64][]byte, attachments []Attachment) NoteDocument {
	pk, _ := int64Value(row, "Z_PK")
	externalID := firstStringValue(row, "ZIDENTIFIER", "ZSERVERRECORDID", "ZCLOUDKITRECORDID")
	if externalID == "" && pk > 0 {
		externalID = strconv.FormatInt(pk, 10)
	}
	title := firstStringValue(row, "ZTITLE1", "ZTITLE", "ZNAME")
	snippet := firstStringValue(row, "ZSNIPPET", "ZPLAINTEXT", "ZTEXT")
	body := snippet
	if data := bodyDataForRow(row, pk, bodyByNotePK, bodyByDataPK); len(data) > 0 {
		if decoded, err := DecodeZData(data); err == nil && strings.TrimSpace(decoded) != "" {
			body = decoded
		}
	}
	if title == "" {
		title = firstLine(body)
	}
	if title == "" {
		title = "Untitled Apple Note"
	}

	attachmentTexts := attachmentTextsFromRow(row)
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.Text) != "" {
			attachmentTexts = append(attachmentTexts, attachment.Text)
		}
	}
	attachmentText := strings.Join(attachmentTexts, "\n")
	doc := NoteDocument{
		SourceKey:         "apple-note:default:" + sanitizeIdentity(externalID),
		ExternalID:        externalID,
		CanonicalURL:      "apple-notes://default/" + sanitizeIdentity(externalID),
		Title:             title,
		Text:              body,
		Snippet:           snippet,
		AccountName:       firstStringValue(row, "ZACCOUNTNAME", "ZACCOUNT", "ZNAME"),
		FolderPath:        firstStringValue(row, "ZFOLDERPATH", "ZFOLDER", "ZTITLE2"),
		CreatedAt:         macTimeString(row, "ZCREATIONDATE1", "ZCREATIONDATE", "ZCREATEDATE"),
		UpdatedAt:         macTimeString(row, "ZMODIFICATIONDATE1", "ZMODIFICATIONDATE", "ZMODIFIEDDATE"),
		PasswordProtected: boolValue(row, "ZISPASSWORDPROTECTED", "ZPASSWORDPROTECTED"),
		Shared:            boolValue(row, "ZISSHARED", "ZSHARED"),
		Deleted:           boolValue(row, "ZMARKEDFORDELETION", "ZDELETED"),
		Links:             extractLinks(body + "\n" + snippet + "\n" + attachmentLinksInput(attachments) + "\n" + attachmentText),
		AppleNoteTags:     extractAppleNoteTags(body),
		Attachments:       attachments,
		AttachmentTexts:   dedupeTextValues(attachmentTexts),
		Raw:               rawRowForJSON(row),
	}
	if strings.TrimSpace(doc.Text) == "" {
		doc.BlockedReason = "empty_decoded"
	}
	return doc
}

func attachmentTextsFromRow(row map[string]any) []string {
	values := make([]string, 0, 4)
	seen := map[string]struct{}{}
	for _, name := range []string{"ZADDITIONALINDEXABLETEXT", "ZALTTEXT", "ZINDEXABLETEXT", "ZTRANSCRIPT"} {
		value := firstStringValue(row, name)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, value)
	}
	return values
}

func loadAttachmentsFromRows(rows []map[string]any) map[int64][]Attachment {
	attachmentsByNotePK := map[int64][]Attachment{}
	for _, row := range rows {
		if !rowLooksLikeAttachment(row) {
			continue
		}
		notePK, ok := attachmentNotePK(row)
		if !ok || notePK <= 0 {
			continue
		}
		if boolValue(row, "ZMARKEDFORDELETION", "ZDELETED") {
			continue
		}
		attachment := attachmentFromRow(row)
		if attachment.ID == "" && attachment.Name == "" && attachment.URL == "" && attachment.Text == "" {
			continue
		}
		attachmentsByNotePK[notePK] = append(attachmentsByNotePK[notePK], attachment)
	}
	return attachmentsByNotePK
}

func rowLooksLikeAttachment(row map[string]any) bool {
	if _, ok := attachmentNotePK(row); !ok {
		return false
	}
	if len(attachmentTextsFromRow(row)) > 0 {
		return true
	}
	for _, name := range []string{
		"ZCONTENTID", "ZCONTENTIDENTIFIER", "ZIDENTIFIER",
		"ZFILENAME", "ZFILENAME1", "ZFILEURL", "ZURL", "ZURLSTRING", "ZMEDIAURL",
		"ZUTI", "ZUNIFORMTYPEIDENTIFIER", "ZTYPEUTI", "ZMIMETYPE",
		"ZATTACHMENT", "ZATTACHMENT1", "ZATTACHMENTIDENTIFIER",
	} {
		if firstStringValue(row, name) != "" {
			return true
		}
	}
	for _, name := range []string{"ZFILESIZE", "ZSIZEINBYTES", "ZBYTESIZE"} {
		if value, ok := int64Value(row, name); ok && value > 0 {
			return true
		}
	}
	return false
}

func attachmentNotePK(row map[string]any) (int64, bool) {
	for _, name := range []string{"ZNOTE", "ZNOTE1", "ZPARENTNOTE", "ZATTACHEDTO", "ZOWNINGNOTE"} {
		if value, ok := int64Value(row, name); ok {
			return value, true
		}
	}
	return 0, false
}

func attachmentFromRow(row map[string]any) Attachment {
	pk, _ := int64Value(row, "Z_PK")
	id := firstStringValue(row, "ZIDENTIFIER", "ZCONTENTID", "ZCONTENTIDENTIFIER", "ZATTACHMENTIDENTIFIER")
	if id == "" && pk > 0 {
		id = strconv.FormatInt(pk, 10)
	}
	texts := attachmentTextsFromRow(row)
	filePath := firstFilePathValue(row, "ZFILEURL", "ZFILEURLSTRING", "ZMEDIAURL", "ZPATH", "ZFILEPATH", "ZFILENAME", "ZFILENAME1")
	attachment := Attachment{
		ID:        id,
		Name:      firstStringValue(row, "ZTITLE", "ZTITLE1", "ZNAME", "ZDISPLAYNAME"),
		ContentID: firstStringValue(row, "ZCONTENTID", "ZCONTENTIDENTIFIER"),
		URL:       firstHTTPURLValue(row, "ZURL", "ZURLSTRING", "ZMEDIAURL", "ZFILEURL", "ZFILEURLSTRING"),
		FileName:  firstStringValue(row, "ZFILENAME", "ZFILENAME1", "ZFILEURLSTRING"),
		FilePath:  filePath,
		MIMEType:  firstStringValue(row, "ZMIMETYPE", "ZCONTENTTYPE"),
		UTI:       firstStringValue(row, "ZUTI", "ZUNIFORMTYPEIDENTIFIER", "ZTYPEUTI"),
		CreatedAt: macTimeString(row,
			"ZCREATIONDATE1",
			"ZCREATIONDATE",
			"ZCREATEDATE",
		),
		UpdatedAt: macTimeString(row,
			"ZMODIFICATIONDATE1",
			"ZMODIFICATIONDATE",
			"ZMODIFIEDDATE",
		),
		Shared: boolValue(row, "ZISSHARED", "ZSHARED"),
		Text:   strings.Join(dedupeTextValues(texts), "\n\n"),
		Raw:    rawRowForJSON(row),
	}
	if attachment.FileName == "" && filePath != "" {
		attachment.FileName = filepath.Base(filePath)
	}
	for _, name := range []string{"ZFILESIZE", "ZSIZEINBYTES", "ZBYTESIZE"} {
		if value, ok := int64Value(row, name); ok && value > 0 {
			attachment.ByteSize = value
			break
		}
	}
	if attachment.Text == "" && attachment.URL == "" && attachment.FileName == "" {
		attachment.BlockedReason = "metadata_only"
	}
	return attachment
}

func firstHTTPURLValue(row map[string]any, names ...string) string {
	for _, name := range names {
		value := firstStringValue(row, name)
		if value == "" {
			continue
		}
		lower := strings.ToLower(value)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			return value
		}
	}
	return ""
}

func firstFilePathValue(row map[string]any, names ...string) string {
	for _, name := range names {
		value := firstStringValue(row, name)
		if value == "" {
			continue
		}
		if filePath := filePathFromString(value); filePath != "" {
			return filePath
		}
	}
	return ""
}

func filePathFromString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return ""
	}
	if strings.HasPrefix(lower, "file://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return ""
		}
		path := strings.TrimSpace(parsed.Path)
		if path == "" {
			return ""
		}
		if unescaped, err := url.PathUnescape(path); err == nil {
			path = unescaped
		}
		return path
	}
	if strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return ""
		}
		return filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	if filepath.IsAbs(value) {
		return value
	}
	if strings.Contains(value, "/") || strings.Contains(value, `\`) {
		return value
	}
	return ""
}

func attachmentLinksInput(attachments []Attachment) string {
	var b strings.Builder
	for _, attachment := range attachments {
		if attachment.URL != "" {
			b.WriteString(attachment.URL)
			b.WriteByte('\n')
		}
		if attachment.Text != "" {
			b.WriteString(attachment.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func dedupeTextValues(values []string) []string {
	seen := map[string]struct{}{}
	deduped := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, value)
	}
	return deduped
}

func bodyDataForRow(row map[string]any, pk int64, bodyByNotePK map[int64][]byte, bodyByDataPK map[int64][]byte) []byte {
	if data := bytesValue(row, "ZDATA"); len(data) > 0 {
		return data
	}
	if data, ok := bodyByNotePK[pk]; ok {
		return data
	}
	if dataPK, ok := int64Value(row, "ZNOTEDATA"); ok {
		if data, ok := bodyByDataPK[dataPK]; ok {
			return data
		}
	}
	if data, ok := bodyByDataPK[pk]; ok {
		return data
	}
	return nil
}

func rowLooksLikeNote(row map[string]any) bool {
	if rowLooksLikeAttachment(row) {
		return false
	}
	if strings.TrimSpace(firstStringValue(row, "ZTITLE1", "ZSNIPPET", "ZPLAINTEXT", "ZTEXT")) != "" {
		return true
	}
	if _, ok := int64Value(row, "ZNOTEDATA"); ok {
		return true
	}
	if bytesValue(row, "ZDATA") != nil {
		return true
	}
	if strings.TrimSpace(firstStringValue(row, "ZTITLE2", "ZNAME")) != "" {
		return false
	}
	return false
}
