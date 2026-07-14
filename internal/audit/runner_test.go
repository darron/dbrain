package audit

import (
	"context"
	"errors"
	"strings"
	"sync"
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

func TestValidateReportRejectsClosedContractViolations(t *testing.T) {
	now := time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC)
	makeReport := func() Report {
		report, err := Run(t.Context(), Request{Profile: ProfileStandard, Since: 7 * 24 * time.Hour}, passingDependencies(now))
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	checkByID := func(report *Report, id CheckID) *Check {
		for index := range report.Checks {
			if report.Checks[index].ID == id {
				return &report.Checks[index]
			}
		}
		t.Fatalf("check %q not found", id)
		return nil
	}
	tests := []struct {
		name string
		edit func(*Report)
	}{
		{"omitted check", func(report *Report) { report.Checks = report.Checks[:54]; FinalizeReport(report) }},
		{"arbitrary summary", func(report *Report) { report.Checks[0].Summary = "arbitrary" }},
		{"arbitrary remediation", func(report *Report) { report.Checks[0].Remediation = "run /secret/path" }},
		{"missing evidence", func(report *Report) { delete(report.Checks[0].Evidence, "layout") }},
		{"path in boundary", func(report *Report) { report.Boundary.Version = "/secret/path" }},
		{"observation before start", func(report *Report) { report.Checks[0].ObservedAt = report.StartedAt.Add(-time.Second) }},
		{"bad audit id", func(report *Report) { report.AuditID = "audit_guessable" }},
		{"scope mismatch", func(report *Report) { report.Scope.Categories = []Category{CategoryBoundary} }},
		{"profile exclusion", func(report *Report) { report.Checks[46].Status = StatusUnknown; report.Checks[46].SkipReason = "" }},
		{"in-profile profile exclusion", func(report *Report) {
			report.Checks[0].Status = StatusSkipped
			report.Checks[0].Confidence = ConfidenceUnknown
			report.Checks[0].Required = false
			report.Checks[0].Evidence = Evidence{}
			report.Checks[0].Threshold = nil
			report.Checks[0].Remediation = ""
			report.Checks[0].SkipReason = SkipProfileExcluded
			FinalizeReport(report)
		}},
		{"always required false", func(report *Report) {
			report.Checks[0].Required = false
			FinalizeReport(report)
		}},
		{"conditional required false", func(report *Report) {
			for _, id := range []CheckID{
				CheckSchedulerLatestSync,
				CheckImportsXBookmarksPoll,
				CheckPipelineExtractionPartition,
				CheckDurabilityMediaLocalCoverage,
				CheckDurabilityMediaRemote,
				CheckDurabilitySQLiteBackupAge,
				CheckDurabilityOKFFreshness,
			} {
				checkByID(report, id).Required = false
			}
			FinalizeReport(report)
		}},
		{"optional arrivals required true", func(report *Report) {
			checkByID(report, CheckImportsXBookmarksArrivals).Required = true
			FinalizeReport(report)
		}},
		{"conditional failure forged optional green", func(report *Report) {
			check := checkByID(report, CheckSchedulerLatestSync)
			check.Status = StatusFail
			check.Required = false
			FinalizeReport(report)
		}},
		{"always required feature disabled", func(report *Report) {
			report.Checks[0].Status = StatusSkipped
			report.Checks[0].Confidence = ConfidenceUnknown
			report.Checks[0].Required = false
			report.Checks[0].Evidence = Evidence{}
			report.Checks[0].Threshold = nil
			report.Checks[0].Remediation = ""
			report.Checks[0].SkipReason = SkipFeatureDisabled
			FinalizeReport(report)
		}},
		{"threshold mismatch", func(report *Report) {
			for index := range report.Checks {
				if report.Checks[index].Threshold != nil {
					report.Checks[index].Threshold.WarnAfterSeconds++
					return
				}
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := makeReport()
			test.edit(&report)
			if err := ValidateReport(report); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestRunUsesDistinctAuditIDsAndRealObservationTimes(t *testing.T) {
	start := time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC)
	step := 0
	deps := passingDependencies(start)
	deps.Clock = func() time.Time {
		value := start.Add(time.Duration(step) * time.Millisecond)
		step++
		return value
	}
	first, err := Run(t.Context(), Request{Profile: ProfileFast, CheckIDs: []CheckID{CheckBoundaryRuntime}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	step = 0
	second, err := Run(t.Context(), Request{Profile: ProfileFast, CheckIDs: []CheckID{CheckBoundaryRuntime}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if first.AuditID == second.AuditID || !first.CompletedAt.After(first.StartedAt) || !first.Checks[0].ObservedAt.After(first.StartedAt) {
		t.Fatalf("first=%#v second_id=%s", first, second.AuditID)
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

func TestRunMixedCategoryAndSourceScopeKeepsSourceLessCategories(t *testing.T) {
	deps := passingDependencies(time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC))
	report, err := Run(context.Background(), Request{
		Profile: ProfileStandard,
		Since:   time.Hour,
		Categories: []Category{
			CategoryImports,
			CategoryPipeline,
		},
		Sources: []Source{SourceXBookmarks},
	}, deps)
	if err != nil {
		t.Fatal(err)
	}
	seenPipeline := false
	for _, check := range report.Checks {
		if check.Category == CategoryPipeline {
			seenPipeline = true
		}
		if check.Category == CategoryImports {
			entry, _ := Lookup(check.ID)
			if entry.Source != SourceXBookmarks {
				t.Fatalf("source filter leaked import check %q for %q", check.ID, entry.Source)
			}
		}
	}
	if !seenPipeline {
		t.Fatalf("mixed scope dropped source-less pipeline category: %#v", report.Scope)
	}
}

func TestRunIncompleteLatestAttemptAndExhaustedWindowAreUnknown(t *testing.T) {
	now := time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	window := deps.Metrics.(fakeMetrics).value
	window.Runs = append(window.Runs, metrics.RunRecord{ID: "incomplete", StartedAt: now.Add(-time.Minute), CompletedStages: map[string]metrics.StageRecord{}})
	window.AttemptCount = 2
	window.CompletedCount = 1
	window.ByteBudgetExhausted = true
	deps.Metrics = fakeMetrics{window}
	report, err := Run(t.Context(), Request{Profile: ProfileStandard, Since: 7 * 24 * time.Hour, CheckIDs: []CheckID{CheckSchedulerLatestSync, CheckMetricsWindow}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checks) != 2 || report.Checks[0].Status != StatusUnknown || report.Checks[1].Status != StatusUnknown {
		t.Fatalf("checks = %#v", report.Checks)
	}
	if got := report.Checks[1].Evidence["completed_attempt_count"]; got != 1 {
		t.Fatalf("completed attempts = %#v", got)
	}
}

func TestRunUnknownGitStatusMakesRuntimeIdentityUnknown(t *testing.T) {
	deps := passingDependencies(time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC))
	deps.Runtime.GitStatus = "unknown"
	report, err := Run(t.Context(), Request{Profile: ProfileFast, CheckIDs: []CheckID{CheckBoundaryRuntime}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checks) != 1 || report.Checks[0].Status != StatusUnknown {
		t.Fatalf("checks = %#v", report.Checks)
	}
}

func TestRunExpectedCommitAcceptsShortPrefix(t *testing.T) {
	deps := passingDependencies(time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC))
	deps.Runtime.Commit = "0123456789abcdef0123456789abcdef01234567"
	report, err := Run(t.Context(), Request{
		Profile:      ProfileFast,
		CheckIDs:     []CheckID{CheckBoundaryRuntime},
		ExpectCommit: "0123456",
	}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checks) != 1 || report.Checks[0].Status != StatusPass {
		t.Fatalf("checks = %#v", report.Checks)
	}
	if matched, _ := report.Checks[0].Evidence["expected_commit_matched"].(bool); !matched {
		t.Fatalf("expected short commit prefix to match: %#v", report.Checks[0].Evidence)
	}
}

func TestRunExpectedCommitAddsRuntimeCheckToFilteredScope(t *testing.T) {
	deps := passingDependencies(time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC))
	deps.Runtime.Commit = "0123456789abcdef0123456789abcdef01234567"
	report, err := Run(t.Context(), Request{
		Profile:      ProfileFast,
		Categories:   []Category{CategoryDurability},
		ExpectCommit: "fedcba9876543210fedcba9876543210fedcba98",
	}, deps)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, check := range report.Checks {
		if check.ID != CheckBoundaryRuntime {
			continue
		}
		found = true
		if check.Status != StatusFail {
			t.Fatalf("boundary.runtime status = %s, want fail", check.Status)
		}
	}
	if !found {
		t.Fatalf("expected boundary.runtime in filtered scope: %#v", report.Scope)
	}
}

func TestRunRejectsInvalidExpectedCommit(t *testing.T) {
	deps := passingDependencies(time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC))
	_, err := Run(t.Context(), Request{
		Profile:      ProfileFast,
		CheckIDs:     []CheckID{CheckBoundaryRuntime},
		ExpectCommit: "not-a-sha",
	}, deps)
	if err == nil || !strings.Contains(err.Error(), "invalid expected commit") {
		t.Fatalf("error = %v, want invalid expected commit", err)
	}
}

func TestRunFastFilteredImportsLoadsMetricsOnlyAndNoNetworkCapabilities(t *testing.T) {
	now := time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	storeCalls, databaseCalls, okfCalls, archiveCalls := 0, 0, 0, 0
	deps.Store = countingStore{calls: &storeCalls}
	deps.Database = countingDatabase{calls: &databaseCalls}
	deps.OKF = countingOKF{calls: &okfCalls}
	deps.Archives = countingArchive{calls: &archiveCalls}
	report, err := Run(t.Context(), Request{Profile: ProfileFast, Since: time.Hour, Categories: []Category{CategoryImports}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if storeCalls != 0 || databaseCalls != 0 || okfCalls != 0 || archiveCalls != 0 {
		t.Fatalf("unrelated calls store=%d database=%d okf=%d archive=%d", storeCalls, databaseCalls, okfCalls, archiveCalls)
	}
	if len(report.Scope.Categories) != 1 || report.Scope.Categories[0] != CategoryImports || len(report.Scope.Sources) != 7 || len(report.Scope.CheckIDs) != 0 {
		t.Fatalf("effective scope = %#v", report.Scope)
	}
}

func TestTimeoutClassCeilings(t *testing.T) {
	tests := []struct {
		profile Profile
		class   TimeoutClass
		want    time.Duration
	}{
		{ProfileFast, TimeoutLocalQuery, 5 * time.Second},
		{ProfileStandard, TimeoutLocalQuery, 30 * time.Second},
		{ProfileFast, TimeoutMetricsOrManifest, 10 * time.Second},
		{ProfileStandard, TimeoutSQLiteOrOKFIntegrity, 2 * time.Minute},
		{ProfileStandard, TimeoutRemoteMetadata, 2 * time.Minute},
	}
	for _, test := range tests {
		if got := timeoutFor(test.profile, test.class); got != test.want {
			t.Fatalf("timeoutFor(%s, %s) = %s, want %s", test.profile, test.class, got, test.want)
		}
	}
	overrides := map[TimeoutClass]time.Duration{TimeoutLocalQuery: 3 * time.Second, TimeoutSQLiteOrOKFIntegrity: 3 * time.Minute}
	if got := timeoutFor(ProfileStandard, TimeoutLocalQuery, overrides); got != 3*time.Second {
		t.Fatalf("downward local override = %s", got)
	}
	if got := timeoutFor(ProfileStandard, TimeoutSQLiteOrOKFIntegrity, overrides); got != 2*time.Minute {
		t.Fatalf("upward integrity override must clamp, got %s", got)
	}
}

func TestRunAppliesLocalAndIntegrityDeadlinesToSeparateDatabaseCalls(t *testing.T) {
	now := time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.Timeouts = map[TimeoutClass]time.Duration{TimeoutLocalQuery: 2 * time.Second, TimeoutSQLiteOrOKFIntegrity: time.Minute}
	recorder := &deadlineDatabase{value: DatabaseInspection{
		SchemaCompatibility: "current_compatible", MigrationCompatibility: "current_compatible",
		QuickCheck: "ok", UserVersion: 12, SupportedVersion: 12, AppliedCount: 12,
	}}
	deps.Database = recorder
	_, err := Run(t.Context(), Request{Profile: ProfileStandard, CheckIDs: []CheckID{CheckIntegritySchemaIdentity, CheckIntegritySQLiteQuickCheck}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorder.calls) != 2 || recorder.calls[0].full || !recorder.calls[1].full {
		t.Fatalf("database calls = %#v", recorder.calls)
	}
	if recorder.calls[0].remaining > 3*time.Second || recorder.calls[0].remaining < time.Second {
		t.Fatalf("identity deadline remaining = %s", recorder.calls[0].remaining)
	}
	if recorder.calls[1].remaining > 61*time.Second || recorder.calls[1].remaining < 59*time.Second {
		t.Fatalf("integrity deadline remaining = %s", recorder.calls[1].remaining)
	}
}

func TestRunKeepsSnapshotLocalInspectionsWithinConcurrencyCeiling(t *testing.T) {
	now := time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	tracker := &localConcurrencyStore{now: now}
	deps.Store = tracker
	_, err := Run(t.Context(), Request{Profile: ProfileStandard, Since: time.Hour, CheckIDs: []CheckID{
		CheckPipelineExtractionPartition,
		CheckPipelineItemSummaryProvenance,
		CheckDurabilityMediaLocalCoverage,
		CheckDurabilityMediaRemote,
	}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if tracker.calls != 4 || tracker.maxActive > 4 {
		t.Fatalf("local inspection calls=%d max_concurrency=%d", tracker.calls, tracker.maxActive)
	}
}

func TestRunStandardOKFLoadsFullInspectionOnlyOnce(t *testing.T) {
	now := time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	inspector := &recordingOKF{value: OKFInspection{ManifestValid: true, ExportedAt: now.Add(-time.Minute), TraversalComplete: true}}
	deps.OKF = inspector
	_, err := Run(t.Context(), Request{Profile: ProfileStandard, CheckIDs: []CheckID{CheckDurabilityOKFFreshness, CheckDurabilityOKFValidation}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(inspector.full) != 1 || !inspector.full[0] {
		t.Fatalf("OKF calls = %#v", inspector.full)
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
func (f fakeStore) CountLocalIdentityMatches(context.Context, Source, []string) (int, error) {
	return 0, nil
}

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

type countingStore struct{ calls *int }

func (f countingStore) Pipeline(context.Context) (map[PipelineStage]PipelineEvidence, error) {
	*f.calls++
	return nil, nil
}

type localConcurrencyStore struct {
	mu        sync.Mutex
	now       time.Time
	active    int
	maxActive int
	calls     int
}

func (s *localConcurrencyStore) inspect(fn func()) {
	s.mu.Lock()
	s.active++
	s.calls++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()
	time.Sleep(time.Millisecond)
	fn()
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
}

func (s *localConcurrencyStore) Pipeline(context.Context) (out map[PipelineStage]PipelineEvidence, err error) {
	s.inspect(func() {
		out = map[PipelineStage]PipelineEvidence{PipelineExtraction: {Total: 1, Current: 1, PartitionValid: true, ByKind: []KindPartition{}}}
	})
	return out, nil
}
func (s *localConcurrencyStore) Provenance(context.Context) (out []ProvenanceEvidence, err error) {
	s.inspect(func() {
		out = []ProvenanceEvidence{{CheckID: CheckPipelineItemSummaryProvenance, SuccessfulCount: 1, CompleteCount: 1, CutoverKnown: true, CutoverAt: s.now.Add(-time.Hour), MissingByField: map[string]int{}}}
	})
	return out, nil
}
func (s *localConcurrencyStore) MediaLocal(context.Context) (out MediaLocalEvidence, err error) {
	s.inspect(func() { out = MediaLocalEvidence{EligibleLocalCount: 1} })
	return out, nil
}
func (s *localConcurrencyStore) ArchivedMedia(context.Context) (out []ArchivedMediaRecord, err error) {
	s.inspect(func() { out = []ArchivedMediaRecord{} })
	return out, nil
}
func (s *localConcurrencyStore) CountLocalIdentityMatches(context.Context, Source, []string) (int, error) {
	return 0, nil
}
func (f countingStore) Provenance(context.Context) ([]ProvenanceEvidence, error) {
	*f.calls++
	return nil, nil
}
func (f countingStore) MediaLocal(context.Context) (MediaLocalEvidence, error) {
	*f.calls++
	return MediaLocalEvidence{}, nil
}
func (f countingStore) ArchivedMedia(context.Context) ([]ArchivedMediaRecord, error) {
	*f.calls++
	return nil, nil
}
func (f countingStore) CountLocalIdentityMatches(context.Context, Source, []string) (int, error) {
	*f.calls++
	return 0, nil
}

type countingDatabase struct{ calls *int }

func (f countingDatabase) Inspect(context.Context, bool) (DatabaseInspection, error) {
	*f.calls++
	return DatabaseInspection{}, nil
}

type databaseDeadlineCall struct {
	full      bool
	remaining time.Duration
}

type deadlineDatabase struct {
	value DatabaseInspection
	calls []databaseDeadlineCall
}

func (d *deadlineDatabase) Inspect(ctx context.Context, full bool) (DatabaseInspection, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return DatabaseInspection{}, errors.New("database inspection context has no deadline")
	}
	d.calls = append(d.calls, databaseDeadlineCall{full: full, remaining: time.Until(deadline)})
	return d.value, nil
}

type countingOKF struct{ calls *int }

func (f countingOKF) Inspect(context.Context, bool) (OKFInspection, error) {
	*f.calls++
	return OKFInspection{}, nil
}

type countingArchive struct{ calls *int }

func (f countingArchive) List(context.Context) (SQLiteArchiveListing, error) {
	*f.calls++
	return SQLiteArchiveListing{}, nil
}

type recordingOKF struct {
	value OKFInspection
	full  []bool
}

func (f *recordingOKF) Inspect(_ context.Context, full bool) (OKFInspection, error) {
	f.full = append(f.full, full)
	return f.value, nil
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
		Metrics:  fakeMetrics{metrics.Window{CoverageStart: now.Add(-7 * 24 * time.Hour), CoverageEnd: now, Runs: []metrics.RunRecord{run}, Imports: imports, Markers: []metrics.Marker{}, DurationSamples: []time.Duration{time.Minute, time.Minute, time.Minute, time.Minute, time.Minute}, ParseErrorPositions: []int64{}, AttemptCount: 1, CompletedCount: 1, LatestAttemptPresent: true, LatestCompletedPresent: true}},
		Runtime:  RuntimeVersion{ReleaseVersion: "v0.6.0", Commit: "abcdef1", GitStatus: "clean", Platform: "darwin/arm64", SecurityBaselineID: "v0.6.0-security-pass", SecurityBaselineEpoch: 1},
		OKF:      fakeOKF{OKFInspection{ManifestValid: true, ExportedAt: now.Add(time.Hour - time.Minute), DocumentCount: 1, TraversalComplete: true}},
		Archives: fakeArchive{SQLiteArchiveListing{ConfigurationState: "required_ready", Complete: true, Objects: []ArchiveObject{{Key: "archive/db/brain-valid.db.gz", ValidKey: true, SizeBytes: 100, LastModified: now.Add(-time.Hour)}}}},
		Media:    fakeMedia{},
	}
}
