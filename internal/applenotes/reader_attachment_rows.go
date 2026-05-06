package applenotes

import (
	"path/filepath"
	"strconv"
	"strings"
)

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
