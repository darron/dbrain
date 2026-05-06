package syncjob

import (
	"fmt"
	"io"
	"strings"

	"github.com/darron/dbrain/internal/applenotes"
	"github.com/darron/dbrain/internal/safaritabs"
)

func progressf(dst io.Writer, format string, args ...any) {
	if dst == nil {
		return
	}
	_, _ = fmt.Fprintf(dst, format, args...)
}

func formatAppleNotesSyncProgress(dst io.Writer, event applenotes.ProgressEvent) {
	if dst == nil {
		return
	}
	switch event.Phase {
	case "processing":
		if event.Reason == "summary" {
			return
		}
		progressf(dst, "Apple Note%s processing source=%s reason=%s links=%d attachments=%d\n",
			appleNoteProgressPosition(event), appleNoteProgressSource(event), emptyProgressValue(event.Reason), event.Links, event.Attachments)
	case "summarizing":
		progressf(dst, "Apple Note%s summarizing source=%s item_status=%s summary=%s\n",
			appleNoteProgressPosition(event), appleNoteProgressSource(event), emptyProgressValue(event.Status), emptyProgressValue(event.SummaryStatus))
	case "imported":
		if event.Status == "unchanged" && !event.Rendered && (event.SummaryStatus == "ok" || event.SummaryStatus == "current") {
			return
		}
		progressf(dst, "Apple Note%s imported source=%s status=%s rendered=%t summary=%s links=%d attachments=%d\n",
			appleNoteProgressPosition(event), appleNoteProgressSource(event), emptyProgressValue(event.Status), event.Rendered, emptyProgressValue(event.SummaryStatus), event.Links, event.Attachments)
	case "skipped", "blocked":
		progressf(dst, "Apple Note%s %s source=%s reason=%s\n",
			appleNoteProgressPosition(event), event.Phase, appleNoteProgressSource(event), emptyProgressValue(event.Reason))
	}
}

func appleNoteProgressPosition(event applenotes.ProgressEvent) string {
	if event.Index <= 0 || event.Total <= 0 {
		return ""
	}
	return fmt.Sprintf(" %d/%d", event.Index, event.Total)
}

func appleNoteProgressSource(event applenotes.ProgressEvent) string {
	if strings.TrimSpace(event.SourceKey) == "" {
		return "unknown"
	}
	return event.SourceKey
}

func formatSafariTabsSyncProgress(dst io.Writer, event safaritabs.ProgressEvent) {
	if dst == nil {
		return
	}
	switch event.Phase {
	case "loaded":
		progressf(dst, "Safari tabs loaded: candidates=%d\n", event.Total)
	case "imported":
		if event.Status == "unchanged" && !event.Rendered {
			return
		}
		progressf(dst, "Safari Tab%s imported source=%s status=%s rendered=%t\n",
			safariTabProgressPosition(event), safariTabProgressSource(event), emptyProgressValue(event.Status), event.Rendered)
	case "skipped":
		if event.Reason == "newer_than_cutoff" {
			return
		}
		progressf(dst, "Safari Tab%s skipped reason=%s url=%s\n",
			safariTabProgressPosition(event), emptyProgressValue(event.Reason), emptyProgressValue(event.URL))
	}
}

func safariTabProgressPosition(event safaritabs.ProgressEvent) string {
	if event.Index <= 0 || event.Total <= 0 {
		return ""
	}
	return fmt.Sprintf(" %d/%d", event.Index, event.Total)
}

func safariTabProgressSource(event safaritabs.ProgressEvent) string {
	if strings.TrimSpace(event.SourceKey) == "" {
		return "unknown"
	}
	return event.SourceKey
}

func emptyProgressValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
