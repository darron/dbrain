package applenotes

import (
	"strconv"
	"strings"
)

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
		SourceKey:         appleNoteSourceKey(externalID),
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

func appleNoteSourceKey(externalID string) string {
	return "apple-note:default:" + sanitizeIdentity(externalID)
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
