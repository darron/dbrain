package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/metrics"
)

type fakeSemanticInspector struct {
	snapshot SemanticAuditSnapshot
	err      error
}

func (f fakeSemanticInspector) InspectAuditSemantic(context.Context) (SemanticAuditSnapshot, error) {
	return f.snapshot, f.err
}

type failingMetricsReader struct{ err error }

func (f failingMetricsReader) Read(context.Context, time.Time) (metrics.Window, error) {
	return metrics.Window{}, f.err
}

func TestSemanticCurrentReadinessClassifiesClosedRuntimeStates(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		snapshot   SemanticAuditSnapshot
		configured bool
		supported  bool
		wantStatus Status
		wantReq    bool
		capability string
	}{
		{name: "disabled", snapshot: SemanticAuditSnapshot{Backend: "none", Readiness: "disabled"}, wantStatus: StatusUnknown, capability: "disabled"},
		{name: "unsupported", snapshot: SemanticAuditSnapshot{Configured: true, Backend: "unsupported", Readiness: "unavailable"}, configured: true, wantStatus: StatusUnknown, capability: "unsupported"},
		{name: "ready", snapshot: readySemanticSnapshot(), configured: true, supported: true, wantStatus: StatusPass, wantReq: true, capability: "available"},
		{name: "catching up", snapshot: semanticSnapshotWithReadiness("catching_up"), configured: true, supported: true, wantStatus: StatusWarn, wantReq: true, capability: "available"},
		{name: "degraded", snapshot: semanticSnapshotWithReadiness("degraded_blocked"), configured: true, supported: true, wantStatus: StatusFail, wantReq: true, capability: "available"},
		{name: "corrupt", snapshot: semanticSnapshotWithReadiness("corrupt"), configured: true, supported: true, wantStatus: StatusFail, wantReq: true, capability: "available"},
		{name: "stale", snapshot: semanticSnapshotWithReadiness("stale"), configured: true, supported: true, wantStatus: StatusFail, wantReq: true, capability: "available"},
		{name: "unavailable", snapshot: semanticSnapshotWithReadiness("unavailable"), configured: true, supported: true, wantStatus: StatusFail, wantReq: true, capability: "available"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := passingDependencies(now)
			deps.Features.SemanticConfigured = tc.configured
			deps.Features.SemanticCapabilityAvailable = tc.supported
			deps.Semantic = fakeSemanticInspector{snapshot: tc.snapshot}
			report, err := Run(t.Context(), Request{Profile: ProfileStandard, CheckIDs: []CheckID{CheckSemanticCurrentReadiness}}, deps)
			if err != nil {
				t.Fatal(err)
			}
			check := checkByIDForTest(t, report, CheckSemanticCurrentReadiness)
			if check.Status != tc.wantStatus || check.Required != tc.wantReq || check.Evidence["capability"] != tc.capability {
				t.Fatalf("semantic current check=%#v, want status=%s required=%t capability=%s", check, tc.wantStatus, tc.wantReq, tc.capability)
			}
			if check.Evidence["readiness"] != string(tc.snapshot.Readiness) {
				t.Fatalf("readiness evidence=%#v", check.Evidence)
			}
		})
	}
}

