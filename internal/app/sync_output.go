package app

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
)

type syncSemanticResultOutput struct {
	syncjob.Stats
	Semantic   semanticRefreshResultOutput `json:"semantic"`
	SemanticGC *syncSemanticGCResult       `json:"semantic_gc,omitempty"`
	RunError   string                      `json:"run_error,omitempty"`
}

type syncSemanticErrorOutput struct {
	syncjob.Stats
	Semantic      semanticRefreshResultOutput   `json:"semantic"`
	SemanticError *semanticrefresh.RefreshError `json:"semantic_error"`
	RunError      string                        `json:"run_error,omitempty"`
}

func writeSyncSemanticResultJSON(
	dst io.Writer,
	stats syncjob.Stats,
	result semanticrefresh.Result,
	gc ...*syncSemanticGCResult,
) error {
	return writeSyncSemanticResultJSONWithRunError(dst, stats, result, nil, gc...)
}

func writeSyncSemanticResultJSONWithRunError(
	dst io.Writer,
	stats syncjob.Stats,
	result semanticrefresh.Result,
	runErr error,
	gc ...*syncSemanticGCResult,
) error {
	return writeJSON(dst, syncSemanticResultOutput{
		Stats:      stats,
		Semantic:   newSemanticRefreshResultOutput(result),
		SemanticGC: firstSyncSemanticGCResult(gc),
		RunError:   syncRunErrorText(runErr),
	})
}

func writeSyncSemanticErrorJSONWithRunError(
	dst io.Writer,
	stats syncjob.Stats,
	result semanticrefresh.Result,
	refreshErr *semanticrefresh.RefreshError,
	runErr error,
) error {
	return writeJSON(dst, syncSemanticErrorOutput{
		Stats:         stats,
		Semantic:      newSemanticRefreshResultOutput(result),
		SemanticError: refreshErr,
		RunError:      syncRunErrorText(runErr),
	})
}

func syncRunErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func writeSyncStats(dst interface{ Write([]byte) (int, error) }, stats syncjob.Stats) error {
	return writeSyncStatsWithSemantic(dst, stats, semanticrefresh.Result{}, nil)
}

func writeSyncStatsWithSemantic(
	dst interface{ Write([]byte) (int, error) },
	stats syncjob.Stats,
	semantic semanticrefresh.Result,
	semanticErr error,
	gc ...*syncSemanticGCResult,
) error {
	_, err := io.WriteString(dst, renderSyncStatsWithSemantic(outputWidth(dst), stats, semantic, semanticErr, gc...))
	return err
}

func renderSyncStatsWithSemantic(
	width int,
	stats syncjob.Stats,
	semantic semanticrefresh.Result,
	semanticErr error,
	gc ...*syncSemanticGCResult,
) string {
	if width < 80 {
		width = 80
	}
	width = clampTerminalWidth(width)

	var output strings.Builder
	fmt.Fprintf(&output, "\nSync Summary\n")
	fmt.Fprintf(&output, "Started:   %s\n", stats.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&output, "Completed: %s\n", stats.CompletedAt.Format(time.RFC3339))
	fmt.Fprintf(&output, "Duration:  %s\n\n", formatSyncDuration(stats.Duration))

	rows := syncSummaryRows(stats)
	rows = append(rows, semanticSyncSummaryRows(semantic, semanticErr)...)
	rows = append(rows, semanticGCSyncSummaryRows(firstSyncSemanticGCResult(gc))...)
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers("Stage", "Duration", "Primary", "Secondary", "Errors").
		Rows(rows...).
		Width(width).
		Wrap(true).
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
	fmt.Fprintln(&output, t.String())
	return output.String()
}

func completeSyncStatsWithSemantic(stats syncjob.Stats, result semanticrefresh.Result) syncjob.Stats {
	duration := nonnegativeDuration(result.Duration)
	stats.CompletedAt = stats.CompletedAt.Add(duration)
	stats.Duration = nonnegativeDuration(stats.CompletedAt.Sub(stats.StartedAt))
	return stats
}

