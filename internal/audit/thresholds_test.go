package audit

import (
	"testing"
	"time"
)

func TestAgeThresholdHalfOpenBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name            string
		age, warn, fail time.Duration
		want            Status
	}{
		{"pending below", 24*time.Hour - time.Second, 24 * time.Hour, 72 * time.Hour, StatusPass},
		{"pending warn exact", 24 * time.Hour, 24 * time.Hour, 72 * time.Hour, StatusWarn},
		{"pending fail exact", 72 * time.Hour, 24 * time.Hour, 72 * time.Hour, StatusFail},
		{"backup warn exact", 36 * time.Hour, 36 * time.Hour, 72 * time.Hour, StatusWarn},
		{"backup fail exact", 72 * time.Hour, 36 * time.Hour, 72 * time.Hour, StatusFail},
		{"okf two intervals", 2 * time.Hour, 2 * time.Hour, 4 * time.Hour, StatusWarn},
		{"okf four intervals", 4 * time.Hour, 2 * time.Hour, 4 * time.Hour, StatusFail},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyAge(tt.age, tt.warn, tt.fail); got != tt.want {
				t.Fatalf("ClassifyAge = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSchedulerThresholdsAndDurationAllowance(t *testing.T) {
	tests := []struct {
		name           string
		durations      []time.Duration
		wantAllowance  time.Duration
		wantSource     DurationAllowanceSource
		wantConfidence Confidence
	}{
		{"none", nil, 0, DurationAllowanceNone, ConfidenceModerate},
		{"one", []time.Duration{9 * time.Minute}, 9 * time.Minute, DurationAllowanceMaxObserved, ConfidenceHigh},
		{"four", []time.Duration{time.Minute, 3 * time.Minute, 2 * time.Minute, 4 * time.Minute}, 4 * time.Minute, DurationAllowanceMaxObserved, ConfidenceHigh},
		{"five nearest rank p95", []time.Duration{time.Minute, 4 * time.Minute, 3 * time.Minute, 2 * time.Minute, 5 * time.Minute}, 5 * time.Minute, DurationAllowanceP95, ConfidenceHigh},
		{"twenty nearest rank p95", durationRange(1, 20), 19 * time.Minute, DurationAllowanceP95, ConfidenceHigh},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowance, source, confidence := DurationAllowance(tt.durations)
			if allowance != tt.wantAllowance || source != tt.wantSource || confidence != tt.wantConfidence {
				t.Fatalf("DurationAllowance = %s/%s/%s, want %s/%s/%s", allowance, source, confidence, tt.wantAllowance, tt.wantSource, tt.wantConfidence)
			}
			warn, fail := SchedulerThresholds(time.Hour, 10*time.Minute, allowance)
			if warn != time.Hour+10*time.Minute+allowance+15*time.Minute || fail != warn+2*time.Hour {
				t.Fatalf("SchedulerThresholds = %s/%s", warn, fail)
			}
		})
	}
}

func durationRange(first, last int) []time.Duration {
	out := make([]time.Duration, 0, last-first+1)
	for i := first; i <= last; i++ {
		out = append(out, time.Duration(i)*time.Minute)
	}
	return out
}

func TestMetricsWindowSufficiency(t *testing.T) {
	requested := 7 * 24 * time.Hour
	if got := MetricsWindowStatus(requested, requested, 1, true, true, 6*time.Hour); got != StatusPass {
		t.Fatalf("full = %s", got)
	}
	if got := MetricsWindowStatus(requested, 12*time.Hour, 2, true, true, 6*time.Hour); got != StatusWarn {
		t.Fatalf("bounded = %s", got)
	}
	if got := MetricsWindowStatus(requested, 12*time.Hour-time.Second, 2, true, true, 6*time.Hour); got != StatusUnknown {
		t.Fatalf("short = %s", got)
	}
	if got := MetricsWindowStatus(requested, 12*time.Hour, 1, true, true, 6*time.Hour); got != StatusUnknown {
		t.Fatalf("one run = %s", got)
	}
}
