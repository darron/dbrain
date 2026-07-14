package audit

import "time"

type FreshnessStatus string
type FreshnessReason string

const (
	FreshnessCurrent FreshnessStatus = "current"
	FreshnessUnknown FreshnessStatus = "unknown"

	FreshnessNotFound FreshnessReason = "not_found"
	FreshnessStale    FreshnessReason = "stale"
)

type Freshness struct {
	Status          FreshnessStatus `json:"status"`
	Reason          FreshnessReason `json:"reason,omitempty"`
	AgeSeconds      *int64          `json:"age_seconds,omitempty"`
	DeadlineSeconds int64           `json:"deadline_seconds"`
}

type PresentedReport struct {
	Report    *Report   `json:"report"`
	Freshness Freshness `json:"freshness"`
}

func FreshnessDeadline(profile Profile, syncInterval, standardInterval time.Duration) time.Duration {
	switch profile {
	case ProfileFast:
		if syncInterval <= 0 {
			syncInterval = time.Hour
		}
		return 2 * syncInterval
	case ProfileStandard:
		if standardInterval <= 0 {
			standardInterval = 6 * time.Hour
		}
		deadline := 2 * standardInterval
		if deadline < 12*time.Hour {
			deadline = 12 * time.Hour
		}
		return deadline
	default:
		return 0
	}
}

// PresentReport adds non-persisted freshness metadata to an immutable exact-
// profile report. It never rewrites the report's health status.
func PresentReport(report *Report, profile Profile, syncInterval, standardInterval time.Duration, now time.Time) PresentedReport {
	deadline := FreshnessDeadline(profile, syncInterval, standardInterval)
	base := Freshness{Status: FreshnessUnknown, Reason: FreshnessNotFound, DeadlineSeconds: int64(deadline / time.Second)}
	if report == nil || report.Profile != profile || deadline <= 0 {
		return PresentedReport{Report: nil, Freshness: base}
	}
	age := now.UTC().Sub(report.CompletedAt.UTC())
	if age < 0 {
		age = 0
	}
	seconds := int64(age / time.Second)
	base.AgeSeconds = &seconds
	base.Reason = ""
	base.Status = FreshnessCurrent
	if age > deadline {
		base.Status = FreshnessUnknown
		base.Reason = FreshnessStale
	}
	return PresentedReport{Report: report, Freshness: base}
}
