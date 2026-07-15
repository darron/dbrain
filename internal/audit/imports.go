package audit

import (
	"context"
	"strings"
	"time"
)

func executeImport(_ context.Context, s *runState, e RegistryEntry) Check {
	if s.metricsErr != nil {
		return unknownCheck(e, ErrorRead, s.now)
	}
	record, ok := s.metrics.Imports[metricsSource(e.Source)]
	if strings.HasSuffix(string(e.ID), ".arrivals") {
		daily := make([]map[string]any, 0, len(record.Daily))
		for _, row := range record.Daily {
			daily = append(daily, map[string]any{"day": row.Day, "created": row.Created, "updated": row.Updated, "unchanged": row.Unchanged, "skipped": row.Skipped, "linked": row.Linked, "blocked": row.Blocked, "failed": row.Failed})
		}
		quiet := s.req.Since
		if ok && !record.LastArrivalAt.IsZero() {
			quiet = s.now.Sub(record.LastArrivalAt)
		}
		return baseCheck(e, s.now, StatusPass, ConfidenceHigh, Evidence{"quiet_seconds": seconds(quiet), "daily": daily})
	}
	if !ok {
		return unknownCheck(e, ErrorUnavailable, s.now)
	}
	interval := s.deps.Features.SchedulerInterval
	if interval <= 0 {
		interval = time.Hour
	}
	allowance, _, confidence := DurationAllowance(s.metrics.DurationSamples)
	warn, fail := SchedulerThresholds(interval, s.deps.Features.SchedulerJitter, allowance)
	reference := record.SucceededAt
	if reference.IsZero() {
		reference = record.AttemptedAt
	}
	age := s.now.Sub(reference)
	status := ClassifyAge(age, warn, fail)
	if record.FailureCount > 0 && record.SucceededAt.Before(record.AttemptedAt) {
		status = StatusFail
	}
	ev := Evidence{"age_seconds": seconds(age), "warn_after_seconds": seconds(warn), "fail_after_seconds": seconds(fail), "attempt_count": record.AttemptCount, "success_count": record.SuccessCount, "failure_count": record.FailureCount}
	if !record.AttemptedAt.IsZero() {
		ev["attempted_at"] = record.AttemptedAt.UTC().Format(time.RFC3339)
	}
	if !record.SucceededAt.IsZero() {
		ev["succeeded_at"] = record.SucceededAt.UTC().Format(time.RFC3339)
	}
	return baseCheck(e, s.now, status, confidence, ev)
}
func metricsSource(source Source) string {
	switch source {
	case SourceAppleNotes:
		return "apple_notes"
	case SourceSafariTabs:
		return "safari_tabs"
	case SourceXBookmarks:
		return "x_bookmarks"
	case SourceGitHubStars:
		return "github_stars"
	case SourceYouTubeLiked:
		return "youtube_liked"
	case SourceYouTubeWatchLater:
		return "youtube_watch_later"
	case SourceFeeds:
		return "feeds"
	}
	return ""
}
