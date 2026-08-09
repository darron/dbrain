package syncjob

import (
	"encoding/json"
	"time"

	"github.com/darron/dbrain/internal/itemcategorize"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/sourceenrich"
)

func newSyncRunID(startedAt time.Time) string {
	return metrics.NewRunID("sync", startedAt)
}

func emitSyncRunStarted(run metrics.RunContext, startedAt time.Time, opts Options) {
	if !run.Enabled() {
		return
	}
	_ = run.Emit(metrics.Event{
		"event":           "sync.run.started",
		"status":          "started",
		"started_at":      startedAt.UTC().Format(time.RFC3339Nano),
		"selected_stages": selectedMetricStages(opts),
		"config": map[string]any{
			"summary_model":               opts.Model,
			"categorize_model":            opts.CategorizeModel,
			"categorize_concurrency":      opts.CategorizeConcurrency,
			"categorize_images":           opts.CategorizeImages,
			"categorize_timeout_ms":       metrics.DurationMillis(opts.CategorizeTimeout),
			"source_concurrency":          opts.SourceConcurrency,
			"source_limit":                opts.SourceLimit,
			"source_timeout_ms":           metrics.DurationMillis(opts.Timeout),
			"x_concurrency":               opts.XConcurrency,
			"x_timeout_ms":                metrics.DurationMillis(opts.XTimeout),
			"x_media_download_timeout_ms": metrics.DurationMillis(opts.XMediaTimeout),
		},
	})
}

func emitSyncCompletion(run metrics.RunContext, stats Stats, runErr error, parentOwnsCompletion bool) {
	if parentOwnsCompletion {
		emitSyncCompletedEvent(run, "sync.pipeline.completed", stats, runErr)
		return
	}
	EmitRunCompleted(run, stats, runErr)
}

// EmitRunCompleted records the terminal boundary for the full sync execution.
// Callers that set Options.ParentOwnsRunCompletion must invoke this after their
// attached work settles.
func EmitRunCompleted(run metrics.RunContext, stats Stats, runErr error) {
	emitSyncCompletedEvent(run, "sync.run.completed", stats, runErr)
}

func emitSyncCompletedEvent(run metrics.RunContext, eventName string, stats Stats, runErr error) {
	if !run.Enabled() {
		return
	}
	status := "ok"
	event := metrics.Event{
		"event":        eventName,
		"status":       status,
		"started_at":   stats.StartedAt.UTC().Format(time.RFC3339Nano),
		"completed_at": stats.CompletedAt.UTC().Format(time.RFC3339Nano),
		"duration_ms":  metrics.DurationMillis(stats.Duration),
		"counts": map[string]any{
			"stages_completed": completedStageCount(stats),
			"stages_error":     erroredStageCount(runErr),
		},
	}
	if runErr != nil {
		event["status"] = "error"
		event["error"] = metrics.ErrorObject(runErr)
	}
	_ = run.Emit(event)
}