func completeSyncStatsWithSemanticGC(stats syncjob.Stats, semantic semanticrefresh.Result, gc *syncSemanticGCResult) syncjob.Stats {
	stats = completeSyncStatsWithSemantic(stats, semantic)
	if gc == nil {
		return stats
	}
	stats.CompletedAt = stats.CompletedAt.Add(nonnegativeDuration(gc.Duration))
	stats.Duration = nonnegativeDuration(stats.CompletedAt.Sub(stats.StartedAt))
	return stats
}

func firstSyncSemanticGCResult(results []*syncSemanticGCResult) *syncSemanticGCResult {
	if len(results) == 0 {
		return nil
	}
	return results[0]
}

func semanticGCSyncSummaryRows(result *syncSemanticGCResult) [][]string {
	if result == nil {
		return nil
	}
	errorsCount := "0"
	if result.Status == syncSemanticGCStatusError {
		errorsCount = "1"
	}
	secondary := fmt.Sprintf(
		"status=%s generations=%d segments=%d members=%d",
		result.Status,
		result.GenerationsPruned,
		result.SegmentsPruned,
		result.MemberRowsPruned,
	)
	if result.SkipReason != "" {
		secondary += " reason=" + safeSemanticRefreshOutputField(result.SkipReason, 64)
	}
	if result.Error != "" {
		secondary += " error=" + safeSemanticRefreshOutputField(result.Error, 64)
	}
	return [][]string{{
		"Semantic GC",
		formatSyncDuration(result.Duration),
		fmt.Sprintf("dirs=%d bytes=%d", result.FilesystemDeleted, result.DeletedBytes),
		secondary,
		errorsCount,
	}}
}

func semanticSyncSummaryRows(result semanticrefresh.Result, resultErr error) [][]string {
	if len(result.Stages) == 0 {
		if result.Outcome == "" && result.SkipReason == "" && resultErr == nil {
			return nil
		}
		status := string(result.Outcome)
		if status == "" {
			status = "error"
		}
		secondary := fmt.Sprintf("status=%s capability=%s", status, emptyDash(string(result.Capability.State)))
		if result.SkipReason != "" {
			secondary += " reason=" + safeSemanticRefreshOutputField(result.SkipReason, 64)
		}
		errorsCount := "0"
		if resultErr != nil {
			errorsCount = "1"
		}
		return [][]string{{"Semantic Refresh", formatSyncDuration(result.Duration), "units=0", secondary, errorsCount}}
	}

	rows := make([][]string, 0, len(result.Stages))
	for _, stage := range result.Stages {
		primary, secondary := semanticStageSummaryCounts(stage)
		if stage.Units > 1 {
			secondary = fmt.Sprintf("units=%d %s", stage.Units, secondary)
		}
		errorsCount := "0"
		if stage.Status == "error" || stage.ErrorCode != "" {
			errorsCount = "1"
		}
		rows = append(rows, []string{
			"Semantic " + semanticStageDisplayName(stage.Stage),
			formatSyncDuration(stage.Duration),
			primary,
			strings.TrimSpace(secondary),
			errorsCount,
		})
	}
	return rows
}

