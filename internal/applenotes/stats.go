package applenotes

import (
	"path"
	"strings"
)

func countAttachments(stats *Stats, attachments []Attachment) {
	stats.AttachmentsSeen += len(attachments)
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.BlockedReason) != "" {
			stats.AttachmentsBlocked++
		}
		if attachment.ExtractStatus == "ok" {
			stats.AttachmentsExtracted++
			if attachment.ExtractTool == attachmentOCRTool {
				stats.AttachmentsOCRed++
			}
		}
		if attachment.Text != "" || attachment.URL != "" || attachment.FileName != "" || attachment.Name != "" {
			stats.AttachmentsIndexed++
		}
	}
}

func totalAttachmentTextChars(doc NoteDocument) int {
	total := 0
	for _, text := range doc.AttachmentTexts {
		total += len(text)
	}
	for _, attachment := range doc.Attachments {
		total += len(attachment.Text)
	}
	return total
}

func exclusionReason(doc NoteDocument, opts Options) string {
	if doc.Shared && opts.ExcludeShared {
		return "shared"
	}
	if doc.PasswordProtected && !opts.IncludeLocked {
		return "locked"
	}
	if matchesAny(doc.AccountName, opts.ExcludeAccounts) {
		return "account"
	}
	if matchesAny(doc.FolderPath, opts.ExcludeFolders) {
		return "folder"
	}
	return ""
}

func countSkip(stats *Stats, reason string) {
	stats.NotesSkipped++
	switch reason {
	case "account":
		stats.ExcludedAccounts++
	case "folder":
		stats.ExcludedFolders++
	case "shared":
		stats.ExcludedShared++
	case "locked":
		stats.NotesBlocked++
		stats.BlockedLocked++
	case "empty_decoded":
		stats.NotesBlocked++
		stats.BlockedEmpty++
	default:
		stats.NotesBlocked++
	}
}

func matchesAny(value string, patterns []string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	valueLower := strings.ToLower(value)
	leafLower := strings.ToLower(path.Base(value))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		patternLower := strings.ToLower(pattern)
		if valueLower == patternLower || leafLower == patternLower {
			return true
		}
		if ok, _ := path.Match(patternLower, valueLower); ok {
			return true
		}
	}
	return false
}

func containsIgnoreMarker(value string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower("[[dbrain-ignore]]"))
}
