package app

import (
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/darron/dbrain/internal/syncjob"
)

func writeSyncStats(dst interface{ Write([]byte) (int, error) }, stats syncjob.Stats) error {
	if _, err := fmt.Fprintf(dst, "\nSync Summary\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Started:   %s\n", stats.StartedAt.Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Completed: %s\n", stats.CompletedAt.Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Duration:  %s\n\n", formatSyncDuration(stats.Duration)); err != nil {
		return err
	}

	rows := syncSummaryRows(stats)
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers("Stage", "Duration", "Primary", "Secondary", "Errors").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().Padding(0, 1)
			if col == 1 {
				base = base.Align(lipgloss.Right)
			}
			if row == table.HeaderRow {
				return base.Bold(true).Foreground(lipgloss.Color("39"))
			}
			if col == 4 && row >= 0 && rows[row][4] != "0" {
				return base.Foreground(lipgloss.Color("196"))
			}
			if col == 0 {
				return base.Bold(true)
			}
			return base
		})

	if _, err := fmt.Fprintln(dst, t.String()); err != nil {
		return err
	}
	return nil
}

func syncSummaryRows(stats syncjob.Stats) [][]string {
	rows := make([][]string, 0, 10)
	if stats.AppleNotes != nil {
		s := stats.AppleNotes.Stats
		rows = append(rows, []string{"Apple Notes", formatSyncDuration(stats.AppleNotes.Duration), fmt.Sprintf("imported=%d rendered=%d", s.NotesImported, s.NotesRendered), fmt.Sprintf("skipped=%d blocked=%d attachments=%d extracted=%d summarized=%d", s.NotesSkipped, s.NotesBlocked, s.AttachmentsIndexed, s.AttachmentsExtracted, s.SummariesCreated), strconv.Itoa(s.Errors)})
	}
	if stats.SafariTabs != nil {
		s := stats.SafariTabs.Stats
		rows = append(rows, []string{"Safari Tabs", formatSyncDuration(stats.SafariTabs.Duration), fmt.Sprintf("created=%d updated=%d", s.TabsCreated, s.TabsUpdated), fmt.Sprintf("unchanged=%d rendered=%d skipped=%d links=%d device=%s", s.TabsUnchanged, s.TabsRendered, s.TabsSkipped, s.LinksFound, emptyDash(s.DeviceName)), strconv.Itoa(s.Errors)})
	}
	if stats.XBookmarks != nil {
		s := stats.XBookmarks.Stats
		rows = append(rows, []string{"X Bookmarks", formatSyncDuration(stats.XBookmarks.Duration), fmt.Sprintf("created=%d updated=%d", s.Created, s.Updated), fmt.Sprintf("unchanged=%d pages=%d stopped=%s", s.Unchanged, s.PagesFetched, s.StoppedReason), "0"})
	}
	if stats.X != nil {
		s := stats.X.Stats
		rows = append(rows, []string{"X Hydration", formatSyncDuration(stats.X.Duration), fmt.Sprintf("hydrated=%d rendered=%d", s.Hydrated, s.Rendered), fmt.Sprintf("media_downloaded=%d blocked=%d missing=%d", s.MediaDownloaded, s.MediaBlocked, s.Missing), strconv.Itoa(s.APIErrors + s.MediaErrors)})
	}
	if stats.XMedia != nil {
		s := stats.XMedia.Stats
		rows = append(rows, []string{"X Media", formatSyncDuration(stats.XMedia.Duration), fmt.Sprintf("processed=%d transcribed=%d", s.ItemsProcessed, s.MediaTranscribed), fmt.Sprintf("summarized=%d skipped=%d", s.ItemsSummarized, s.ItemsSkipped), strconv.Itoa(s.Errors + s.SummaryErrors)})
	}
	if stats.XPhotoOCR != nil {
		s := stats.XPhotoOCR.Stats
		rows = append(rows, []string{"X Photo OCR", formatSyncDuration(stats.XPhotoOCR.Duration), fmt.Sprintf("processed=%d ocr=%d", s.ItemsProcessed, s.PhotosOCRed), fmt.Sprintf("updated=%d skipped=%d", s.ItemsUpdated, s.ItemsSkipped), strconv.Itoa(s.Errors)})
	}
	if stats.Links != nil {
		s := stats.Links.Stats
		rows = append(rows, []string{"Links", formatSyncDuration(stats.Links.Duration), fmt.Sprintf("items_scanned=%d", s.ItemsScanned), fmt.Sprintf("queued=%d summarized=%d", s.SourcesQueued, s.SourcesSummarized), strconv.Itoa(s.Errors)})
	}
	if stats.GitHub != nil {
		s := stats.GitHub.Stats
		rows = append(rows, []string{"GitHub", formatSyncDuration(stats.GitHub.Duration), fmt.Sprintf("stars=%d created=%d", s.StarsProcessed, s.ItemsCreated), fmt.Sprintf("summarized=%d", s.SourcesSummarized), strconv.Itoa(s.Errors)})
	}
	if stats.YouTube != nil {
		s := stats.YouTube.Stats
		rows = append(rows, []string{"YouTube", formatSyncDuration(stats.YouTube.Duration), fmt.Sprintf("items=%d", s.ItemsProcessed), fmt.Sprintf("summarized=%d", s.SourcesSummarized), strconv.Itoa(s.Errors)})
	}
	if stats.Feeds != nil {
		s := stats.Feeds.Stats
		rows = append(rows, []string{"Feeds", formatSyncDuration(stats.Feeds.Duration), fmt.Sprintf("checked=%d entries=%d", s.FeedsChecked, s.EntriesSeen), fmt.Sprintf("changed=%d unchanged=%d created=%d updated=%d", s.FeedsChanged, s.FeedsUnchanged, s.ItemsCreated, s.ItemsUpdated), strconv.Itoa(s.Errors)})
	}
	if stats.Sources != nil {
		s := stats.Sources.Stats
		rows = append(rows, []string{"Sources", formatSyncDuration(stats.Sources.Duration), fmt.Sprintf("cycles=%d summarized=%d", s.WorkCycles, s.SourcesSummarized), fmt.Sprintf("stopped=%s", s.StoppedReason), strconv.Itoa(s.Errors)})
	}
	if stats.Categorize != nil {
		s := stats.Categorize.Stats
		items := stats.Categorize.ItemStats
		sources := stats.Categorize.SourceStats
		rows = append(rows, []string{"Categorize", formatSyncDuration(stats.Categorize.Duration), fmt.Sprintf("items=%d/%d sources=%d/%d", items.Applied, items.Queued, sources.Applied, sources.Queued), fmt.Sprintf("succeeded=%d skipped=%d", s.Succeeded, s.Skipped), strconv.Itoa(s.Errors)})
	}
	if stats.MediaArchive != nil {
		s := stats.MediaArchive.Stats
		rows = append(rows, []string{"Media Archive", formatSyncDuration(stats.MediaArchive.Duration), fmt.Sprintf("uploaded=%d archived=%d", s.Uploaded, s.Archived), fmt.Sprintf("pruned_files=%d unchanged=%d", s.LocalFilesPruned, s.Unchanged), strconv.Itoa(s.Errors)})
	}
	return rows
}

func formatSyncDuration(duration time.Duration) string {
	return fmt.Sprintf("%.2fs", duration.Seconds())
}
