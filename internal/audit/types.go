package audit

import (
	"fmt"
	"time"
)

const SchemaV1 = "dbrain.audit.v1"

type Profile string

const (
	ProfileFast     Profile = "fast"
	ProfileStandard Profile = "standard"
	ProfileDeep     Profile = "deep"
)

func (p Profile) Valid() bool { return p == ProfileFast || p == ProfileStandard || p == ProfileDeep }

type Category string

const (
	CategoryBoundary   Category = "boundary"
	CategoryScheduler  Category = "scheduler"
	CategoryImports    Category = "imports"
	CategoryPipeline   Category = "pipeline"
	CategoryDurability Category = "durability"
)

func (c Category) Valid() bool {
	switch c {
	case CategoryBoundary, CategoryScheduler, CategoryImports, CategoryPipeline, CategoryDurability:
		return true
	default:
		return false
	}
}

type Source string

const (
	SourceAppleNotes        Source = "apple-notes"
	SourceFeeds             Source = "feeds"
	SourceGitHubStars       Source = "github-stars"
	SourceSafariTabs        Source = "safari-tabs"
	SourceXBookmarks        Source = "x-bookmarks"
	SourceYouTubeLiked      Source = "youtube-liked"
	SourceYouTubeWatchLater Source = "youtube-watch-later"
)

func (s Source) Valid() bool {
	switch s {
	case SourceAppleNotes, SourceFeeds, SourceGitHubStars, SourceSafariTabs, SourceXBookmarks, SourceYouTubeLiked, SourceYouTubeWatchLater:
		return true
	default:
		return false
	}
}

type CheckID string
type Status string

const (
	StatusPass    Status = "pass"
	StatusWarn    Status = "warn"
	StatusFail    Status = "fail"
	StatusUnknown Status = "unknown"
	StatusSkipped Status = "skipped"
)

func (s Status) Valid() bool {
	return s == StatusPass || s == StatusWarn || s == StatusFail || s == StatusUnknown || s == StatusSkipped
}

type Confidence string

const (
	ConfidenceHigh     Confidence = "high"
	ConfidenceModerate Confidence = "moderate"
	ConfidenceLow      Confidence = "low"
	ConfidenceUnknown  Confidence = "unknown"
)

func (c Confidence) Valid() bool {
	return c == ConfidenceHigh || c == ConfidenceModerate || c == ConfidenceLow || c == ConfidenceUnknown
}

type SkipReason string

const (
	SkipProfileExcluded SkipReason = "profile_excluded"
	SkipFeatureDisabled SkipReason = "feature_disabled"
)

func (s SkipReason) Valid() bool { return s == SkipProfileExcluded || s == SkipFeatureDisabled }

type Scope struct {
	Categories  []Category `json:"categories"`
	Sources     []Source   `json:"sources"`
	CheckIDs    []CheckID  `json:"check_ids"`
	Filtered    bool       `json:"filtered"`
	WholeSystem bool       `json:"whole_system"`
}

type Boundary struct {
	Layout                string `json:"layout"`
	ConfigVerified        bool   `json:"config_verified"`
	DatabaseVerified      bool   `json:"database_verified"`
	Version               string `json:"version"`
	Commit                string `json:"commit"`
	GitStatus             string `json:"git_status"`
	Platform              string `json:"platform"`
	SecurityBaseline      string `json:"security_baseline"`
	SecurityBaselineEpoch int    `json:"security_baseline_epoch"`
	SchemaVersion         int    `json:"schema_version"`
	SchemaCompatibility   string `json:"schema_compatibility"`
}

type StatusCounts struct {
	Pass    int `json:"pass"`
	Warn    int `json:"warn"`
	Fail    int `json:"fail"`
	Unknown int `json:"unknown"`
	Skipped int `json:"skipped"`
}

type RequiredStatusCounts struct {
	Pass    int `json:"pass"`
	Warn    int `json:"warn"`
	Fail    int `json:"fail"`
	Unknown int `json:"unknown"`
}

type Summary struct {
	All      StatusCounts         `json:"all"`
	Required RequiredStatusCounts `json:"required"`
}

type Threshold struct {
	WarnAfterSeconds int64 `json:"warn_after_seconds,omitempty"`
	FailAfterSeconds int64 `json:"fail_after_seconds,omitempty"`
}

type Evidence map[string]any

type Check struct {
	ID          CheckID    `json:"id"`
	Category    Category   `json:"category"`
	Status      Status     `json:"status"`
	Confidence  Confidence `json:"confidence"`
	Required    bool       `json:"required"`
	Summary     string     `json:"summary"`
	ObservedAt  time.Time  `json:"observed_at"`
	Threshold   *Threshold `json:"threshold,omitempty"`
	Evidence    Evidence   `json:"evidence"`
	Remediation string     `json:"remediation,omitempty"`
	SkipReason  SkipReason `json:"skip_reason,omitempty"`
	ErrorCode   ErrorCode  `json:"error_code,omitempty"`
}

