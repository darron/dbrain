package audit

import (
	"context"
	"sort"
	"time"

	"github.com/darron/dbrain/internal/metrics"
)

func executeScheduler(_ context.Context, s *runState, e RegistryEntry) Check {
	if s.metricsErr != nil {
		return unknownCheck(e, ErrorRead, s.now)
	}
	interval := s.deps.Features.SchedulerInterval
	if interval <= 0 {
		interval = time.Hour
	}
	allowance, source, confidence := DurationAllowance(s.metrics.DurationSamples)
	warn, fail := SchedulerThresholds(interval, s.deps.Features.SchedulerJitter, allowance)
	switch e.ID {
	case CheckMetricsWindow:
		covered := s.metrics.CoverageEnd.Sub(s.metrics.CoverageStart)
		status := MetricsWindowStatus(s.req.Since, covered, s.metrics.CompletedCount, s.metrics.LatestAttemptPresent, s.metrics.LatestCompletedPresent, interval)
		if s.metrics.ByteBudgetExhausted || s.metrics.EventBudgetExhausted {
			status = StatusUnknown
		}
		c := ConfidenceHigh
		if status == StatusWarn {
			c = ConfidenceModerate
		}
		if status == StatusUnknown {
			c = ConfidenceUnknown
		}
		return baseCheck(e, s.observedAt(), status, c, Evidence{"requested_seconds": seconds(s.req.Since), "covered_seconds": seconds(covered), "completed_attempt_count": s.metrics.CompletedCount, "latest_attempt_present": s.metrics.LatestAttemptPresent, "latest_completed_present": s.metrics.LatestCompletedPresent, "parse_error_count": s.metrics.ParseErrorCount})
	case CheckSchedulerLatestSync:
		run, ok := latestRun(s.metrics.Runs)
		if !ok {
			return unknownCheck(e, ErrorUnavailable, s.now)
		}
		if !run.RecordComplete || run.CompletedAt.IsZero() {
			ev := Evidence{"latest_attempt_at": run.StartedAt.UTC().Format(time.RFC3339), "age_seconds": seconds(s.now.Sub(run.StartedAt)), "warn_after_seconds": seconds(warn), "fail_after_seconds": seconds(fail), "duration_allowance_seconds": seconds(allowance), "duration_allowance_source": string(source)}
			return baseCheck(e, s.observedAt(), StatusUnknown, ConfidenceUnknown, ev)
		}
		observed := run.CompletedAt
		age := s.now.Sub(observed)
		status := ClassifyAge(age, warn, fail)
		if run.Status == "error" {
			status = StatusFail
		}
		ev := Evidence{"latest_attempt_at": run.StartedAt.UTC().Format(time.RFC3339), "age_seconds": seconds(age), "warn_after_seconds": seconds(warn), "fail_after_seconds": seconds(fail), "duration_allowance_seconds": seconds(allowance), "duration_allowance_source": string(source)}
		if run.Status == "ok" {
			ev["latest_success_at"] = observed.UTC().Format(time.RFC3339)
		}
		if s.metrics.ByteBudgetExhausted || s.metrics.EventBudgetExhausted {
			return baseCheck(e, s.observedAt(), StatusUnknown, ConfidenceUnknown, ev)
		}
		return baseCheck(e, s.observedAt(), status, confidence, ev)
	case CheckSchedulerStageCoverage:
		run, ok := latestRun(s.metrics.Runs)
		if !ok {
			return unknownCheck(e, ErrorUnavailable, s.now)
		}
		expected := s.deps.Features.SelectedStages
		if len(expected) == 0 {
			expected = run.SelectedStages
		}
		missing := 0
		for _, stage := range expected {
			if _, ok := run.CompletedStages[stage]; !ok {
				missing++
			}
		}
		status := StatusPass
		if !run.RecordComplete {
			status = StatusUnknown
		} else if missing > 0 {
			status = StatusFail
		}
		c := ConfidenceHigh
		if status == StatusUnknown {
			c = ConfidenceUnknown
		}
		return baseCheck(e, s.now, status, c, Evidence{"expected_stage_count": len(expected), "completed_stage_count": len(run.CompletedStages), "missing_stage_count": missing, "record_complete": run.RecordComplete})
	case CheckSchedulerContinuity:
		covered := s.metrics.CoverageEnd.Sub(s.metrics.CoverageStart)
		windowStatus := MetricsWindowStatus(s.req.Since, covered, s.metrics.CompletedCount, s.metrics.LatestAttemptPresent, s.metrics.LatestCompletedPresent, interval)
		if s.metrics.ByteBudgetExhausted || s.metrics.EventBudgetExhausted {
			windowStatus = StatusUnknown
		}
		base := Evidence{"observed_attempt_count": s.metrics.AttemptCount, "gap_count": 0, "explained_gap_count": 0, "unexplained_gap_count": 0, "largest_gap_seconds": 0, "warn_after_seconds": seconds(warn), "fail_after_seconds": seconds(fail)}
		if windowStatus == StatusUnknown {
			return baseCheck(e, s.now, StatusUnknown, ConfidenceUnknown, base)
		}
		runs := append([]metrics.RunRecord{}, s.metrics.Runs...)
		sort.Slice(runs, func(i, j int) bool { return runs[i].StartedAt.Before(runs[j].StartedAt) })
		gaps, explained, unexplained := 0, 0, 0
		largest := time.Duration(0)
		status := StatusPass
		for i := 1; i < len(runs); i++ {
			gap := runs[i].StartedAt.Sub(runs[i-1].StartedAt)
			if gap < warn {
				continue
			}
			gaps++
			if gap > largest {
				largest = gap
			}
			reason := false
			for _, m := range s.metrics.Markers {
				if m.ExplainsContinuity && m.At.After(runs[i-1].StartedAt) && m.At.Before(runs[i].StartedAt) {
					reason = true
					break
				}
			}
			if reason {
				explained++
				if status == StatusPass {
					status = StatusWarn
				}
			} else {
				unexplained++
				if gap >= fail {
					status = StatusFail
				} else if status == StatusPass {
					status = StatusWarn
				}
			}
		}
		base["gap_count"] = gaps
		base["explained_gap_count"] = explained
		base["unexplained_gap_count"] = unexplained
		base["largest_gap_seconds"] = seconds(largest)
		c := ConfidenceHigh
		if windowStatus == StatusWarn {
			c = ConfidenceModerate
		}
		return baseCheck(e, s.now, status, c, base)
	}
	return unknownCheck(e, ErrorUnavailable, s.now)
}