func emitSyncStageMetrics(run metrics.RunContext, opts stageOptions, stats *Stats, stageID syncStageID, stageErr error) {
	if !run.Enabled() || stats == nil {
		return
	}
	switch stageID {
	case syncStageAppleNotes:
		emitStage(run, "apple_notes", stats.AppleNotes, stageErr, nil)
	case syncStageSafariTabs:
		emitStage(run, "safari_tabs", stats.SafariTabs, stageErr, nil)
	case syncStageBlueskyBookmarks:
		emitStage(run, "bluesky_bookmarks", stats.BlueskyBookmarks, stageErr, map[string]any{
			"limit":      opts.BlueskyBookmarks.Limit,
			"timeout_ms": metrics.DurationMillis(opts.BlueskyBookmarks.Timeout),
		})
	case syncStageXFrontier:
		emitStage(run, "x_bookmarks", stats.XBookmarks, stageErr, nil)
		emitStage(run, "x", stats.X, stageErr, nil)
		emitStage(run, "links", stats.Links, stageErr, map[string]any{
			"discover_limit": opts.Links.DiscoverLimit,
			"limit":          opts.Links.Limit,
			"concurrency":    opts.Links.Concurrency,
		})
	case syncStageXMedia:
		emitStage(run, "x_media", stats.XMedia, stageErr, map[string]any{
			"summary_model": opts.Common.Model,
			"timeout_ms":    metrics.DurationMillis(opts.Common.Timeout),
		})
	case syncStageXPhotoOCR:
		emitStage(run, "x_photo_ocr", stats.XPhotoOCR, stageErr, map[string]any{
			"ocr_model":  opts.XPhotoOCR.Model,
			"timeout_ms": metrics.DurationMillis(opts.Common.Timeout),
		})
	case syncStageGitHub:
		emitStage(run, "github", stats.GitHub, stageErr, nil)
	case syncStageYouTube:
		emitStage(run, "youtube", stats.YouTube, stageErr, nil)
	case syncStageFeeds:
		emitStage(run, "feeds", stats.Feeds, stageErr, nil)
	case syncStageSources:
		emitStage(run, "sources", stats.Sources, stageErr, map[string]any{
			"summary_model": opts.Common.Model,
			"concurrency":   opts.Sources.Concurrency,
			"limit":         opts.Sources.Limit,
			"timeout_ms":    metrics.DurationMillis(opts.Common.Timeout),
		})
	case syncStageCategorize:
		emitCategorizeStage(run, stats.Categorize, opts.Categorize, stageErr)
	case syncStageMediaArchive:
		emitStage(run, "media_archive", stats.MediaArchive, stageErr, nil)
	case syncStageOKFExport:
		emitStage(run, "okf_export", stats.OKFExport, stageErr, nil)
	}
	emitSyncImportStageMetric(run, opts, stageID, *stats, stageErr)
}

func selectedMetricStages(opts Options) []string {
	out := make([]string, 0, 14)
	if opts.AppleNotesEnabled {
		out = append(out, "apple_notes")
	}
	if opts.SafariTabsEnabled {
		out = append(out, "safari_tabs")
	}
	if opts.BlueskyBookmarksEnabled {
		out = append(out, "bluesky_bookmarks")
	}
	if opts.XBookmarksEnabled {
		out = append(out, "x_bookmarks")
	}
	if opts.XEnabled {
		out = append(out, "x")
	}
	if opts.LinksEnabled {
		out = append(out, "links")
	}
	if opts.XMediaEnabled {
		out = append(out, "x_media")
	}
	if opts.XPhotoOCREnabled {
		out = append(out, "x_photo_ocr")
	}
	if opts.GitHubEnabled {
		out = append(out, "github")
	}
	if opts.YouTubeEnabled {
		out = append(out, "youtube")
	}
	if opts.FeedsEnabled {
		out = append(out, "feeds")
	}
	if opts.SourcesEnabled {
		out = append(out, "sources")
	}
	if opts.CategorizeEnabled {
		out = append(out, "categorize")
	}
	if opts.ArchiveMediaEnabled {
		out = append(out, "media_archive")
	}
	if opts.OKFExportEnabled {
		out = append(out, "okf_export")
	}
	return out
}

func emitSyncImportMetrics(run metrics.RunContext, stats Stats, stageErrors map[syncStageID]error) {
	opts := newStageOptions(Options{
		AppleNotesEnabled: stats.AppleNotes != nil, SafariTabsEnabled: stats.SafariTabs != nil,
		BlueskyBookmarksEnabled: stats.BlueskyBookmarks != nil,
		XBookmarksEnabled:       stats.XBookmarks != nil, GitHubEnabled: stats.GitHub != nil,
		YouTubeEnabled: stats.YouTube != nil, WatchLater: stats.YouTube != nil, Liked: stats.YouTube != nil,
		FeedsEnabled: stats.Feeds != nil,
	})
	for _, stage := range []syncStageID{syncStageAppleNotes, syncStageSafariTabs, syncStageBlueskyBookmarks, syncStageXFrontier, syncStageGitHub, syncStageYouTube, syncStageFeeds} {
		emitSyncImportStageMetric(run, opts, stage, stats, stageErrors[stage])
	}
}