type Report struct {
	Schema      string     `json:"schema"`
	AuditID     string     `json:"audit_id"`
	Profile     Profile    `json:"profile"`
	Scope       Scope      `json:"scope"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt time.Time  `json:"completed_at"`
	Status      Status     `json:"status"`
	Confidence  Confidence `json:"confidence"`
	Boundary    Boundary   `json:"boundary"`
	Summary     Summary    `json:"summary"`
	Checks      []Check    `json:"checks"`
}

type Request struct {
	Profile      Profile
	Since        time.Duration
	Categories   []Category
	Sources      []Source
	CheckIDs     []CheckID
	ExpectCommit string
}

func NewReport(profile Profile, started time.Time) Report {
	return Report{
		Schema:     SchemaV1,
		Profile:    profile,
		Scope:      Scope{Categories: []Category{}, Sources: []Source{}, CheckIDs: []CheckID{}, WholeSystem: true},
		StartedAt:  started.UTC(),
		Status:     StatusUnknown,
		Confidence: ConfidenceUnknown,
		Boundary:   Boundary{GitStatus: "unknown"},
		Checks:     []Check{},
	}
}

func FinalizeReport(report *Report) {
	if report == nil {
		return
	}
	report.Summary = summarize(report.Checks)
	report.Status, report.Confidence = Overall(report.Checks)
}

func summarize(checks []Check) Summary {
	var out Summary
	for _, check := range checks {
		switch check.Status {
		case StatusPass:
			out.All.Pass++
		case StatusWarn:
			out.All.Warn++
		case StatusFail:
			out.All.Fail++
		case StatusUnknown:
			out.All.Unknown++
		case StatusSkipped:
			out.All.Skipped++
		}
		if !check.Required || check.Status == StatusSkipped {
			continue
		}
		switch check.Status {
		case StatusPass:
			out.Required.Pass++
		case StatusWarn:
			out.Required.Warn++
		case StatusFail:
			out.Required.Fail++
		case StatusUnknown:
			out.Required.Unknown++
		}
	}
	return out
}

func ValidateReport(report Report) error {
	if report.Schema != SchemaV1 {
		return fmt.Errorf("unsupported audit schema %q", report.Schema)
	}
	if !report.Profile.Valid() {
		return fmt.Errorf("invalid profile %q", report.Profile)
	}
	if report.Scope.Categories == nil || report.Scope.Sources == nil || report.Scope.CheckIDs == nil || report.Checks == nil {
		return fmt.Errorf("audit arrays must not be null")
	}
	if report.Scope.Filtered == report.Scope.WholeSystem {
		return fmt.Errorf("scope filtered and whole_system are inconsistent")
	}
	for _, category := range report.Scope.Categories {
		if !category.Valid() {
			return fmt.Errorf("invalid category %q", category)
		}
	}
	for _, source := range report.Scope.Sources {
		if !source.Valid() {
			return fmt.Errorf("invalid source %q", source)
		}
	}
	for _, id := range report.Scope.CheckIDs {
		if _, ok := Lookup(id); !ok {
			return fmt.Errorf("invalid check id %q", id)
		}
	}
	if !report.Status.Valid() || report.Status == StatusSkipped {
		return fmt.Errorf("invalid overall status %q", report.Status)
	}
	if !report.Confidence.Valid() {
		return fmt.Errorf("invalid overall confidence %q", report.Confidence)
	}
	lastIndex := -1
	for i, check := range report.Checks {
		entry, ok := Lookup(check.ID)
		if !ok {
			return fmt.Errorf("checks[%d]: unknown id %q", i, check.ID)
		}
		if entry.Category != check.Category {
			return fmt.Errorf("checks[%d]: category %q does not match registry", i, check.Category)
		}
		if entry.index <= lastIndex {
			return fmt.Errorf("checks are not in registry order")
		}
		lastIndex = entry.index
		if !check.Status.Valid() || !check.Confidence.Valid() {
			return fmt.Errorf("checks[%d]: invalid status or confidence", i)
		}
		if check.Evidence == nil {
			return fmt.Errorf("checks[%d]: evidence must not be null", i)
		}
		if check.Status == StatusSkipped {
			if check.Required || !check.SkipReason.Valid() {
				return fmt.Errorf("checks[%d]: invalid skipped check", i)
			}
		} else if check.SkipReason != "" {
			return fmt.Errorf("checks[%d]: non-skipped check has skip reason", i)
		}
		if check.ErrorCode != "" && !check.ErrorCode.Valid() {
			return fmt.Errorf("checks[%d]: invalid error code", i)
		}
		if err := ValidateEvidence(check.ID, check.Evidence); err != nil {
			return fmt.Errorf("checks[%d]: %w", i, err)
		}
	}
	wantStatus, wantConfidence := Overall(report.Checks)
	if report.Status != wantStatus || report.Confidence != wantConfidence {
		return fmt.Errorf("overall status/confidence does not match checks")
	}
	if report.Summary != summarize(report.Checks) {
		return fmt.Errorf("summary does not match checks")
	}
	return nil
}
