package audit

import (
	"sort"
	"time"
)

const (
	DefaultPendingWarn = 24 * time.Hour
	DefaultPendingFail = 72 * time.Hour
	DefaultBackupWarn  = 36 * time.Hour
	DefaultBackupFail  = 72 * time.Hour
)

type DurationAllowanceSource string

const (
	DurationAllowanceP95         DurationAllowanceSource = "p95"
	DurationAllowanceMaxObserved DurationAllowanceSource = "max_observed"
	DurationAllowanceNone        DurationAllowanceSource = "none"
)

func ClassifyAge(age, warn, fail time.Duration) Status {
	if age >= fail {
		return StatusFail
	}
	if age >= warn {
		return StatusWarn
	}
	return StatusPass
}

func DurationAllowance(durations []time.Duration) (time.Duration, DurationAllowanceSource, Confidence) {
	if len(durations) == 0 {
		return 0, DurationAllowanceNone, ConfidenceModerate
	}
	values := append([]time.Duration(nil), durations...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	if len(values) < 5 {
		return values[len(values)-1], DurationAllowanceMaxObserved, ConfidenceHigh
	}
	index := (95*len(values)+99)/100 - 1
	return values[index], DurationAllowanceP95, ConfidenceHigh
}

func SchedulerThresholds(interval, jitter, allowance time.Duration) (time.Duration, time.Duration) {
	warn := interval + jitter + allowance + 15*time.Minute
	return warn, warn + 2*interval
}

func MetricsWindowStatus(requested, covered time.Duration, completedAttempts int, latestAttempt, latestCompleted bool, interval time.Duration) Status {
	if requested > 0 && covered >= requested {
		return StatusPass
	}
	if latestAttempt && latestCompleted && completedAttempts >= 2 && interval > 0 && covered >= 2*interval {
		return StatusWarn
	}
	return StatusUnknown
}
