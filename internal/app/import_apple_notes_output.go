package app

import (
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/applenotes"
)

func writeAppleNotesStats(dst interface{ Write([]byte) (int, error) }, stats applenotes.Stats) {
	mode := "dry-run"
	if stats.Applied {
		mode = "applied"
	}
	_, _ = fmt.Fprintf(dst, "Mode:      %s\n", mode)
	_, _ = fmt.Fprintf(dst, "Seen:      %d\n", stats.NotesSeen)
	_, _ = fmt.Fprintf(dst, "Matched:   %d\n", stats.NotesMatched)
	_, _ = fmt.Fprintf(dst, "Imported:  %d\n", stats.NotesImported)
	_, _ = fmt.Fprintf(dst, "Created:   %d\n", stats.NotesCreated)
	_, _ = fmt.Fprintf(dst, "Updated:   %d\n", stats.NotesUpdated)
	_, _ = fmt.Fprintf(dst, "Unchanged: %d\n", stats.NotesUnchanged)
	_, _ = fmt.Fprintf(dst, "Rendered:  %d\n", stats.NotesRendered)
	_, _ = fmt.Fprintf(dst, "Skipped:   %d\n", stats.NotesSkipped)
	_, _ = fmt.Fprintf(dst, "Blocked:   %d\n", stats.NotesBlocked)
	_, _ = fmt.Fprintf(dst, "Purged:    %d\n", stats.NotesPurged)
	_, _ = fmt.Fprintf(dst, "Attachments: seen=%d indexed=%d extracted=%d ocr=%d blocked=%d\n", stats.AttachmentsSeen, stats.AttachmentsIndexed, stats.AttachmentsExtracted, stats.AttachmentsOCRed, stats.AttachmentsBlocked)
	_, _ = fmt.Fprintf(dst, "Links:     %d\n", stats.LinksDiscovered)
	_, _ = fmt.Fprintf(dst, "Summaries: %d\n", stats.SummariesCreated)
	_, _ = fmt.Fprintf(dst, "Errors:    %d\n", stats.Errors)
	if len(stats.SampleTitles) > 0 {
		_, _ = fmt.Fprintf(dst, "Sample titles:\n")
		for _, title := range stats.SampleTitles {
			_, _ = fmt.Fprintf(dst, "- %s\n", title)
		}
	}
}

func writeAppleNotesProgress(dst interface{ Write([]byte) (int, error) }, event applenotes.ProgressEvent, showTitles bool) {
	if event.Phase == "" {
		return
	}
	if event.Phase == "loaded" {
		_, _ = fmt.Fprintf(dst, "Apple Notes loaded: candidates=%d\n", event.Total)
		return
	}
	if event.Phase == "snapshotting" {
		_, _ = fmt.Fprintf(dst, "Apple Notes snapshotting source=%s\n", emptyDash(event.Reason))
		return
	}
	if event.Phase == "snapshot" {
		_, _ = fmt.Fprintf(dst, "Apple Notes snapshot ready path=%s\n", emptyDash(event.Reason))
		return
	}
	if event.Phase == "decoded" {
		_, _ = fmt.Fprintf(dst, "Apple Notes decoded: candidates=%d\n", event.Total)
		return
	}

	position := ""
	if event.Index > 0 && event.Total > 0 {
		position = fmt.Sprintf(" %d/%d", event.Index, event.Total)
	}
	source := event.SourceKey
	if source == "" {
		source = "unknown"
	}
	title := ""
	if showTitles && strings.TrimSpace(event.Title) != "" {
		title = fmt.Sprintf(" title=%q", event.Title)
	}

	switch event.Phase {
	case "decoded_note", "unchanged":
		return
	case "attachments":
		if event.Total > 1 {
			return
		}
		_, _ = fmt.Fprintf(dst, "Apple Note%s attachments source=%s status=%s links=%d attachments=%d attachment_chars=%d%s\n",
			position, source, emptyDash(event.Status), event.Links, event.Attachments, event.AttachmentChars, title)
	case "attachment":
		if event.Total > 1 {
			return
		}
		_, _ = fmt.Fprintf(dst, "Apple Note%s attachment source=%s ordinal=%d status=%s reason=%s attachment_chars=%d%s\n",
			position, source, event.Attachments, emptyDash(event.Status), emptyDash(event.Reason), event.AttachmentChars, title)
	case "processing":
		if event.Reason == "summary" {
			return
		}
		_, _ = fmt.Fprintf(dst, "Apple Note%s processing source=%s reason=%s links=%d attachments=%d text_chars=%d attachment_chars=%d%s\n",
			position, source, emptyDash(event.Reason), event.Links, event.Attachments, event.TextChars, event.AttachmentChars, title)
	case "summarizing":
		_, _ = fmt.Fprintf(dst, "Apple Note%s summarizing source=%s status=%s%s\n",
			position, source, emptyDash(event.Status), title)
	case "imported":
		if event.Status == "unchanged" && !event.Rendered && (event.SummaryStatus == "ok" || event.SummaryStatus == "current") {
			return
		}
		_, _ = fmt.Fprintf(dst, "Apple Note%s imported source=%s status=%s rendered=%t summary=%s links=%d attachments=%d%s\n",
			position, source, emptyDash(event.Status), event.Rendered, emptyDash(event.SummaryStatus), event.Links, event.Attachments, title)
	case "skipped", "blocked":
		_, _ = fmt.Fprintf(dst, "Apple Note%s %s source=%s reason=%s%s\n",
			position, event.Phase, source, emptyDash(event.Reason), title)
	case "dry_run":
		_, _ = fmt.Fprintf(dst, "Apple Note%s dry-run source=%s status=%s links=%d attachments=%d text_chars=%d attachment_chars=%d%s\n",
			position, source, emptyDash(event.Status), event.Links, event.Attachments, event.TextChars, event.AttachmentChars, title)
	default:
		_, _ = fmt.Fprintf(dst, "Apple Note%s %s source=%s status=%s reason=%s%s\n",
			position, event.Phase, source, emptyDash(event.Status), emptyDash(event.Reason), title)
	}
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
