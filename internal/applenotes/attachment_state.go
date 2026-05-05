package applenotes

import "strings"

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
