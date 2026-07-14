package audit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFreshnessUsesExactProfileDeadlinesWithoutMutatingReport(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	fast := testStoredReport(t, ProfileFast, now.Add(-3*time.Hour), StatusPass)
	original, _ := json.Marshal(fast)
	presented := PresentReport(&fast, ProfileFast, time.Hour, 6*time.Hour, now)
	if presented.Report == nil || presented.Freshness.Status != FreshnessUnknown || presented.Freshness.Reason != FreshnessStale || presented.Freshness.DeadlineSeconds != 7200 || presented.Freshness.AgeSeconds == nil || *presented.Freshness.AgeSeconds != 10800 {
		t.Fatalf("fast presentation = %#v", presented)
	}
	after, _ := json.Marshal(fast)
	if string(after) != string(original) {
		t.Fatal("freshness mutated immutable report")
	}

	standard := testStoredReport(t, ProfileStandard, now.Add(-11*time.Hour), StatusPass)
	current := PresentReport(&standard, ProfileStandard, time.Hour, 3*time.Hour, now)
	if current.Freshness.Status != FreshnessCurrent || current.Freshness.DeadlineSeconds != 43200 {
		t.Fatalf("standard presentation = %#v", current)
	}
	wrong := PresentReport(&standard, ProfileFast, time.Hour, 6*time.Hour, now)
	if wrong.Report != nil || wrong.Freshness.Reason != FreshnessNotFound {
		t.Fatalf("cross-profile presentation = %#v", wrong)
	}
}

func TestFreshnessMissingOmitsAgeAndKeepsDeadline(t *testing.T) {
	presented := PresentReport(nil, ProfileStandard, time.Hour, 4*time.Hour, time.Now().UTC())
	data, err := json.Marshal(presented)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if text != `{"report":null,"freshness":{"status":"unknown","reason":"not_found","deadline_seconds":43200}}` {
		t.Fatalf("wire = %s", text)
	}
	if strings.Contains(text, "age_seconds") {
		t.Fatal("missing report exposed age")
	}
}

func TestFreshnessDeadlineBoundaryAndFutureClockAreCurrent(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	atBoundary := testStoredReport(t, ProfileFast, now.Add(-2*time.Hour), StatusPass)
	presented := PresentReport(&atBoundary, ProfileFast, time.Hour, 6*time.Hour, now)
	if presented.Freshness.Status != FreshnessCurrent {
		t.Fatalf("exact deadline status = %s", presented.Freshness.Status)
	}
	future := testStoredReport(t, ProfileFast, now.Add(time.Hour), StatusPass)
	presented = PresentReport(&future, ProfileFast, time.Hour, 6*time.Hour, now)
	if presented.Freshness.Status != FreshnessCurrent || presented.Freshness.AgeSeconds == nil || *presented.Freshness.AgeSeconds != 0 {
		t.Fatalf("future clock presentation = %#v", presented)
	}
}