func emitSyncImportStageMetric(run metrics.RunContext, opts stageOptions, stage syncStageID, stats Stats, stageErr error) {
	if !run.Enabled() {
		return
	}
	emit := func(source string, duration time.Duration, counts map[string]int, sourceFailed bool) {
		completedAt := time.Now().UTC()
		status := "ok"
		if stageErr != nil || sourceFailed {
			status = "error"
			if counts["failed"] == 0 {
				counts["failed"] = 1
			}
		}
		_ = run.Emit(metrics.Event{
			"event": "sync.import.completed", "source": source, "status": status,
			"attempted_at": completedAt.Add(-duration).Format(time.RFC3339Nano),
			"completed_at": completedAt.Format(time.RFC3339Nano), "counts": counts,
		})
	}
	switch stage {
	case syncStageAppleNotes:
		if value := stats.AppleNotes; value != nil {
			s := value.Stats
			emit("apple_notes", value.Duration, importCounts(s.NotesCreated, s.NotesUpdated, s.NotesUnchanged, s.NotesSkipped, s.LinksDiscovered, s.NotesBlocked, s.Errors), s.Errors > 0)
		} else if opts.AppleNotes.Enabled {
			emit("apple_notes", 0, importCounts(0, 0, 0, 0, 0, 0, 0), true)
		}
	case syncStageSafariTabs:
		if value := stats.SafariTabs; value != nil {
			s := value.Stats
			emit("safari_tabs", value.Duration, importCounts(s.TabsCreated, s.TabsUpdated, s.TabsUnchanged, s.TabsSkipped, s.LinksFound, 0, s.Errors), s.Errors > 0)
		} else if opts.SafariTabs.Enabled {
			emit("safari_tabs", 0, importCounts(0, 0, 0, 0, 0, 0, 0), true)
		}
	case syncStageBlueskyBookmarks:
		if value := stats.BlueskyBookmarks; value != nil {
			s := value.Stats
			counts := importCounts(s.Created, s.Updated, s.Unchanged, s.Skipped, 0, s.SkippedBlocked, 0)
			counts["media_discovered"] = s.MediaDiscovered
			counts["media_linked"] = s.MediaLinked
			counts["media_unavailable"] = s.MediaUnavailable
			counts["media_downloaded"] = s.MediaDownloaded
			counts["media_gone"] = s.MediaGone
			// The compact import event intentionally avoids the reserved
			// "error" field name; full stage metrics retain media_errors.
			counts["media_failures"] = s.MediaErrors
			counts["media_blocked"] = s.MediaBlocked
			counts["quote_linked"] = s.QuoteLinked
			counts["quote_skipped"] = s.QuoteSkipped
			emit("bluesky_bookmarks", value.Duration, counts, s.MediaErrors > 0)
		} else if opts.BlueskyBookmarks.Enabled {
			emit("bluesky_bookmarks", 0, importCounts(0, 0, 0, 0, 0, 0, 0), true)
		}
	case syncStageXFrontier:
		if value := stats.XBookmarks; value != nil {
			s := value.Stats
			emit("x_bookmarks", value.Duration, importCounts(s.Created, s.Updated, s.Unchanged, 0, 0, 0, 0), false)
		} else if opts.XBookmarks.Enabled {
			emit("x_bookmarks", 0, importCounts(0, 0, 0, 0, 0, 0, 0), true)
		}
	case syncStageGitHub:
		if value := stats.GitHub; value != nil {
			s := value.Stats
			emit("github_stars", value.Duration, importCounts(s.ItemsCreated, s.ItemsUpdated, s.ItemsUnchanged, 0, s.LinksCreated, 0, s.Errors), s.Errors > 0)
		} else if opts.GitHub.Enabled {
			emit("github_stars", 0, importCounts(0, 0, 0, 0, 0, 0, 0), true)
		}
	case syncStageYouTube:
		if value := stats.YouTube; value != nil {
			if opts.YouTube.WatchLater {
				s := value.Stats.WatchLater
				emit("youtube_watch_later", value.Duration, importCounts(s.ItemsCreated, s.ItemsUpdated, s.ItemsUnchanged, s.ItemsSkipped, s.LinksCreated, 0, s.Errors), s.Errors > 0)
			}
			if opts.YouTube.Liked {
				s := value.Stats.Liked
				emit("youtube_liked", value.Duration, importCounts(s.ItemsCreated, s.ItemsUpdated, s.ItemsUnchanged, s.ItemsSkipped, s.LinksCreated, 0, s.Errors), s.Errors > 0)
			}
		} else {
			if opts.YouTube.WatchLater {
				emit("youtube_watch_later", 0, importCounts(0, 0, 0, 0, 0, 0, 0), true)
			}
			if opts.YouTube.Liked {
				emit("youtube_liked", 0, importCounts(0, 0, 0, 0, 0, 0, 0), true)
			}
		}
	case syncStageFeeds:
		if value := stats.Feeds; value != nil {
			s := value.Stats
			emit("feeds", value.Duration, importCounts(s.ItemsCreated, s.ItemsUpdated, s.ItemsUnchanged, 0, s.SourcesLinked, 0, s.Errors), s.Errors > 0)
		} else if opts.Feeds.Enabled {
			emit("feeds", 0, importCounts(0, 0, 0, 0, 0, 0, 0), true)
		}
	}
}

