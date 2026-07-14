package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/metrics"
)

func TestRunStandardEmitsCompleteRegistryInOrder(t *testing.T) {
	now := time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	report, err := Run(context.Background(), Request{Profile: ProfileStandard, Since: 7 * 24 * time.Hour}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checks) != 55 {
		t.Fatalf("checks = %d, want 55", len(report.Checks))
	}
	applicable, excluded := 0, 0
	for i, check := range report.Checks {
		if check.ID != Registry()[i].ID {
			t.Fatalf("check[%d] = %q, want %q", i, check.ID, Registry()[i].ID)
		}
		if check.Status == StatusSkipped && check.SkipReason == SkipProfileExcluded {
			excluded++
		} else {
			applicable++
		}
	}
	if applicable != 46 || excluded != 9 {
		t.Fatalf("membership = %d/%d, want 46/9", applicable, excluded)
	}
	if err := ValidateReport(report); err != nil {
		t.Fatalf("ValidateReport: %v", err)
	}
	if report.Scope.Filtered || !report.Scope.WholeSystem || len(report.Scope.Categories) != 5 || len(report.Scope.Sources) != 7 || report.Scope.CheckIDs == nil {
		t.Fatalf("scope = %#v", report.Scope)
	}
}

func TestRunMissingRuntimeCapabilitiesEmitsUnknownRatherThanOmitting(t *testing.T) {
	now := time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC)
	features := allFeatures()
	features.ConfigVerified = false
	features.DatabaseOpenedQueryOnly = false
	deps := Dependencies{Features: features, Clock: func() time.Time { return now }}
	report, err := Run(context.Background(), Request{Profile: ProfileStandard, Since: 7 * 24 * time.Hour}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checks) != 55 {
		t.Fatalf("checks = %d", len(report.Checks))
	}
	for _, check := range report.Checks {
		entry, _ := Lookup(check.ID)
		if entry.InProfile(ProfileStandard) && check.Required && check.Status != StatusUnknown {
			t.Fatalf("required unavailable %q = %s, want unknown", check.ID, check.Status)
		}
	}
	if report.Status != StatusUnknown || ExitCode(report) != 3 {
		t.Fatalf("overall = %s exit=%d", report.Status, ExitCode(report))
	}
}

