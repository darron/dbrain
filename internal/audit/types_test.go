package audit

import (
	"encoding/json"
	"testing"
	"time"
)

func TestReportJSONUsesClosedNonNilSchema(t *testing.T) {
	now := time.Date(2026, 7, 14, 1, 2, 3, 0, time.UTC)
	report := NewReport(ProfileStandard, now)
	report.CompletedAt = now.Add(time.Second)
	report.Checks = append(report.Checks, Check{
		ID: CheckBoundaryConfig, Category: CategoryBoundary, Status: StatusPass,
		Confidence: ConfidenceHigh, Required: true, Summary: "Resolved audit configuration is verified",
		ObservedAt: now, Evidence: Evidence{"layout": "xdg", "config_source": "default", "verified": true},
	})
	FinalizeReport(&report)

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"schema":"dbrain.audit.v1","audit_id":"","profile":"standard","scope":{"categories":[],"sources":[],"check_ids":[],"filtered":false,"whole_system":true},"started_at":"2026-07-14T01:02:03Z","completed_at":"2026-07-14T01:02:04Z","status":"pass","confidence":"high","boundary":{"layout":"","config_verified":false,"database_verified":false,"version":"","commit":"","git_status":"unknown","platform":"","security_baseline":"","security_baseline_epoch":0,"schema_version":0,"schema_compatibility":""},"summary":{"all":{"pass":1,"warn":0,"fail":0,"unknown":0,"skipped":0},"required":{"pass":1,"warn":0,"fail":0,"unknown":0}},"checks":[{"id":"boundary.config","category":"boundary","status":"pass","confidence":"high","required":true,"summary":"Resolved audit configuration is verified","observed_at":"2026-07-14T01:02:03Z","evidence":{"config_source":"default","layout":"xdg","verified":true}}]}`
	if string(data) != want {
		t.Fatalf("report JSON mismatch\n got: %s\nwant: %s", data, want)
	}
	if err := ValidateReport(report); err != nil {
		t.Fatalf("ValidateReport: %v", err)
	}
}

func TestReportJSONRejectsUnknownClosedValues(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Report)
	}{
		{"profile", func(r *Report) { r.Profile = "future" }},
		{"category", func(r *Report) { r.Scope.Categories = []Category{"future"} }},
		{"source", func(r *Report) { r.Scope.Sources = []Source{"future"} }},
		{"check id", func(r *Report) { r.Scope.CheckIDs = []CheckID{"future.check"} }},
		{"status", func(r *Report) { r.Status = "future" }},
		{"confidence", func(r *Report) { r.Confidence = "future" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReport(ProfileStandard, time.Now().UTC())
			tt.edit(&r)
			if err := ValidateReport(r); err == nil {
				t.Fatal("expected closed-schema validation failure")
			}
		})
	}
}
