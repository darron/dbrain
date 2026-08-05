package audit

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/version"
)

var ErrDeepUnsupported = errors.New("deep audit requires the explicit RunDeep CLI --profile deep entry point")
var errCapabilityUnavailable = errors.New("audit capability unavailable")

type runState struct {
	now                    time.Time
	req                    Request
	deps                   Dependencies
	database               DatabaseInspection
	databaseIdentityErr    error
	databaseIntegrityErr   error
	metrics                metrics.Window
	metricsErr             error
	pipeline               map[PipelineStage]PipelineEvidence
	pipelineErr            error
	provenance             map[CheckID]ProvenanceEvidence
	provenanceErr          error
	local                  MediaLocalEvidence
	localErr               error
	media                  []ArchivedMediaRecord
	mediaErr               error
	okfFast, okfFull       OKFInspection
	okfFastErr, okfFullErr error
	archives               SQLiteArchiveListing
	archivesErr            error
	deep                   *DeepDependencies
	deepMedia              deepMediaResult
	deepMediaErr           error
	deepMediaErrorCode     ErrorCode
	deepArchive            DeepArchiveResult
	deepArchiveErr         error
	deepCleanupComplete    bool
	deepCleanupAttempted   bool
	upstream               map[Source]upstreamObservation
}

func (s *runState) observedAt() time.Time {
	if s.deps.Clock != nil {
		return s.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

type checkExecutor func(context.Context, *runState, RegistryEntry) Check

var executors map[CheckID]checkExecutor
var auditSequence atomic.Uint64

func init() {
	executors = map[CheckID]checkExecutor{}
	bind := func(ids []CheckID, fn checkExecutor) {
		for _, id := range ids {
			executors[id] = fn
		}
	}
	bind([]CheckID{CheckBoundaryConfig, CheckBoundaryRuntime, CheckBoundarySecurityBaseline, CheckBoundaryDatabase, CheckIntegritySchemaIdentity, CheckIntegrityMigrationCompatibility, CheckIntegritySQLiteQuickCheck, CheckIntegrityForeignKeys}, executeBoundary)
	bind([]CheckID{CheckSchedulerLatestSync, CheckSchedulerStageCoverage, CheckSchedulerContinuity, CheckMetricsWindow}, executeScheduler)
	for _, entry := range Registry() {
		if strings.HasPrefix(string(entry.ID), "imports.") {
			executors[entry.ID] = executeImport
		}
	}
	bind([]CheckID{CheckPipelineHydrationPartition, CheckPipelineHydrationPendingAge, CheckPipelineExtractionPartition, CheckPipelineExtractionPendingAge, CheckPipelineSummaryPartition, CheckPipelineSummaryPendingAge, CheckPipelineTranscriptionPartition, CheckPipelineTranscriptionPendingAge, CheckPipelineOCRPartition, CheckPipelineOCRPendingAge, CheckPipelineItemSummaryProvenance, CheckPipelineItemOCRProvenance, CheckPipelineXMediaTranscriptProvenance, CheckPipelineSourceSummaryProvenance}, executePipeline)
	bind([]CheckID{CheckDurabilityMediaLocalCoverage, CheckDurabilityMediaRemote, CheckDurabilitySQLiteBackupConfiguration, CheckDurabilitySQLiteBackupAge, CheckDurabilityOKFFreshness, CheckDurabilityOKFValidation}, executeDurability)
	bind([]CheckID{CheckDurabilityMediaRemoteOnly, CheckDurabilitySQLiteRestore}, executeDeep)
	for id := range upstreamCheckSources {
		executors[id] = executeUpstream
	}
}

func HasExecutor(id CheckID) bool { _, ok := executors[id]; return ok }

func Run(ctx context.Context, req Request, deps Dependencies) (Report, error) {
	if req.Profile == "" {
		req.Profile = ProfileStandard
	}
	if !req.Profile.Valid() {
		return Report{}, fmt.Errorf("invalid audit profile %q", req.Profile)
	}
	if req.Profile == ProfileDeep {
		return Report{}, ErrDeepUnsupported
	}
	return runAudit(ctx, req, deps, nil)
}

func runAudit(ctx context.Context, req Request, deps Dependencies, deep *DeepDependencies) (Report, error) {
	if req.Since <= 0 {
		req.Since = 7 * 24 * time.Hour
	}
	req.ExpectCommit = strings.TrimSpace(req.ExpectCommit)
	if req.ExpectCommit != "" && !commitPattern.MatchString(req.ExpectCommit) {
		return Report{}, fmt.Errorf("invalid expected commit")
	}
	for _, c := range req.Categories {
		if !c.Valid() {
			return Report{}, fmt.Errorf("invalid category %q", c)
		}
	}
	for _, s := range req.Sources {
		if !s.Valid() {
			return Report{}, fmt.Errorf("invalid source %q", s)
		}
	}
	seenOverrides := make(map[Source]struct{}, len(req.SourceOverrides))
	for _, source := range req.SourceOverrides {
		if !source.Valid() {
			return Report{}, fmt.Errorf("invalid source override %q", source)
		}
		if _, exists := seenOverrides[source]; exists {
			return Report{}, fmt.Errorf("duplicate source override %q", source)
		}
		seenOverrides[source] = struct{}{}
		if !containsSource(req.Sources, source) || !sourceOverrideInScope(req, source) {
			return Report{}, fmt.Errorf("source override %q is outside declared parity scope", source)
		}
	}
	for _, id := range req.CheckIDs {
		if _, ok := Lookup(id); !ok {
			return Report{}, fmt.Errorf("invalid check id %q", id)
		}
	}
	now := time.Now().UTC()
	if deps.Clock != nil {
		now = deps.Clock().UTC()
	}
	report := NewReport(req.Profile, now)
	report.AuditID = fmt.Sprintf("audit_%s_%08x", now.Format("20060102T150405.000000000Z07:00"), auditSequence.Add(1))
	report.Scope = effectiveScope(req)
	state := &runState{now: now, req: req, deps: deps, deep: deep, provenance: map[CheckID]ProvenanceEvidence{}, upstream: map[Source]upstreamObservation{}}
	state.load(ctx)
	state.loadDeep(ctx)
	for _, entry := range Registry() {
		if !scopeIncludes(req, entry) {
			continue
		}
		required := isRequired(entry, deps.Features, req)
		var check Check
		switch {
		case !entry.InProfile(req.Profile):
			check = skippedCheck(entry, SkipProfileExcluded, now)
		case !featureEnabled(entry, deps.Features, req):
			check = skippedCheck(entry, SkipFeatureDisabled, now)
		case ctx.Err() != nil:
			check = unknownCheck(entry, ErrorInterrupted, now)
		default:
			fn, ok := executors[entry.ID]
			if !ok {
				check = unknownCheck(entry, ErrorUnavailable, now)
			} else {
				check = fn(ctx, state, entry)
			}
		}
		check.Required = required && check.Status != StatusSkipped
		check.ObservedAt = state.observedAt()
		report.Checks = append(report.Checks, check)
	}
	report.CompletedAt = state.observedAt()
	report.Boundary = Boundary{Layout: deps.Features.Layout, ConfigVerified: deps.Features.ConfigVerified, DatabaseVerified: deps.Features.DatabaseOpenedQueryOnly, Version: deps.Runtime.ReleaseVersion, Commit: deps.Runtime.Commit, GitStatus: normalizeGitStatus(deps.Runtime.GitStatus), Platform: deps.Runtime.Platform, SecurityBaseline: deps.Runtime.SecurityBaselineID, SecurityBaselineEpoch: deps.Runtime.SecurityBaselineEpoch, SchemaVersion: state.database.UserVersion, SchemaCompatibility: state.database.SchemaCompatibility}
	FinalizeReport(&report)
	if err := ValidateReport(report); err != nil {
		return Report{}, fmt.Errorf("validate audit report: %w", err)
	}
	return report, nil
}

func sourceOverrideInScope(req Request, source Source) bool {
	if len(req.Categories) > 0 && !containsCategory(req.Categories, CategoryImports) {
		return false
	}
	if len(req.CheckIDs) == 0 {
		return true
	}
	for id, mappedSource := range upstreamCheckSources {
		if mappedSource == source && containsCheck(req.CheckIDs, id) {
			return true
		}
	}
	return false
}

func (s *runState) load(ctx context.Context) {
	selected := func(id CheckID) bool {
		entry, ok := Lookup(id)
		return ok && scopeIncludes(s.req, entry) && entry.InProfile(s.req.Profile) && featureEnabled(entry, s.deps.Features, s.req)
	}
	selectedCategory := func(category Category) bool {
		for _, entry := range Registry() {
			if entry.Category == category && selected(entry.ID) {
				return true
			}
		}
		return false
	}
	inspectDatabaseIdentity := selected(CheckIntegritySchemaIdentity) || selected(CheckIntegrityMigrationCompatibility)
	inspectDatabaseIntegrity := selected(CheckIntegritySQLiteQuickCheck) || selected(CheckIntegrityForeignKeys)
	if inspectDatabaseIdentity || inspectDatabaseIntegrity {
		if s.deps.Database != nil {
			if inspectDatabaseIdentity {
				s.database, s.databaseIdentityErr = inspectWithTimeout(ctx, timeoutFor(s.req.Profile, TimeoutLocalQuery, s.deps.Features.Timeouts), func(child context.Context) (DatabaseInspection, error) { return s.deps.Database.Inspect(child, false) })
			}
			if inspectDatabaseIntegrity {
				integrity, err := inspectWithTimeout(ctx, timeoutFor(s.req.Profile, TimeoutSQLiteOrOKFIntegrity, s.deps.Features.Timeouts), func(child context.Context) (DatabaseInspection, error) { return s.deps.Database.Inspect(child, true) })
				s.databaseIntegrityErr = err
				if !inspectDatabaseIdentity {
					s.database = integrity
				} else {
					s.database.QuickCheck = integrity.QuickCheck
					s.database.QuickViolationCount = integrity.QuickViolationCount
					s.database.ForeignKeyViolationCount = integrity.ForeignKeyViolationCount
				}
			}
		} else {
			s.databaseIdentityErr = errCapabilityUnavailable
			s.databaseIntegrityErr = errCapabilityUnavailable
		}
	}
	if selectedCategory(CategoryScheduler) || selectedCategory(CategoryImports) {
		if s.deps.Metrics != nil {
			s.metrics, s.metricsErr = inspectWithTimeout(ctx, timeoutFor(s.req.Profile, TimeoutMetricsOrManifest, s.deps.Features.Timeouts), func(child context.Context) (metrics.Window, error) {
				return s.deps.Metrics.Read(child, s.now.Add(-s.req.Since))
			})
		} else {
			s.metricsErr = errCapabilityUnavailable
		}
	}
	needPipeline := false
	needProvenance := false
	for _, entry := range Registry() {
		if !selected(entry.ID) {
			continue
		}
		if strings.Contains(string(entry.ID), ".partition") || strings.Contains(string(entry.ID), ".pending_age") {
			needPipeline = true
		}
		if strings.Contains(string(entry.ID), ".provenance") {
			needProvenance = true
		}
	}
	needLocal := selected(CheckDurabilityMediaLocalCoverage)
	needMedia := selected(CheckDurabilityMediaRemote) || selected(CheckDurabilityMediaRemoteOnly)
	// Store snapshot inspections are intentionally sequential. AuditReadSnapshot
	// owns one read transaction/connection; sharing it across concurrent queries
	// would add unsafe contention without improving the <=4 local-work ceiling.
	if needPipeline || needProvenance || needLocal || needMedia {
		if s.deps.Store == nil {
			s.pipelineErr, s.provenanceErr, s.localErr, s.mediaErr = errCapabilityUnavailable, errCapabilityUnavailable, errCapabilityUnavailable, errCapabilityUnavailable
		} else {
			if needPipeline {
				s.pipeline, s.pipelineErr = inspectWithTimeout(ctx, timeoutFor(s.req.Profile, TimeoutLocalQuery, s.deps.Features.Timeouts), s.deps.Store.Pipeline)
			}
			if needProvenance {
				list, err := inspectWithTimeout(ctx, timeoutFor(s.req.Profile, TimeoutLocalQuery, s.deps.Features.Timeouts), s.deps.Store.Provenance)
				s.provenanceErr = err
				for _, item := range list {
					s.provenance[item.CheckID] = item
				}
			}
			if needLocal {
				s.local, s.localErr = inspectWithTimeout(ctx, timeoutFor(s.req.Profile, TimeoutLocalQuery, s.deps.Features.Timeouts), s.deps.Store.MediaLocal)
			}
			if needMedia {
				s.media, s.mediaErr = inspectWithTimeout(ctx, timeoutFor(s.req.Profile, TimeoutLocalQuery, s.deps.Features.Timeouts), s.deps.Store.ArchivedMedia)
			}
		}
	}
	needOKFFull := selected(CheckDurabilityOKFValidation)
	needOKFFast := selected(CheckDurabilityOKFFreshness)
	if needOKFFull || needOKFFast {
		if s.deps.OKF == nil {
			s.okfFastErr, s.okfFullErr = errCapabilityUnavailable, errCapabilityUnavailable
		} else if needOKFFull {
			s.okfFull, s.okfFullErr = inspectWithTimeout(ctx, timeoutFor(s.req.Profile, TimeoutSQLiteOrOKFIntegrity, s.deps.Features.Timeouts), func(child context.Context) (OKFInspection, error) { return s.deps.OKF.Inspect(child, true) })
			s.okfFast, s.okfFastErr = s.okfFull, s.okfFullErr
		} else {
			s.okfFast, s.okfFastErr = inspectWithTimeout(ctx, timeoutFor(s.req.Profile, TimeoutMetricsOrManifest, s.deps.Features.Timeouts), func(child context.Context) (OKFInspection, error) { return s.deps.OKF.Inspect(child, false) })
		}
	}
	if selected(CheckDurabilitySQLiteBackupAge) || selected(CheckDurabilitySQLiteRestore) {
		if s.deps.Archives != nil {
			if s.req.Profile == ProfileDeep && s.deep != nil {
				s.archives, s.archivesErr = s.deps.Archives.List(ctx)
			} else {
				s.archives, s.archivesErr = inspectWithTimeout(ctx, timeoutFor(s.req.Profile, TimeoutRemoteMetadata, s.deps.Features.Timeouts), s.deps.Archives.List)
			}
		} else {
			s.archivesErr = errCapabilityUnavailable
		}
	}
}

func inspectWithTimeout[T any](ctx context.Context, timeout time.Duration, inspect func(context.Context) (T, error)) (T, error) {
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return inspect(child)
}

func timeoutFor(profile Profile, class TimeoutClass, configured ...map[TimeoutClass]time.Duration) time.Duration {
	ceiling := defaultTimeoutFor(profile, class)
	if len(configured) > 0 {
		if value := configured[0][class]; value > 0 && value < ceiling {
			return value
		}
	}
	return ceiling
}

func defaultTimeoutFor(profile Profile, class TimeoutClass) time.Duration {
	switch class {
	case TimeoutLocalQuery:
		if profile == ProfileFast {
			return 5 * time.Second
		}
		return 30 * time.Second
	case TimeoutMetricsOrManifest:
		return 10 * time.Second
	case TimeoutSQLiteOrOKFIntegrity, TimeoutRemoteMetadata:
		return 2 * time.Minute
	case TimeoutUpstreamInventory:
		return 5 * time.Minute
	default:
		return 30 * time.Second
	}
}

func effectiveScope(req Request) Scope {
	filtered := len(req.Categories) > 0 || len(req.Sources) > 0 || len(req.CheckIDs) > 0
	declareExactChecks := len(req.CheckIDs) > 0 || (filtered && strings.TrimSpace(req.ExpectCommit) != "")
	s := Scope{Categories: []Category{}, Sources: []Source{}, CheckIDs: []CheckID{}, Filtered: filtered, WholeSystem: !filtered}
	if !filtered {
		s.Categories = []Category{CategoryBoundary, CategoryScheduler, CategoryImports, CategoryPipeline, CategoryDurability, CategorySemantic}
		s.Sources = append(s.Sources, allSources...)
		return s
	}
	for _, entry := range Registry() {
		if !scopeIncludes(req, entry) {
			continue
		}
		if !containsCategory(s.Categories, entry.Category) {
			s.Categories = append(s.Categories, entry.Category)
		}
		if entry.Source != "" && !containsSource(s.Sources, entry.Source) {
			s.Sources = append(s.Sources, entry.Source)
		}
		if declareExactChecks {
			s.CheckIDs = append(s.CheckIDs, entry.ID)
		}
	}
	return s
}
func scopeIncludes(req Request, e RegistryEntry) bool {
	if strings.TrimSpace(req.ExpectCommit) != "" && e.ID == CheckBoundaryRuntime {
		return true
	}
	if len(req.CheckIDs) > 0 && !containsCheck(req.CheckIDs, e.ID) {
		return false
	}
	if len(req.Categories) > 0 && !containsCategory(req.Categories, e.Category) {
		return false
	}
	if len(req.Sources) > 0 && e.Source != "" && !containsSource(req.Sources, e.Source) {
		return false
	}
	return true
}
func containsCheck(v []CheckID, x CheckID) bool {
	for _, i := range v {
		if i == x {
			return true
		}
	}
	return false
}
func containsCategory(v []Category, x Category) bool {
	for _, i := range v {
		if i == x {
			return true
		}
	}
	return false
}
func containsSource(v []Source, x Source) bool {
	for _, i := range v {
		if i == x {
			return true
		}
	}
	return false
}
func isRequired(e RegistryEntry, f Features, req Request) bool {
	switch e.RequiredWhen {
	case RequiredAlways:
		return true
	case RequiredScheduler:
		return f.SchedulerEnabled
	case RequiredSourceScheduler:
		return f.SchedulerEnabled && f.Sources[e.Source]
	case RequiredSource:
		return f.Sources[e.Source] || containsSource(req.SourceOverrides, e.Source)
	case RequiredStage:
		return f.Stages[stageForCheck(e.ID)]
	case RequiredMediaLocal:
		return f.MediaLocalEnabled
	case RequiredMediaRemote:
		return f.MediaRemoteEnabled
	case RequiredSQLiteBackup:
		return f.SQLiteBackupSchedulerEnabled || f.SQLiteBackupAuditRequired
	case RequiredOKF:
		return f.OKFEnabled
	default:
		return false
	}
}
func featureEnabled(e RegistryEntry, f Features, req Request) bool {
	if e.ID == CheckDurabilityMediaRemoteOnly {
		return f.MediaRemoteEnabled
	}
	if e.Source != "" && strings.HasPrefix(string(e.ID), "imports.") {
		return f.SchedulerEnabled && f.Sources[e.Source]
	}
	switch e.RequiredWhen {
	case RequiredScheduler:
		return f.SchedulerEnabled
	case RequiredSourceScheduler:
		return f.SchedulerEnabled && f.Sources[e.Source]
	case RequiredSource:
		return f.Sources[e.Source] || containsSource(req.SourceOverrides, e.Source)
	case RequiredStage:
		return f.Stages[stageForCheck(e.ID)]
	case RequiredMediaLocal:
		return f.MediaLocalEnabled
	case RequiredMediaRemote:
		return f.MediaRemoteEnabled
	case RequiredSQLiteBackup:
		return f.SQLiteBackupSchedulerEnabled || f.SQLiteBackupAuditRequired
	case RequiredOKF:
		return f.OKFEnabled
	}
	if e.ID == CheckDurabilitySQLiteBackupConfiguration {
		return f.SQLiteArchiveCapabilityConfigured
	}
	return true
}
func stageForCheck(id CheckID) PipelineStage {
	value := string(id)
	for _, stage := range allPipelineStages {
		if strings.Contains(value, "pipeline."+string(stage)+".") {
			return stage
		}
	}
	switch id {
	case CheckPipelineItemSummaryProvenance, CheckPipelineSourceSummaryProvenance:
		return PipelineSummary
	case CheckPipelineItemOCRProvenance:
		return PipelineOCR
	case CheckPipelineXMediaTranscriptProvenance:
		return PipelineTranscription
	}
	return ""
}
func skippedCheck(e RegistryEntry, reason SkipReason, now time.Time) Check {
	return Check{ID: e.ID, Category: e.Category, Status: StatusSkipped, Confidence: ConfidenceUnknown, Summary: fixedSummary(e.ID), ObservedAt: now, Evidence: Evidence{}, Remediation: fixedRemediation(e.ID), SkipReason: reason}
}
func unknownCheck(e RegistryEntry, code ErrorCode, now time.Time) Check {
	return Check{ID: e.ID, Category: e.Category, Status: StatusUnknown, Confidence: ConfidenceUnknown, Summary: fixedSummary(e.ID), ObservedAt: now, Evidence: Evidence{}, Remediation: fixedRemediation(e.ID), ErrorCode: code}
}
func baseCheck(e RegistryEntry, now time.Time, status Status, confidence Confidence, evidence Evidence) Check {
	check := Check{ID: e.ID, Category: e.Category, Status: status, Confidence: confidence, Summary: fixedSummary(e.ID), ObservedAt: now, Evidence: evidence, Remediation: fixedRemediation(e.ID)}
	warn, warnOK := evidence["warn_after_seconds"]
	fail, failOK := evidence["fail_after_seconds"]
	if warnOK && failOK {
		check.Threshold = &Threshold{WarnAfterSeconds: integerEvidence(warn), FailAfterSeconds: integerEvidence(fail)}
	}
	return check
}
func fixedSummary(id CheckID) string { return "Audit result for " + string(id) }
func fixedRemediation(id CheckID) string {
	switch id {
	case CheckDurabilitySQLiteBackupConfiguration:
		return "Enable scheduler.sqlite_archive.enabled or set audit.require.sqlite_backup when remote SQLite backups are required."
	default:
		return ""
	}
}
func integerEvidence(value any) int64 {
	switch number := value.(type) {
	case int:
		return int64(number)
	case int64:
		return number
	default:
		return 0
	}
}
func normalizeGitStatus(value string) string {
	switch value {
	case "clean":
		return "clean"
	case "dirty", "modified":
		return "dirty"
	default:
		return "unknown"
	}
}
func latestRun(runs []metrics.RunRecord) (metrics.RunRecord, bool) {
	if len(runs) == 0 {
		return metrics.RunRecord{}, false
	}
	values := append([]metrics.RunRecord{}, runs...)
	sort.Slice(values, func(i, j int) bool { return values[i].StartedAt.Before(values[j].StartedAt) })
	return values[len(values)-1], true
}
func seconds(d time.Duration) int64 {
	if d < 0 {
		return 0
	}
	return int64(d / time.Second)
}

func executeBoundary(ctx context.Context, s *runState, e RegistryEntry) Check {
	switch e.ID {
	case CheckBoundaryConfig:
		if !s.deps.Features.ConfigVerified {
			return unknownCheck(e, ErrorConfiguration, s.now)
		}
		return baseCheck(e, s.now, StatusPass, ConfidenceHigh, Evidence{"layout": s.deps.Features.Layout, "config_source": s.deps.Features.ConfigSource, "verified": true})
	case CheckBoundaryRuntime:
		r := s.deps.Runtime
		release := known(r.ReleaseVersion)
		commit := known(r.Commit)
		platform := known(r.Platform)
		matched := expectedCommitMatches(s.req.ExpectCommit, r.Commit)
		ev := Evidence{"release_known": release, "commit_known": commit, "platform_known": platform, "git_status": normalizeGitStatus(r.GitStatus), "expected_commit_matched": matched}
		if !matched {
			return baseCheck(e, s.now, StatusFail, ConfidenceHigh, ev)
		}
		if !release || !commit || !platform {
			return baseCheck(e, s.now, StatusUnknown, ConfidenceUnknown, ev)
		}
		if normalizeGitStatus(r.GitStatus) == "unknown" {
			return baseCheck(e, s.now, StatusUnknown, ConfidenceUnknown, ev)
		}
		if normalizeGitStatus(r.GitStatus) == "dirty" {
			return baseCheck(e, s.now, StatusWarn, ConfidenceHigh, ev)
		}
		return baseCheck(e, s.now, StatusPass, ConfidenceHigh, ev)
	case CheckBoundarySecurityBaseline:
		ev := Evidence{"baseline_epoch": s.deps.Runtime.SecurityBaselineEpoch, "minimum_epoch": 1}
		if s.deps.Runtime.SecurityBaselineID == "pre-v0.6.0" || s.deps.Runtime.SecurityBaselineID == "v0.6.0-security-pass" {
			ev["baseline_id"] = s.deps.Runtime.SecurityBaselineID
		}
		class := version.ClassifySecurityBaseline(s.deps.Runtime.SecurityBaselineID, s.deps.Runtime.SecurityBaselineEpoch)
		if class == "unknown" {
			return baseCheck(e, s.now, StatusUnknown, ConfidenceUnknown, ev)
		}
		if class == "legacy" {
			return baseCheck(e, s.now, StatusFail, ConfidenceHigh, ev)
		}
		return baseCheck(e, s.now, StatusPass, ConfidenceHigh, ev)
	case CheckBoundaryDatabase:
		if !s.deps.Features.DatabaseOpenedQueryOnly {
			return unknownCheck(e, ErrorDatabase, s.now)
		}
		return baseCheck(e, s.now, StatusPass, ConfidenceHigh, Evidence{"opened_query_only": true})
	}
	databaseErr := s.databaseIdentityErr
	if e.ID == CheckIntegritySQLiteQuickCheck || e.ID == CheckIntegrityForeignKeys {
		databaseErr = s.databaseIntegrityErr
	}
	if databaseErr != nil {
		return unknownCheck(e, ErrorDatabase, s.now)
	}
	switch e.ID {
	case CheckIntegritySchemaIdentity:
		ev := Evidence{"compatibility": s.database.SchemaCompatibility, "missing_table_count": s.database.MissingTableCount, "missing_column_count": s.database.MissingColumnCount}
		status := StatusPass
		if s.database.SchemaCompatibility == "incompatible" {
			status = StatusFail
		}
		return baseCheck(e, s.now, status, ConfidenceHigh, ev)
	case CheckIntegrityMigrationCompatibility:
		ev := Evidence{"user_version": s.database.UserVersion, "supported_version": s.database.SupportedVersion, "applied_count": s.database.AppliedCount, "compatibility": s.database.MigrationCompatibility}
		status := StatusPass
		if s.database.MigrationCompatibility == "incompatible" {
			status = StatusFail
		}
		return baseCheck(e, s.now, status, ConfidenceHigh, ev)
	case CheckIntegritySQLiteQuickCheck:
		status := StatusPass
		if s.database.QuickCheck != "ok" || s.database.QuickViolationCount > 0 {
			status = StatusFail
		}
		return baseCheck(e, s.now, status, ConfidenceHigh, Evidence{"result": s.database.QuickCheck, "violation_count": s.database.QuickViolationCount})
	case CheckIntegrityForeignKeys:
		status := StatusPass
		if s.database.ForeignKeyViolationCount > 0 {
			status = StatusFail
		}
		return baseCheck(e, s.now, status, ConfidenceHigh, Evidence{"violation_count": s.database.ForeignKeyViolationCount})
	}
	return unknownCheck(e, ErrorUnavailable, s.now)
}

func expectedCommitMatches(expected, observed string) bool {
	expected = strings.ToLower(strings.TrimSpace(expected))
	observed = strings.ToLower(strings.TrimSpace(observed))
	return expected == "" || (len(expected) <= len(observed) && strings.HasPrefix(observed, expected))
}
func known(value string) bool {
	return strings.TrimSpace(value) != "" && !strings.EqualFold(value, "unknown")
}
