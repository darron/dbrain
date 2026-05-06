package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/safaritabs"
)

func writeSafariTabsStats(dst interface{ Write([]byte) (int, error) }, stats safaritabs.Stats) {
	mode := "dry-run"
	if stats.Applied {
		mode = "applied"
	}
	_, _ = fmt.Fprintf(dst, "Mode:      %s\n", mode)
	_, _ = fmt.Fprintf(dst, "Device:    %s (%s)\n", emptyDash(stats.DeviceName), emptyDash(stats.DeviceUUID))
	_, _ = fmt.Fprintf(dst, "Seen:      %d\n", stats.TabsSeen)
	_, _ = fmt.Fprintf(dst, "Matched:   %d\n", stats.TabsMatched)
	_, _ = fmt.Fprintf(dst, "Imported:  %d\n", stats.TabsImported)
	_, _ = fmt.Fprintf(dst, "Created:   %d\n", stats.TabsCreated)
	_, _ = fmt.Fprintf(dst, "Updated:   %d\n", stats.TabsUpdated)
	_, _ = fmt.Fprintf(dst, "Unchanged: %d\n", stats.TabsUnchanged)
	_, _ = fmt.Fprintf(dst, "Rendered:  %d\n", stats.TabsRendered)
	_, _ = fmt.Fprintf(dst, "Skipped:   %d\n", stats.TabsSkipped)
	_, _ = fmt.Fprintf(dst, "Links:     %d\n", stats.LinksFound)
	_, _ = fmt.Fprintf(dst, "Errors:    %d\n", stats.Errors)
	if len(stats.SampleTitles) > 0 {
		_, _ = fmt.Fprintf(dst, "Sample titles:\n")
		for _, title := range stats.SampleTitles {
			_, _ = fmt.Fprintf(dst, "- %s\n", title)
		}
	}
}

func writeSafariTabsProgress(dst interface{ Write([]byte) (int, error) }, event safaritabs.ProgressEvent, showTitles bool) {
	if event.Phase == "" {
		return
	}
	if event.Phase == "loaded" {
		_, _ = fmt.Fprintf(dst, "Safari tabs loaded: candidates=%d\n", event.Total)
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
	case "dry_run":
		_, _ = fmt.Fprintf(dst, "Safari Tab%s would_import source=%s url=%s%s\n", position, source, emptyDash(event.URL), title)
	case "imported":
		if event.Status == "unchanged" && !event.Rendered {
			return
		}
		_, _ = fmt.Fprintf(dst, "Safari Tab%s imported source=%s status=%s rendered=%t%s\n", position, source, emptyDash(event.Status), event.Rendered, title)
	case "skipped":
		if event.Reason == "newer_than_cutoff" {
			return
		}
		_, _ = fmt.Fprintf(dst, "Safari Tab%s skipped reason=%s url=%s%s\n", position, emptyDash(event.Reason), emptyDash(event.URL), title)
	}
}

func formatCLIOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