func importCounts(created, updated, unchanged, skipped, linked, blocked, failed int) map[string]int {
	return map[string]int{"created": created, "updated": updated, "unchanged": unchanged, "skipped": skipped, "linked": linked, "blocked": blocked, "failed": failed}
}

func emitStage(run metrics.RunContext, stage string, value any, stageErr error, config map[string]any) {
	duration, stats, ok := stageMetrics(value)
	if !ok {
		return
	}
	completedAt := time.Now().UTC()
	status := "ok"
	event := metrics.Event{
		"event":        "sync.stage.completed",
		"stage":        stage,
		"status":       status,
		"started_at":   completedAt.Add(-duration).Format(time.RFC3339Nano),
		"completed_at": completedAt.Format(time.RFC3339Nano),
		"duration_ms":  metrics.DurationMillis(duration),
		"counts":       countsFromStruct(stats),
	}
	if len(config) > 0 {
		event["config"] = config
	}
	if stageErr != nil {
		event["status"] = "error"
		event["error"] = metrics.ErrorObject(stageErr)
	}
	_ = run.Emit(event)
}

func emitCategorizeStage(run metrics.RunContext, stage *CategorizeStage, opts categorizeStageOptions, stageErr error) {
	if stage == nil {
		return
	}
	completedAt := time.Now().UTC()
	event := metrics.Event{
		"event":        "sync.stage.completed",
		"stage":        "categorize",
		"status":       "ok",
		"started_at":   completedAt.Add(-stage.Duration).Format(time.RFC3339Nano),
		"completed_at": completedAt.Format(time.RFC3339Nano),
		"duration_ms":  metrics.DurationMillis(stage.Duration),
		"counts": map[string]any{
			"item_queued":    stage.ItemStats.Queued,
			"item_applied":   stage.ItemStats.Applied,
			"source_queued":  stage.SourceStats.Queued,
			"source_applied": stage.SourceStats.Applied,
			"succeeded":      stage.Stats.Succeeded,
			"skipped":        stage.Stats.Skipped,
			"errors":         stage.Stats.Errors,
		},
		"config": map[string]any{
			"categorize_model": opts.Model,
			"concurrency":      opts.Concurrency,
			"limit":            opts.Limit,
			"include_images":   opts.Images,
			"timeout_ms":       metrics.DurationMillis(opts.Timeout),
		},
	}
	if stageErr != nil {
		event["status"] = "error"
		event["error"] = metrics.ErrorObject(stageErr)
	}
	_ = run.Emit(event)
}

func emitCategorizeItemMetric(run metrics.RunContext, result itemcategorize.ItemResult, opts categorizeStageOptions) {
	if !detailIncludesItem(run) {
		return
	}
	event := categorizationDetailEvent("categorize.item.completed", "item", result.Item.SourceKey, result.Duration, result.Result, result.Error, opts, run.IncludeSubjectKeys())
	_ = run.Emit(event)
}

func emitCategorizeSourceMetric(run metrics.RunContext, result itemcategorize.SourceResult, opts categorizeStageOptions) {
	if !detailIncludesItem(run) {
		return
	}
	event := categorizationDetailEvent("categorize.source.completed", "source", result.Source.SourceKey, result.Duration, result.Result, result.Error, opts, run.IncludeSubjectKeys())
	_ = run.Emit(event)
}