func TestRunFiltersScopeWithoutClaimingWholeSystem(t *testing.T) {
	deps := passingDependencies(time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC))
	report, err := Run(context.Background(), Request{Profile: ProfileStandard, Since: time.Hour, Categories: []Category{CategoryPipeline}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Scope.Filtered || report.Scope.WholeSystem || len(report.Checks) != 14 {
		t.Fatalf("filtered report = %#v", report.Scope)
	}
	for _, check := range report.Checks {
		if check.Category != CategoryPipeline {
			t.Fatalf("unexpected %q", check.ID)
		}
	}
}

func TestRunRejectsDeepUntilDeepExecutorsExist(t *testing.T) {
	_, err := Run(context.Background(), Request{Profile: ProfileDeep}, Dependencies{})
	if err == nil || !errors.Is(err, ErrDeepUnsupported) {
		t.Fatalf("deep error = %v", err)
	}
}

func TestStandardCompletenessEveryApplicableRegistryEntryHasExecutor(t *testing.T) {
	for _, entry := range Registry() {
		if entry.InProfile(ProfileStandard) && !HasExecutor(entry.ID) {
			t.Fatalf("standard check %q has no executor", entry.ID)
		}
	}
}

type fakeStore struct {
	pipeline   map[PipelineStage]PipelineEvidence
	provenance []ProvenanceEvidence
	local      MediaLocalEvidence
	media      []ArchivedMediaRecord
}

func (f fakeStore) Pipeline(context.Context) (map[PipelineStage]PipelineEvidence, error) {
	return f.pipeline, nil
}
func (f fakeStore) Provenance(context.Context) ([]ProvenanceEvidence, error) {
	return f.provenance, nil
}
func (f fakeStore) MediaLocal(context.Context) (MediaLocalEvidence, error)       { return f.local, nil }
func (f fakeStore) ArchivedMedia(context.Context) ([]ArchivedMediaRecord, error) { return f.media, nil }

type fakeDatabase struct{ value DatabaseInspection }

func (f fakeDatabase) Inspect(context.Context, bool) (DatabaseInspection, error) { return f.value, nil }

type fakeMetrics struct{ value metrics.Window }

func (f fakeMetrics) Read(context.Context, time.Time) (metrics.Window, error) { return f.value, nil }

type fakeOKF struct{ value OKFInspection }

func (f fakeOKF) Inspect(context.Context, bool) (OKFInspection, error) { return f.value, nil }

type fakeArchive struct{ value SQLiteArchiveListing }

func (f fakeArchive) List(context.Context) (SQLiteArchiveListing, error) { return f.value, nil }

type fakeMedia struct{}

func (fakeMedia) HeadObject(context.Context, string) (ObjectMetadata, error) {
	return ObjectMetadata{Exists: true, SizeBytes: 10}, nil
}

func allFeatures() Features {
	sources := map[Source]bool{}
	for _, source := range allSources {
		sources[source] = true
	}
	stages := map[PipelineStage]bool{}
	for _, stage := range allPipelineStages {
		stages[stage] = true
	}
	return Features{Layout: "xdg", ConfigSource: "default", ConfigVerified: true, DatabaseOpenedQueryOnly: true, SchedulerEnabled: true, SchedulerInterval: time.Hour, SchedulerJitter: 0, Sources: sources, Stages: stages, MediaLocalEnabled: true, MediaRemoteEnabled: true, SQLiteArchiveCapabilityConfigured: true, SQLiteBackupSchedulerEnabled: true, SQLiteProviderConfigured: true, SQLiteCredentialConfigured: true, OKFEnabled: true}
}

func passingDependencies(now time.Time) Dependencies {
	pipeline := map[PipelineStage]PipelineEvidence{}
	for _, stage := range allPipelineStages {
		pipeline[stage] = PipelineEvidence{Total: 1, Current: 1, PartitionValid: true, ByKind: []KindPartition{}}
	}
	provenance := []ProvenanceEvidence{}
	for _, id := range []CheckID{CheckPipelineItemSummaryProvenance, CheckPipelineItemOCRProvenance, CheckPipelineXMediaTranscriptProvenance, CheckPipelineSourceSummaryProvenance} {
		provenance = append(provenance, ProvenanceEvidence{CheckID: id, SuccessfulCount: 1, CompleteCount: 1, CutoverKnown: true, CutoverAt: now.Add(-24 * time.Hour), MissingByField: map[string]int{}})
	}
	run := metrics.RunRecord{ID: "run", StartedAt: now.Add(-10 * time.Minute), CompletedAt: now.Add(-9 * time.Minute), Status: "ok", SelectedStages: []string{"hydration", "extraction", "summary", "transcription", "ocr"}, CompletedStages: map[string]metrics.StageRecord{"hydration": {Status: "ok"}, "extraction": {Status: "ok"}, "summary": {Status: "ok"}, "transcription": {Status: "ok"}, "ocr": {Status: "ok"}}, Duration: time.Minute, RecordComplete: true}
	imports := map[string]metrics.ImportRecord{}
	for _, source := range []string{"apple_notes", "safari_tabs", "x_bookmarks", "github_stars", "youtube_liked", "youtube_watch_later", "feeds"} {
		imports[source] = metrics.ImportRecord{AttemptedAt: now.Add(-10 * time.Minute), SucceededAt: now.Add(-9 * time.Minute), AttemptCount: 1, SuccessCount: 1, Daily: []metrics.DailyArrival{}}
	}
	return Dependencies{
		Features: allFeatures(), Clock: func() time.Time { return now },
		Store:    fakeStore{pipeline: pipeline, provenance: provenance, local: MediaLocalEvidence{EligibleLocalCount: 1}, media: []ArchivedMediaRecord{{Key: "key", SizeBytes: 10, ArchivedAt: now.Add(-time.Hour)}}},
		Database: fakeDatabase{DatabaseInspection{SchemaCompatibility: "current_compatible", MigrationCompatibility: "current_compatible", QuickCheck: "ok", UserVersion: 12, SupportedVersion: 12, AppliedCount: 12}},
		Metrics:  fakeMetrics{metrics.Window{CoverageStart: now.Add(-7 * 24 * time.Hour), CoverageEnd: now, Runs: []metrics.RunRecord{run}, Imports: imports, Markers: []metrics.Marker{}, DurationSamples: []time.Duration{time.Minute, time.Minute, time.Minute, time.Minute, time.Minute}, ParseErrorPositions: []int64{}}},
		Runtime:  RuntimeVersion{ReleaseVersion: "v0.6.0", Commit: "abcdef1", GitStatus: "clean", Platform: "darwin/arm64", SecurityBaselineID: "v0.6.0-security-pass", SecurityBaselineEpoch: 1},
		OKF:      fakeOKF{OKFInspection{ManifestValid: true, ExportedAt: now.Add(time.Hour - time.Minute), DocumentCount: 1, TraversalComplete: true}},
		Archives: fakeArchive{SQLiteArchiveListing{ConfigurationState: "required_ready", Complete: true, Objects: []ArchiveObject{{SizeBytes: 100, LastModified: now.Add(-time.Hour)}}}},
		Media:    fakeMedia{},
	}
}
