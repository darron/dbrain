package audit

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/version"
)

var ErrDeepUnsupported = errors.New("deep audit requires Task 4 executors")
var errCapabilityUnavailable = errors.New("audit capability unavailable")

type runState struct {
	now                    time.Time
	req                    Request
	deps                   Dependencies
	database               DatabaseInspection
	databaseErr            error
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
}

type checkExecutor func(context.Context, *runState, RegistryEntry) Check

var executors map[CheckID]checkExecutor

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
	if req.Since <= 0 {
		req.Since = 7 * 24 * time.Hour
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
	report.AuditID = "audit_" + now.Format("20060102T150405Z")
	report.Scope = effectiveScope(req)
	state := &runState{now: now, req: req, deps: deps, provenance: map[CheckID]ProvenanceEvidence{}}
	state.load(ctx)
	for _, entry := range Registry() {
		if !scopeIncludes(req, entry) {
			continue
		}
		required := isRequired(entry, deps.Features)
		var check Check
		switch {
		case !entry.InProfile(req.Profile):
			check = skippedCheck(entry, SkipProfileExcluded, now)
		case !featureEnabled(entry, deps.Features):
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
		report.Checks = append(report.Checks, check)
	}
	report.CompletedAt = now
	report.Boundary = Boundary{Layout: deps.Features.Layout, ConfigVerified: deps.Features.ConfigVerified, DatabaseVerified: deps.Features.DatabaseOpenedQueryOnly, Version: deps.Runtime.ReleaseVersion, Commit: deps.Runtime.Commit, GitStatus: normalizeGitStatus(deps.Runtime.GitStatus), Platform: deps.Runtime.Platform, SecurityBaseline: deps.Runtime.SecurityBaselineID, SecurityBaselineEpoch: deps.Runtime.SecurityBaselineEpoch, SchemaVersion: state.database.UserVersion, SchemaCompatibility: state.database.SchemaCompatibility}
	FinalizeReport(&report)
	if err := ValidateReport(report); err != nil {
		return Report{}, fmt.Errorf("validate audit report: %w", err)
	}
	return report, nil
}

func (s *runState) load(ctx context.Context) {
	if s.deps.Database != nil {
		s.database, s.databaseErr = s.deps.Database.Inspect(ctx, s.req.Profile == ProfileStandard)
	} else {
		s.databaseErr = errCapabilityUnavailable
	}
	if s.deps.Metrics != nil {
		s.metrics, s.metricsErr = s.deps.Metrics.Read(ctx, s.now.Add(-s.req.Since))
	} else {
		s.metricsErr = errCapabilityUnavailable
	}
	if s.deps.Store != nil {
		s.pipeline, s.pipelineErr = s.deps.Store.Pipeline(ctx)
		list, err := s.deps.Store.Provenance(ctx)
		s.provenanceErr = err
		for _, item := range list {
			s.provenance[item.CheckID] = item
		}
		if s.deps.Features.MediaLocalEnabled {
			s.local, s.localErr = s.deps.Store.MediaLocal(ctx)
		}
		if s.deps.Features.MediaRemoteEnabled {
			s.media, s.mediaErr = s.deps.Store.ArchivedMedia(ctx)
		}
	} else {
		s.pipelineErr = errCapabilityUnavailable
		s.provenanceErr = errCapabilityUnavailable
		s.localErr = errCapabilityUnavailable
		s.mediaErr = errCapabilityUnavailable
	}
	if s.deps.Features.OKFEnabled && s.deps.OKF != nil {
		s.okfFast, s.okfFastErr = s.deps.OKF.Inspect(ctx, false)
		if s.req.Profile == ProfileStandard {
			s.okfFull, s.okfFullErr = s.deps.OKF.Inspect(ctx, true)
		}
	} else if s.deps.Features.OKFEnabled {
		s.okfFastErr = errCapabilityUnavailable
		s.okfFullErr = errCapabilityUnavailable
	}
	backupRequired := s.deps.Features.SQLiteBackupSchedulerEnabled || s.deps.Features.SQLiteBackupAuditRequired
	if backupRequired && s.deps.Archives != nil {
		s.archives, s.archivesErr = s.deps.Archives.List(ctx)
	} else if backupRequired {
		s.archivesErr = errCapabilityUnavailable
	}
}

func effectiveScope(req Request) Scope {
	filtered := len(req.Categories) > 0 || len(req.Sources) > 0 || len(req.CheckIDs) > 0
	s := Scope{Categories: []Category{}, Sources: []Source{}, CheckIDs: append([]CheckID{}, req.CheckIDs...), Filtered: filtered, WholeSystem: !filtered}
	if len(req.Categories) > 0 {
		s.Categories = append(s.Categories, req.Categories...)
	} else if !filtered {
		s.Categories = []Category{CategoryBoundary, CategoryScheduler, CategoryImports, CategoryPipeline, CategoryDurability}
	}
	if len(req.Sources) > 0 {
		s.Sources = append(s.Sources, req.Sources...)
	} else if !filtered {
		s.Sources = append(s.Sources, allSources...)
	}
	return s
}
func scopeIncludes(req Request, e RegistryEntry) bool {
	if len(req.CheckIDs) > 0 && !containsCheck(req.CheckIDs, e.ID) {
		return false
	}
	if len(req.Categories) > 0 && !containsCategory(req.Categories, e.Category) {
		return false
	}
	if len(req.Sources) > 0 && (e.Source == "" || !containsSource(req.Sources, e.Source)) {
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
func isRequired(e RegistryEntry, f Features) bool {
	switch e.RequiredWhen {
	case RequiredAlways:
		return true
	case RequiredScheduler:
		return f.SchedulerEnabled
	case RequiredSourceScheduler:
		return f.SchedulerEnabled && f.Sources[e.Source]
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
func featureEnabled(e RegistryEntry, f Features) bool {
	if e.Source != "" && strings.HasPrefix(string(e.ID), "imports.") {
		return f.SchedulerEnabled && f.Sources[e.Source]
	}
	switch e.RequiredWhen {
	case RequiredScheduler:
		return f.SchedulerEnabled
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
	return Check{ID: e.ID, Category: e.Category, Status: StatusSkipped, Confidence: ConfidenceUnknown, Summary: fixedSummary(e.ID), ObservedAt: now, Evidence: Evidence{}, SkipReason: reason}
}
func unknownCheck(e RegistryEntry, code ErrorCode, now time.Time) Check {
	return Check{ID: e.ID, Category: e.Category, Status: StatusUnknown, Confidence: ConfidenceUnknown, Summary: fixedSummary(e.ID), ObservedAt: now, Evidence: Evidence{}, ErrorCode: code}
}
func baseCheck(e RegistryEntry, now time.Time, status Status, confidence Confidence, evidence Evidence) Check {
	return Check{ID: e.ID, Category: e.Category, Status: status, Confidence: confidence, Summary: fixedSummary(e.ID), ObservedAt: now, Evidence: evidence}
}
func fixedSummary(id CheckID) string { return "Audit result for " + string(id) }
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
		matched := s.req.ExpectCommit == "" || s.req.ExpectCommit == r.Commit
		ev := Evidence{"release_known": release, "commit_known": commit, "platform_known": platform, "git_status": normalizeGitStatus(r.GitStatus), "expected_commit_matched": matched}
		if !matched {
			return baseCheck(e, s.now, StatusFail, ConfidenceHigh, ev)
		}
		if !release || !commit || !platform {
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
	if s.databaseErr != nil {
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
func known(value string) bool {
	return strings.TrimSpace(value) != "" && !strings.EqualFold(value, "unknown")
}