func emitSourceSummaryMetric(run metrics.RunContext, result sourceenrich.SourceResult, common commonStageOptions, opts sourcesStageOptions) {
	if !detailIncludesItem(run) {
		return
	}
	if !result.SummaryCreated {
		return
	}
	status := summaryMetricStatus(result)
	event := metrics.Event{
		"event":        "summary.source.completed",
		"status":       status,
		"duration_ms":  metrics.DurationMillis(result.Duration),
		"model":        firstMetricString(result.SummaryModel, common.Model),
		"provider":     result.SummaryProvider,
		"api_model":    result.SummaryAPIModel,
		"transport":    result.SummaryTransport,
		"tool":         result.SummaryTool,
		"tool_version": result.SummaryToolVersion,
		"output": map[string]any{
			"summary_chars": result.SummaryChars,
		},
		"config": map[string]any{
			"length":      common.Length,
			"limit":       opts.Limit,
			"concurrency": opts.Concurrency,
			"timeout_ms":  metrics.DurationMillis(common.Timeout),
		},
	}
	if result.Error != "" {
		event["error"] = metrics.ErrorObject(result.Error)
	}
	_ = run.Emit(event.WithSubject("source", result.SourceKey, run.IncludeSubjectKeys()))
}

func summaryMetricStatus(result sourceenrich.SourceResult) string {
	switch result.SummaryStatus {
	case "", model.SourceSummaryStatusOK:
		return "ok"
	case model.SourceSummaryStatusError:
		return "error"
	default:
		return result.SummaryStatus
	}
}

func detailIncludesItem(run metrics.RunContext) bool {
	if !run.Enabled() {
		return false
	}
	return run.Detail() == metrics.DetailItem || run.Detail() == metrics.DetailModelCall
}

func categorizationDetailEvent(eventName string, subjectKind string, subjectKey string, duration time.Duration, result itemcategorize.Result, errorText string, opts categorizeStageOptions, includeSubjectKey bool) metrics.Event {
	status := "ok"
	event := metrics.Event{
		"event":        eventName,
		"status":       status,
		"duration_ms":  metrics.DurationMillis(duration),
		"model":        firstMetricString(result.Model, opts.Model),
		"provider":     result.Provider,
		"api_model":    result.APIModel,
		"transport":    result.Transport,
		"tool":         result.Tool,
		"tool_version": result.ToolVersion,
		"output": map[string]any{
			"categories": len(result.Categories),
			"tags":       len(result.Tags),
		},
		"config": map[string]any{
			"include_images": opts.Images,
			"timeout_ms":     metrics.DurationMillis(opts.Timeout),
		},
	}
	if errorText != "" {
		event["status"] = "error"
		event["error"] = metrics.ErrorObject(errorText)
	}
	return event.WithSubject(subjectKind, subjectKey, includeSubjectKey)
}

func firstMetricString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func stageMetrics(value any) (time.Duration, any, bool) {
	switch stage := value.(type) {
	case *AppleNotesStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	case *SafariTabsStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	case *BlueskyBookmarksStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	case *XBookmarksStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	case *XStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	case *XMediaStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	case *XPhotoOCRStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	case *LinksStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	case *GitHubStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	case *YouTubeStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	case *FeedsStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	case *SourcesStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	case *MediaArchiveStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	case *OKFExportStage:
		if stage == nil {
			return 0, nil, false
		}
		return stage.Duration, stage.Stats, true
	default:
		return 0, nil, false
	}
}

func countsFromStruct(stats any) map[string]any {
	data, err := json.Marshal(stats)
	if err != nil {
		return map[string]any{}
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[string]any{}
	}
	counts := make(map[string]any, len(raw))
	for key, value := range raw {
		switch value.(type) {
		case float64, bool:
			counts[key] = value
		}
	}
	return counts
}

func completedStageCount(stats Stats) int {
	count := 0
	for _, present := range []bool{
		stats.AppleNotes != nil,
		stats.SafariTabs != nil,
		stats.BlueskyBookmarks != nil,
		stats.XBookmarks != nil,
		stats.X != nil,
		stats.Links != nil,
		stats.XMedia != nil,
		stats.XPhotoOCR != nil,
		stats.GitHub != nil,
		stats.YouTube != nil,
		stats.Feeds != nil,
		stats.Sources != nil,
		stats.Categorize != nil,
		stats.MediaArchive != nil,
		stats.OKFExport != nil,
	} {
		if present {
			count++
		}
	}
	return count
}

func erroredStageCount(runErr error) int {
	if runErr != nil {
		return 1
	}
	return 0
}