func TestSemanticLatestRefreshFailsWithClosedTypedFailure(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	failedAt := now.Add(-time.Minute)
	deps := semanticAuditDependencies(now, metrics.SemanticActivity{Present: true, Latest: metrics.SemanticRefreshRecord{
		State: "failed", StartedAt: failedAt.Add(-5 * time.Second), CompletedAt: failedAt, FailureAt: failedAt,
		Duration: 5 * time.Second, ErrorCode: "semantic_embedding_failed", Stages: successfulMetricStages(),
	}})
	report, err := Run(t.Context(), Request{Profile: ProfileStandard, CheckIDs: []CheckID{CheckSemanticLatestAttachedRefresh}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	check := checkByIDForTest(t, report, CheckSemanticLatestAttachedRefresh)
	if check.Status != StatusFail || !check.Required || check.Evidence["semantic_error_code"] != "semantic_embedding_failed" || check.Evidence["failure_at"] != failedAt.Format(time.RFC3339) {
		t.Fatalf("failed semantic refresh check=%#v", check)
	}
}

func TestSemanticSuccessfulZeroWorkRefreshPasses(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	completedAt := now.Add(-time.Minute)
	deps := semanticAuditDependencies(now, metrics.SemanticActivity{Present: true, Latest: metrics.SemanticRefreshRecord{
		State: "succeeded", StartedAt: completedAt.Add(-time.Second), CompletedAt: completedAt,
		Duration: time.Second, Stages: successfulMetricStages(), Counters: metrics.SemanticRefreshCounters{},
	}})
	report, err := Run(t.Context(), Request{Profile: ProfileStandard, CheckIDs: []CheckID{CheckSemanticLatestAttachedRefresh}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	check := checkByIDForTest(t, report, CheckSemanticLatestAttachedRefresh)
	if check.Status != StatusPass || !check.Required || check.Evidence["embedded_chunk_count"] != 0 || check.Evidence["successor_run_count"] != 0 {
		t.Fatalf("zero-work semantic refresh check=%#v", check)
	}
}

func TestSemanticMissingOrIncompleteMetricsIsUnknownWhenRequired(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name     string
		metrics  MetricsReader
		wantCode ErrorCode
	}{
		{name: "missing", metrics: fakeMetrics{metrics.Window{}}, wantCode: ErrorUnavailable},
		{name: "read failure", metrics: failingMetricsReader{err: errors.New("private metrics path")}, wantCode: ErrorRead},
		{name: "incomplete", metrics: fakeMetrics{metrics.Window{Semantic: metrics.SemanticActivity{
			Present: true, Incomplete: true,
			Latest: metrics.SemanticRefreshRecord{State: "unknown", Stages: unknownMetricStages()},
		}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps := semanticAuditDependencies(now, metrics.SemanticActivity{})
			deps.Metrics = test.metrics
			report, err := Run(t.Context(), Request{Profile: ProfileStandard, CheckIDs: []CheckID{CheckSemanticLatestAttachedRefresh, CheckSemanticStageSummary}}, deps)
			if err != nil {
				t.Fatal(err)
			}
			latest := checkByIDForTest(t, report, CheckSemanticLatestAttachedRefresh)
			stages := checkByIDForTest(t, report, CheckSemanticStageSummary)
			if latest.Status != StatusUnknown || !latest.Required || latest.ErrorCode != test.wantCode {
				t.Fatalf("latest check=%#v", latest)
			}
			if stages.Status != StatusUnknown || stages.Required {
				t.Fatalf("stage check=%#v", stages)
			}
		})
	}
}

func TestSemanticStageSummaryIsInformationalActivity(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	stages := successfulMetricStages()
	stages[2].Status = "failed"
	stages[2].Duration = 48 * time.Hour
	deps := semanticAuditDependencies(now, metrics.SemanticActivity{Present: true, Latest: metrics.SemanticRefreshRecord{
		State: "failed", ErrorCode: "semantic_flush_failed", StartedAt: now.Add(-49 * time.Hour),
		CompletedAt: now.Add(-time.Hour), FailureAt: now.Add(-time.Hour), Duration: 48 * time.Hour,
		Counters: metrics.SemanticRefreshCounters{ProjectedParents: 1 << 30, EmbeddedChunks: 1 << 30}, Stages: stages,
	}})
	report, err := Run(t.Context(), Request{Profile: ProfileStandard, CheckIDs: []CheckID{CheckSemanticStageSummary}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	check := checkByIDForTest(t, report, CheckSemanticStageSummary)
	if check.Status != StatusPass || check.Required {
		t.Fatalf("stage activity affected health: %#v", check)
	}
}

func TestSemanticRequiredPolicyIsV2OnlyAndStageSummaryRemainsInformational(t *testing.T) {
	for id, want := range map[CheckID]RequiredCondition{
		CheckSemanticCurrentReadiness:      RequiredSemantic,
		CheckSemanticLatestAttachedRefresh: RequiredSemantic,
		CheckSemanticStageSummary:          RequiredNever,
	} {
		entry, ok := Lookup(id)
		if !ok || entry.RequiredWhen != want {
			t.Fatalf("semantic registry entry %s=%#v want required=%s", id, entry, want)
		}
	}
	legacy, ok := RegistryForSchema(SchemaV1)
	if !ok || len(legacy) != 55 {
		t.Fatalf("v1 registry changed: ok=%t len=%d", ok, len(legacy))
	}
}

func semanticAuditDependencies(now time.Time, activity metrics.SemanticActivity) Dependencies {
	deps := passingDependencies(now)
	deps.Features.SemanticConfigured = true
	deps.Features.SemanticCapabilityAvailable = true
	deps.Semantic = fakeSemanticInspector{snapshot: readySemanticSnapshot()}
	window := deps.Metrics.(fakeMetrics).value
	window.Semantic = activity
	deps.Metrics = fakeMetrics{window}
	return deps
}

func readySemanticSnapshot() SemanticAuditSnapshot {
	return SemanticAuditSnapshot{
		Configured: true, CapabilityAvailable: true, Backend: "ollama",
		ProfileID:          "embedding-profile-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ActiveGenerationID: "generation-abc", Readiness: "ready", IndexedVectorCount: 12,
		L0VectorCount: 2, TombstoneCount: 1, SegmentCount: 3,
	}
}

func semanticSnapshotWithReadiness(readiness SemanticReadiness) SemanticAuditSnapshot {
	snapshot := readySemanticSnapshot()
	snapshot.Readiness = readiness
	return snapshot
}

func successfulMetricStages() []metrics.SemanticStageRecord {
	return []metrics.SemanticStageRecord{
		{Stage: "projection", Status: "succeeded", Duration: time.Second},
		{Stage: "embedding", Status: "succeeded", Duration: 2 * time.Second},
		{Stage: "flush", Status: "succeeded", Duration: 3 * time.Second},
		{Stage: "compaction", Status: "succeeded", Duration: 4 * time.Second},
		{Stage: "verification", Status: "succeeded", Duration: 5 * time.Second},
		{Stage: "readiness", Status: "succeeded", Duration: 6 * time.Second},
	}
}

func unknownMetricStages() []metrics.SemanticStageRecord {
	stages := successfulMetricStages()
	for index := range stages {
		stages[index].Status = "unknown"
		stages[index].Duration = 0
	}
	return stages
}
