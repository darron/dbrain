package audit

import (
	"encoding/json"
	"testing"
	"time"
)

func TestReportJSONUsesClosedNonNilSchema(t *testing.T) {
	now := time.Date(2026, 7, 14, 1, 2, 3, 0, time.UTC)
	report := NewReport(ProfileStandard, now)
	report.AuditID = "audit_20260714T010203.000000000Z_00000001"
	report.Scope = Scope{Categories: []Category{CategoryBoundary}, Sources: []Source{}, CheckIDs: []CheckID{CheckBoundaryConfig}, Filtered: true, WholeSystem: false}
	report.Boundary.Layout = "xdg"
	report.CompletedAt = now.Add(time.Second)
	report.Checks = append(report.Checks, Check{
		ID: CheckBoundaryConfig, Category: CategoryBoundary, Status: StatusPass,
		Confidence: ConfidenceHigh, Required: true, Summary: fixedSummary(CheckBoundaryConfig),
		ObservedAt: now, Evidence: Evidence{"layout": "xdg", "config_source": "default", "verified": true},
	})
	FinalizeReport(&report)

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"schema":"dbrain.audit.v1","audit_id":"audit_20260714T010203.000000000Z_00000001","profile":"standard","scope":{"categories":["boundary"],"sources":[],"check_ids":["boundary.config"],"filtered":true,"whole_system":false},"started_at":"2026-07-14T01:02:03Z","completed_at":"2026-07-14T01:02:04Z","status":"pass","confidence":"high","boundary":{"layout":"xdg","config_verified":false,"database_verified":false,"version":"","commit":"","git_status":"unknown","platform":"","security_baseline":"","security_baseline_epoch":0,"schema_version":0,"schema_compatibility":""},"summary":{"all":{"pass":1,"warn":0,"fail":0,"unknown":0,"skipped":0},"required":{"pass":1,"warn":0,"fail":0,"unknown":0}},"checks":[{"id":"boundary.config","category":"boundary","status":"pass","confidence":"high","required":true,"summary":"Audit result for boundary.config","observed_at":"2026-07-14T01:02:03Z","evidence":{"config_source":"default","layout":"xdg","verified":true}}]}`
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

func TestSQLiteRestorePartialEvidenceIsOptionalOnlyForVerifiedFailure(t *testing.T) {
	entry, ok := Lookup(CheckDurabilitySQLiteRestore)
	if !ok {
		t.Fatal("restore registry entry missing")
	}
	partial := Evidence{"archive_authenticity": "unverified", "compressed_bytes": 8, "cleanup_complete": true}
	if err := validateRequiredEvidence(entry, StatusFail, partial); err != nil {
		t.Fatalf("verified pre-validation failure rejected reached-phase evidence: %v", err)
	}
	if err := validateRequiredEvidence(entry, StatusPass, partial); err == nil {
		t.Fatal("passing restore accepted missing quick/schema/migration evidence")
	}
}