func semanticStageSummaryCounts(stage semanticrefresh.StageStats) (string, string) {
	switch stage.Stage {
	case "projection":
		return fmt.Sprintf("parents=%d", stage.Counters.ProjectedParents), fmt.Sprintf("dirty=%d", stage.RemainingDebt.DirtyParents)
	case "embedding":
		return fmt.Sprintf("chunks=%d", stage.Counters.EmbeddedChunks), fmt.Sprintf("pending=%d retries=%d blocked=%d failed=%d", stage.RemainingDebt.PendingEmbeddings, stage.RemainingDebt.DueRetries, stage.RemainingDebt.BlockedEmbeddings, stage.RemainingDebt.FailedEmbeddings)
	case "flush":
		return fmt.Sprintf("vectors=%d", stage.Counters.FlushedVectors), fmt.Sprintf("indexed=%d l0=%d", stage.RemainingDebt.Indexed, stage.RemainingDebt.L0Ready)
	case "compaction":
		return fmt.Sprintf("vectors=%d", stage.Counters.CompactedVectors), fmt.Sprintf("segments=%d l0=%d", stage.RemainingDebt.Segments, stage.RemainingDebt.L0Ready)
	case "verify":
		return fmt.Sprintf("vectors=%d", stage.Counters.VerifiedVectors), fmt.Sprintf("indexed=%d segments=%d", stage.RemainingDebt.Indexed, stage.RemainingDebt.Segments)
	case "readiness":
		return fmt.Sprintf("indexed=%d", stage.RemainingDebt.Indexed), fmt.Sprintf("dirty=%d pending=%d failed=%d", stage.RemainingDebt.DirtyParents, stage.RemainingDebt.PendingEmbeddings, stage.RemainingDebt.FailedEmbeddings)
	default:
		return fmt.Sprintf("units=%d", stage.Units), "status=" + emptyDash(stage.Status)
	}
}

