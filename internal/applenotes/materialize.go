package applenotes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/vault"
)

func appendSampleTitle(titles []string, title string) []string {
	if len(titles) >= 20 {
		return titles
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled Apple Note"
	}
	return append(titles, title)
}

func itemFromDocument(doc NoteDocument, now time.Time) (model.Item, error) {
	linksJSON, err := json.Marshal(doc.Links)
	if err != nil {
		return model.Item{}, fmt.Errorf("encode apple note links: %w", err)
	}
	raw := map[string]any{
		"account_name":       doc.AccountName,
		"folder_path":        doc.FolderPath,
		"apple_note_tags":    doc.AppleNoteTags,
		"blocked_reason":     doc.BlockedReason,
		"password_protected": doc.PasswordProtected,
		"shared":             doc.Shared,
		"attachments":        doc.Attachments,
		"attachment_texts":   doc.AttachmentTexts,
		"raw":                doc.Raw,
	}
	rawJSON, err := json.Marshal(raw)
	if err != nil {
		return model.Item{}, fmt.Errorf("encode apple note raw metadata: %w", err)
	}

	year := "unknown"
	if doc.CreatedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, doc.CreatedAt); err == nil {
			year = fmt.Sprintf("%04d", parsed.Year())
		}
	}
	noteSlug := noteSlug(doc.Title, doc.ExternalID)
	item := model.Item{
		SourceKey:    doc.SourceKey,
		SourceType:   sourceType,
		ExternalID:   doc.ExternalID,
		CanonicalURL: doc.CanonicalURL,
		Title:        doc.Title,
		PublishedAt:  doc.CreatedAt,
		SavedAt:      doc.CreatedAt,
		SyncedAt:     now.Format(time.RFC3339),
		Text:         doc.Text,
		ArticleTitle: attachmentArticleTitle(doc),
		ArticleText:  attachmentArticleText(doc),
		LinksJSON:    string(linksJSON),
		FolderNames:  doc.FolderPath,
		NotePath:     vault.NoteRelativePath("apple-notes", year, noteSlug),
		RawJSON:      string(rawJSON),
		LastSeenAt:   now,
		UpdatedAt:    now,
	}
	item.ContentHash = itemhash.Compute(item)
	return item, nil
}

func attachmentArticleTitle(doc NoteDocument) string {
	if len(doc.AttachmentTexts) == 0 && len(doc.Attachments) == 0 {
		return ""
	}
	return "Apple Notes Attachment Text"
}

func attachmentArticleText(doc NoteDocument) string {
	var b strings.Builder
	for _, text := range doc.AttachmentTexts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(text)
	}
	for _, attachment := range doc.Attachments {
		metadata := renderAttachmentMetadata(attachment)
		if metadata == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(metadata)
	}
	return strings.TrimSpace(b.String())
}

func renderAttachmentMetadata(attachment Attachment) string {
	var lines []string
	if attachment.Name != "" {
		lines = append(lines, "Name: "+attachment.Name)
	}
	if attachment.FileName != "" {
		lines = append(lines, "File: "+attachment.FileName)
	}
	if attachment.URL != "" {
		lines = append(lines, "URL: "+attachment.URL)
	}
	if attachment.MIMEType != "" {
		lines = append(lines, "MIME: "+attachment.MIMEType)
	}
	if attachment.UTI != "" {
		lines = append(lines, "UTI: "+attachment.UTI)
	}
	if attachment.ByteSize > 0 {
		lines = append(lines, fmt.Sprintf("Bytes: %d", attachment.ByteSize))
	}
	if attachment.ExtractStatus != "" {
		lines = append(lines, "Extract: "+attachment.ExtractStatus)
	}
	if attachment.ExtractTool != "" {
		lines = append(lines, "Extract Tool: "+attachment.ExtractTool)
	}
	if attachment.BlockedReason != "" {
		lines = append(lines, "Blocked: "+attachment.BlockedReason)
	}
	if attachment.Text != "" {
		lines = append(lines, "Text:\n"+attachment.Text)
	}
	return strings.Join(lines, "\n")
}

func noteSlug(title string, externalID string) string {
	base := strings.TrimSpace(title)
	if base == "" {
		base = externalID
	}
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(base) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "note"
	}
	sum := sha256.Sum256([]byte(externalID + "\x00" + title))
	return slug + "-" + hex.EncodeToString(sum[:])[:12]
}