func semanticStageDisplayName(stage store.SemanticRefreshStage) string {
	value := string(stage)
	if value == "" {
		return "Refresh"
	}
	return strings.ToUpper(value[:1]) + value[1:]
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
	if stats.BlueskyBookmarks != nil {
		s := stats.BlueskyBookmarks.Stats
		rows = append(rows, []string{"Bluesky Bookmarks", formatSyncDuration(stats.BlueskyBookmarks.Duration), fmt.Sprintf("created=%d updated=%d", s.Created, s.Updated), fmt.Sprintf("unchanged=%d skipped=%d blocked=%d not_found=%d pages=%d stopped=%s media_discovered=%d media_linked=%d media_unavailable=%d media_downloaded=%d media_gone=%d media_errors=%d media_blocked=%d quote_linked=%d quote_skipped=%d", s.Unchanged, s.Skipped, s.SkippedBlocked, s.SkippedNotFound, s.PagesFetched, s.StoppedReason, s.MediaDiscovered, s.MediaLinked, s.MediaUnavailable, s.MediaDownloaded, s.MediaGone, s.MediaErrors, s.MediaBlocked, s.QuoteLinked, s.QuoteSkipped), strconv.Itoa(s.MediaErrors)})
	}
	if stats.MastodonBookmarks != nil {
		s := stats.MastodonBookmarks.Stats
		rows = append(rows, []string{"Mastodon Bookmarks", formatSyncDuration(stats.MastodonBookmarks.Duration), fmt.Sprintf("created=%d updated=%d", s.Created, s.Updated), fmt.Sprintf("account=%s pages=%d unchanged=%d skipped=%d stopped=%s media_discovered=%d media_linked=%d media_unavailable=%d media_downloaded=%d media_gone=%d media_errors=%d media_blocked=%d api_errors=%d rate_limits=%d retries=%d", emptyDash(s.AccountKey), s.PagesFetched, s.Unchanged, s.Skipped, s.StoppedReason, s.MediaDiscovered, s.MediaLinked, s.MediaUnavailable, s.MediaDownloaded, s.MediaGone, s.MediaErrors, s.MediaBlocked, s.APIErrors, s.RateLimits, s.Retries), strconv.Itoa(s.MediaErrors + s.APIErrors)})
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
		rows = append(rows, []string{"Social Media Transcription", formatSyncDuration(stats.XMedia.Duration), fmt.Sprintf("queued=%d processed=%d media_candidates=%d media_with_audio=%d media_transcribed=%d", s.ItemsQueued, s.ItemsProcessed, s.MediaCandidates, s.MediaWithAudio, s.MediaTranscribed), fmt.Sprintf("updated=%d unchanged=%d summarized=%d summary_cands=%d skipped=%d", s.ItemsUpdated, s.ItemsUnchanged, s.ItemsSummarized, s.SummaryCandidates, s.ItemsSkipped), strconv.Itoa(s.Errors + s.SummaryErrors)})
	}
	if stats.XPhotoOCR != nil {
		s := stats.XPhotoOCR.Stats
		rows = append(rows, []string{"Social Media Photo OCR", formatSyncDuration(stats.XPhotoOCR.Duration), fmt.Sprintf("queued=%d processed=%d photo_candidates=%d photos_ocred=%d", s.ItemsQueued, s.ItemsProcessed, s.PhotoCandidates, s.PhotosOCRed), fmt.Sprintf("updated=%d unchanged=%d skipped=%d hosted_attempts=%d hosted_fallbacks=%d", s.ItemsUpdated, s.ItemsUnchanged, s.ItemsSkipped, s.HostedAttempts, s.HostedFallbacks), strconv.Itoa(s.Errors)})
	}
	if stats.Links != nil {
		s := stats.Links.Stats
		rows = append(rows, []string{"Links", formatSyncDuration(stats.Links.Duration), fmt.Sprintf("items_scanned=%d links_found=%d", s.ItemsScanned, s.LinksFound), fmt.Sprintf("queued=%d extracted=%d summarized=%d rendered=%d unchanged=%d", s.SourcesQueued, s.SourcesExtracted, s.SourcesSummarized, s.SourcesRendered, s.SourcesUnchanged), strconv.Itoa(s.Errors)})
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
		rows = append(rows, []string{"Sources", formatSyncDuration(stats.Sources.Duration), fmt.Sprintf("cycles=%d work_cycles=%d queued=%d extracted=%d", s.Cycles, s.WorkCycles, s.SourcesQueued, s.SourcesExtracted), fmt.Sprintf("summarized=%d rendered=%d unchanged=%d idle_polls=%d stopped=%s", s.SourcesSummarized, s.SourcesRendered, s.SourcesUnchanged, s.IdlePolls, s.StoppedReason), strconv.Itoa(s.Errors)})
	}
	if stats.Categorize != nil {
		s := stats.Categorize.Stats
		items := stats.Categorize.ItemStats
		sources := stats.Categorize.SourceStats
		rows = append(rows, []string{"Categorize", formatSyncDuration(stats.Categorize.Duration), fmt.Sprintf("items=%d/%d sources=%d/%d", items.Applied, items.Queued, sources.Applied, sources.Queued), fmt.Sprintf("succeeded=%d skipped=%d item_applied=%d source_applied=%d", s.Succeeded, s.Skipped, items.Applied, sources.Applied), strconv.Itoa(s.Errors)})
	}
	if stats.MediaArchive != nil {
		s := stats.MediaArchive.Stats
		rows = append(rows, []string{"Media Archive", formatSyncDuration(stats.MediaArchive.Duration), fmt.Sprintf("candidates=%d uploaded=%d archived=%d", s.Candidates, s.Uploaded, s.Archived), fmt.Sprintf("unchanged=%d prune_skipped=%d pruned_files=%d pruned_rows=%d", s.Unchanged, s.PruneSkipped, s.LocalFilesPruned, s.LocalRowsPruned), strconv.Itoa(s.Errors)})
	}
	if stats.OKFExport != nil {
		s := stats.OKFExport.Stats
		rows = append(rows, []string{"OKF Export", formatSyncDuration(stats.OKFExport.Duration), fmt.Sprintf("concepts=%d indexes=%d", s.ConceptsWritten, s.IndexesWritten), fmt.Sprintf("items=%d sources=%d entities=%d topics=%d", s.ItemsWritten, s.SourcesWritten, s.EntitiesWritten, s.TopicsWritten), strconv.Itoa(len(s.Errors))})
	}
	return rows
}

func formatSyncDuration(duration time.Duration) string {
	return fmt.Sprintf("%.2fs", duration.Seconds())
}
