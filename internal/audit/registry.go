package audit

type TimeoutClass string

const (
	TimeoutBootstrap            TimeoutClass = "bootstrap"
	TimeoutLocalQuery           TimeoutClass = "local_query"
	TimeoutMetricsOrManifest    TimeoutClass = "metrics_or_manifest"
	TimeoutSQLiteOrOKFIntegrity TimeoutClass = "sqlite_or_okf_integrity"
	TimeoutRemoteMetadata       TimeoutClass = "remote_metadata"
	TimeoutUpstreamInventory    TimeoutClass = "upstream_inventory"
	TimeoutDeepStream           TimeoutClass = "deep_stream"
)

func (t TimeoutClass) Valid() bool {
	switch t {
	case TimeoutBootstrap, TimeoutLocalQuery, TimeoutMetricsOrManifest, TimeoutSQLiteOrOKFIntegrity, TimeoutRemoteMetadata, TimeoutUpstreamInventory, TimeoutDeepStream:
		return true
	default:
		return false
	}
}

type RequiredCondition string

const (
	RequiredAlways          RequiredCondition = "always"
	RequiredNever           RequiredCondition = "never"
	RequiredScheduler       RequiredCondition = "scheduler_enabled"
	RequiredSourceScheduler RequiredCondition = "source_and_scheduler_enabled"
	RequiredSource          RequiredCondition = "source_enabled_or_explicit"
	RequiredStage           RequiredCondition = "stage_enabled"
	RequiredMediaLocal      RequiredCondition = "media_local_enabled"
	RequiredMediaRemote     RequiredCondition = "media_remote_enabled"
	RequiredSQLiteBackup    RequiredCondition = "sqlite_backup_required"
	RequiredOKF             RequiredCondition = "okf_enabled"
	RequiredSemantic        RequiredCondition = "semantic_enabled_supported"
)

func (r RequiredCondition) Valid() bool {
	switch r {
	case RequiredAlways, RequiredNever, RequiredScheduler, RequiredSourceScheduler, RequiredSource, RequiredStage, RequiredMediaLocal, RequiredMediaRemote, RequiredSQLiteBackup, RequiredOKF, RequiredSemantic:
		return true
	default:
		return false
	}
}

type RegistryEntry struct {
	ID             CheckID
	Category       Category
	Source         Source
	Profiles       []Profile
	RequiredWhen   RequiredCondition
	Timeout        TimeoutClass
	EvidenceFields map[string]EvidenceKind
	index          int
}

func (e RegistryEntry) InProfile(profile Profile) bool {
	for _, candidate := range e.Profiles {
		if candidate == profile {
			return true
		}
	}
	return false
}

const (
	CheckBoundaryConfig                      CheckID = "boundary.config"
	CheckBoundaryRuntime                     CheckID = "boundary.runtime"
	CheckBoundarySecurityBaseline            CheckID = "boundary.security_baseline"
	CheckBoundaryDatabase                    CheckID = "boundary.database"
	CheckIntegritySchemaIdentity             CheckID = "integrity.schema_identity"
	CheckIntegrityMigrationCompatibility     CheckID = "integrity.migration_compatibility"
	CheckIntegritySQLiteQuickCheck           CheckID = "integrity.sqlite_quick_check"
	CheckIntegrityForeignKeys                CheckID = "integrity.foreign_keys"
	CheckSchedulerLatestSync                 CheckID = "scheduler.latest_sync"
	CheckSchedulerStageCoverage              CheckID = "scheduler.stage_coverage"
	CheckSchedulerContinuity                 CheckID = "scheduler.continuity"
	CheckMetricsWindow                       CheckID = "metrics.window"
	CheckImportsAppleNotesPoll               CheckID = "imports.apple_notes.poll"
	CheckImportsAppleNotesArrivals           CheckID = "imports.apple_notes.arrivals"
	CheckImportsSafariTabsPoll               CheckID = "imports.safari_tabs.poll"
	CheckImportsSafariTabsArrivals           CheckID = "imports.safari_tabs.arrivals"
	CheckImportsXBookmarksPoll               CheckID = "imports.x_bookmarks.poll"
	CheckImportsXBookmarksArrivals           CheckID = "imports.x_bookmarks.arrivals"
	CheckImportsGitHubStarsPoll              CheckID = "imports.github_stars.poll"
	CheckImportsGitHubStarsArrivals          CheckID = "imports.github_stars.arrivals"
	CheckImportsYouTubeLikedPoll             CheckID = "imports.youtube_liked.poll"
	CheckImportsYouTubeLikedArrivals         CheckID = "imports.youtube_liked.arrivals"
	CheckImportsYouTubeWatchLaterPoll        CheckID = "imports.youtube_watch_later.poll"
	CheckImportsYouTubeWatchLaterArrivals    CheckID = "imports.youtube_watch_later.arrivals"
	CheckImportsFeedsPoll                    CheckID = "imports.feeds.poll"
	CheckImportsFeedsArrivals                CheckID = "imports.feeds.arrivals"
	CheckPipelineHydrationPartition          CheckID = "pipeline.hydration.partition"
	CheckPipelineHydrationPendingAge         CheckID = "pipeline.hydration.pending_age"
	CheckPipelineExtractionPartition         CheckID = "pipeline.extraction.partition"
	CheckPipelineExtractionPendingAge        CheckID = "pipeline.extraction.pending_age"
	CheckPipelineSummaryPartition            CheckID = "pipeline.summary.partition"
	CheckPipelineSummaryPendingAge           CheckID = "pipeline.summary.pending_age"
	CheckPipelineTranscriptionPartition      CheckID = "pipeline.transcription.partition"
	CheckPipelineTranscriptionPendingAge     CheckID = "pipeline.transcription.pending_age"
	CheckPipelineOCRPartition                CheckID = "pipeline.ocr.partition"
	CheckPipelineOCRPendingAge               CheckID = "pipeline.ocr.pending_age"
	CheckPipelineItemSummaryProvenance       CheckID = "pipeline.item_summary.provenance"
	CheckPipelineItemOCRProvenance           CheckID = "pipeline.item_ocr.provenance"
	CheckPipelineXMediaTranscriptProvenance  CheckID = "pipeline.x_media_transcript.provenance"
	CheckPipelineSourceSummaryProvenance     CheckID = "pipeline.source_summary.provenance"
	CheckDurabilityMediaLocalCoverage        CheckID = "durability.media_local_coverage"
	CheckDurabilityMediaRemote               CheckID = "durability.media_remote"
	CheckDurabilitySQLiteBackupConfiguration CheckID = "durability.sqlite_backup_configuration"
	CheckDurabilitySQLiteBackupAge           CheckID = "durability.sqlite_backup_age"
	CheckDurabilityOKFFreshness              CheckID = "durability.okf_freshness"
	CheckDurabilityOKFValidation             CheckID = "durability.okf_validation"
	CheckUpstreamAppleNotesParity            CheckID = "upstream.apple_notes.parity"
	CheckUpstreamSafariTabsParity            CheckID = "upstream.safari_tabs.parity"
	CheckUpstreamXBookmarksParity            CheckID = "upstream.x_bookmarks.parity"
	CheckUpstreamGitHubStarsParity           CheckID = "upstream.github_stars.parity"
	CheckUpstreamYouTubeLikedParity          CheckID = "upstream.youtube_liked.parity"
	CheckUpstreamYouTubeWatchLaterParity     CheckID = "upstream.youtube_watch_later.parity"
	CheckUpstreamFeedsParity                 CheckID = "upstream.feeds.parity"
	CheckDurabilityMediaRemoteOnly           CheckID = "durability.media_remote_only"
	CheckDurabilitySQLiteRestore             CheckID = "durability.sqlite_restore"
	CheckSemanticCurrentReadiness            CheckID = "semantic.current_readiness"
	CheckSemanticLatestAttachedRefresh       CheckID = "semantic.latest_attached_refresh"
	CheckSemanticStageSummary                CheckID = "semantic.stage_summary"
)

var profilesAll = []Profile{ProfileFast, ProfileStandard, ProfileDeep}
var profilesStandardDeep = []Profile{ProfileStandard, ProfileDeep}
var profilesDeep = []Profile{ProfileDeep}

func fields(entries ...any) map[string]EvidenceKind {
	out := make(map[string]EvidenceKind, len(entries)/2)
	for i := 0; i < len(entries); i += 2 {
		out[entries[i].(string)] = entries[i+1].(EvidenceKind)
	}
	return out
}

var legacyRegistry = []RegistryEntry{
	{ID: CheckBoundaryConfig, Category: CategoryBoundary, Profiles: profilesAll, RequiredWhen: RequiredAlways, Timeout: TimeoutBootstrap, EvidenceFields: fields("layout", EvidenceEnum, "config_source", EvidenceEnum, "verified", EvidenceBoolean)},
	{ID: CheckBoundaryRuntime, Category: CategoryBoundary, Profiles: profilesAll, RequiredWhen: RequiredAlways, Timeout: TimeoutLocalQuery, EvidenceFields: fields("release_known", EvidenceBoolean, "commit_known", EvidenceBoolean, "platform_known", EvidenceBoolean, "git_status", EvidenceEnum, "expected_commit_matched", EvidenceBoolean)},
	{ID: CheckBoundarySecurityBaseline, Category: CategoryBoundary, Profiles: profilesAll, RequiredWhen: RequiredAlways, Timeout: TimeoutLocalQuery, EvidenceFields: fields("baseline_id", EvidenceEnum, "baseline_epoch", EvidenceInteger, "minimum_epoch", EvidenceInteger)},
	{ID: CheckBoundaryDatabase, Category: CategoryBoundary, Profiles: profilesAll, RequiredWhen: RequiredAlways, Timeout: TimeoutBootstrap, EvidenceFields: fields("opened_query_only", EvidenceBoolean)},
	{ID: CheckIntegritySchemaIdentity, Category: CategoryBoundary, Profiles: profilesAll, RequiredWhen: RequiredAlways, Timeout: TimeoutLocalQuery, EvidenceFields: fields("compatibility", EvidenceEnum, "missing_table_count", EvidenceInteger, "missing_column_count", EvidenceInteger)},
	{ID: CheckIntegrityMigrationCompatibility, Category: CategoryBoundary, Profiles: profilesAll, RequiredWhen: RequiredAlways, Timeout: TimeoutLocalQuery, EvidenceFields: fields("user_version", EvidenceInteger, "supported_version", EvidenceInteger, "applied_count", EvidenceInteger, "compatibility", EvidenceEnum)},
	{ID: CheckIntegritySQLiteQuickCheck, Category: CategoryBoundary, Profiles: profilesStandardDeep, RequiredWhen: RequiredAlways, Timeout: TimeoutSQLiteOrOKFIntegrity, EvidenceFields: fields("result", EvidenceEnum, "violation_count", EvidenceInteger)},
	{ID: CheckIntegrityForeignKeys, Category: CategoryBoundary, Profiles: profilesStandardDeep, RequiredWhen: RequiredAlways, Timeout: TimeoutSQLiteOrOKFIntegrity, EvidenceFields: fields("violation_count", EvidenceInteger)},
	{ID: CheckSchedulerLatestSync, Category: CategoryScheduler, Profiles: profilesAll, RequiredWhen: RequiredScheduler, Timeout: TimeoutMetricsOrManifest, EvidenceFields: fields("latest_attempt_at", EvidenceTimestamp, "latest_success_at", EvidenceTimestamp, "age_seconds", EvidenceInteger, "warn_after_seconds", EvidenceInteger, "fail_after_seconds", EvidenceInteger, "duration_allowance_seconds", EvidenceInteger, "duration_allowance_source", EvidenceEnum)},
	{ID: CheckSchedulerStageCoverage, Category: CategoryScheduler, Profiles: profilesAll, RequiredWhen: RequiredScheduler, Timeout: TimeoutMetricsOrManifest, EvidenceFields: fields("expected_stage_count", EvidenceInteger, "completed_stage_count", EvidenceInteger, "missing_stage_count", EvidenceInteger, "record_complete", EvidenceBoolean)},
	{ID: CheckSchedulerContinuity, Category: CategoryScheduler, Profiles: profilesStandardDeep, RequiredWhen: RequiredScheduler, Timeout: TimeoutMetricsOrManifest, EvidenceFields: fields("observed_attempt_count", EvidenceInteger, "gap_count", EvidenceInteger, "explained_gap_count", EvidenceInteger, "unexplained_gap_count", EvidenceInteger, "largest_gap_seconds", EvidenceInteger, "warn_after_seconds", EvidenceInteger, "fail_after_seconds", EvidenceInteger)},
	{ID: CheckMetricsWindow, Category: CategoryScheduler, Profiles: profilesAll, RequiredWhen: RequiredScheduler, Timeout: TimeoutMetricsOrManifest, EvidenceFields: fields("requested_seconds", EvidenceInteger, "covered_seconds", EvidenceInteger, "completed_attempt_count", EvidenceInteger, "latest_attempt_present", EvidenceBoolean, "latest_completed_present", EvidenceBoolean, "parse_error_count", EvidenceInteger)},
}

var registry []RegistryEntry

func init() {
	appendSource := func(source Source, poll, arrivals CheckID) {
		legacyRegistry = append(legacyRegistry,
			RegistryEntry{ID: poll, Category: CategoryImports, Source: source, Profiles: profilesAll, RequiredWhen: RequiredSourceScheduler, Timeout: TimeoutMetricsOrManifest, EvidenceFields: fields("attempted_at", EvidenceTimestamp, "succeeded_at", EvidenceTimestamp, "age_seconds", EvidenceInteger, "warn_after_seconds", EvidenceInteger, "fail_after_seconds", EvidenceInteger, "attempt_count", EvidenceInteger, "success_count", EvidenceInteger, "failure_count", EvidenceInteger)},
			RegistryEntry{ID: arrivals, Category: CategoryImports, Source: source, Profiles: profilesAll, RequiredWhen: RequiredNever, Timeout: TimeoutMetricsOrManifest, EvidenceFields: fields("quiet_seconds", EvidenceInteger, "daily", EvidenceDaily)},
		)
	}
	appendSource(SourceAppleNotes, CheckImportsAppleNotesPoll, CheckImportsAppleNotesArrivals)
	appendSource(SourceSafariTabs, CheckImportsSafariTabsPoll, CheckImportsSafariTabsArrivals)
	appendSource(SourceXBookmarks, CheckImportsXBookmarksPoll, CheckImportsXBookmarksArrivals)
	appendSource(SourceGitHubStars, CheckImportsGitHubStarsPoll, CheckImportsGitHubStarsArrivals)
	appendSource(SourceYouTubeLiked, CheckImportsYouTubeLikedPoll, CheckImportsYouTubeLikedArrivals)
	appendSource(SourceYouTubeWatchLater, CheckImportsYouTubeWatchLaterPoll, CheckImportsYouTubeWatchLaterArrivals)
	appendSource(SourceFeeds, CheckImportsFeedsPoll, CheckImportsFeedsArrivals)

	partitionFields := fields("total", EvidenceInteger, "current", EvidenceInteger, "pending", EvidenceInteger, "blocked", EvidenceInteger, "terminal", EvidenceInteger, "failed", EvidenceInteger, "unknown", EvidenceInteger, "partition_valid", EvidenceBoolean, "by_kind", EvidenceByKind)
	pendingFields := fields("pending_count", EvidenceInteger, "oldest_pending_age_seconds", EvidenceInteger, "warn_after_seconds", EvidenceInteger, "fail_after_seconds", EvidenceInteger)
	for _, pair := range [][2]CheckID{{CheckPipelineHydrationPartition, CheckPipelineHydrationPendingAge}, {CheckPipelineExtractionPartition, CheckPipelineExtractionPendingAge}, {CheckPipelineSummaryPartition, CheckPipelineSummaryPendingAge}, {CheckPipelineTranscriptionPartition, CheckPipelineTranscriptionPendingAge}, {CheckPipelineOCRPartition, CheckPipelineOCRPendingAge}} {
		legacyRegistry = append(legacyRegistry,
			RegistryEntry{ID: pair[0], Category: CategoryPipeline, Profiles: profilesAll, RequiredWhen: RequiredStage, Timeout: TimeoutLocalQuery, EvidenceFields: partitionFields},
			RegistryEntry{ID: pair[1], Category: CategoryPipeline, Profiles: profilesAll, RequiredWhen: RequiredStage, Timeout: TimeoutLocalQuery, EvidenceFields: pendingFields},
		)
	}
	provenanceFields := fields("successful_count", EvidenceInteger, "complete_count", EvidenceInteger, "legacy_missing_count", EvidenceInteger, "post_cutover_missing_count", EvidenceInteger, "cutover_at", EvidenceTimestamp, "missing_by_field", EvidenceMissingByField)
	for _, id := range []CheckID{CheckPipelineItemSummaryProvenance, CheckPipelineItemOCRProvenance, CheckPipelineXMediaTranscriptProvenance, CheckPipelineSourceSummaryProvenance} {
		legacyRegistry = append(legacyRegistry, RegistryEntry{ID: id, Category: CategoryPipeline, Profiles: profilesAll, RequiredWhen: RequiredStage, Timeout: TimeoutLocalQuery, EvidenceFields: provenanceFields})
	}
	legacyRegistry = append(legacyRegistry,
		RegistryEntry{ID: CheckDurabilityMediaLocalCoverage, Category: CategoryDurability, Profiles: profilesAll, RequiredWhen: RequiredMediaLocal, Timeout: TimeoutLocalQuery, EvidenceFields: fields("eligible_local_count", EvidenceInteger, "uncovered_pruned_count", EvidenceInteger, "orphan_count", EvidenceInteger)},
		RegistryEntry{ID: CheckDurabilityMediaRemote, Category: CategoryDurability, Profiles: profilesStandardDeep, RequiredWhen: RequiredMediaRemote, Timeout: TimeoutRemoteMetadata, EvidenceFields: fields("population_count", EvidenceInteger, "checked_count", EvidenceInteger, "recent_population_count", EvidenceInteger, "recent_checked_count", EvidenceInteger, "older_population_count", EvidenceInteger, "older_checked_count", EvidenceInteger, "missing_count", EvidenceInteger, "size_mismatch_count", EvidenceInteger, "invalid_timestamp_count", EvidenceInteger, "sample_mode", EvidenceEnum, "inventory_complete", EvidenceBoolean)},
		RegistryEntry{ID: CheckDurabilitySQLiteBackupConfiguration, Category: CategoryDurability, Profiles: profilesAll, RequiredWhen: RequiredNever, Timeout: TimeoutLocalQuery, EvidenceFields: fields("capability_configured", EvidenceBoolean, "scheduler_enabled", EvidenceBoolean, "audit_required", EvidenceBoolean, "configuration_state", EvidenceEnum)},
		RegistryEntry{ID: CheckDurabilitySQLiteBackupAge, Category: CategoryDurability, Profiles: profilesStandardDeep, RequiredWhen: RequiredSQLiteBackup, Timeout: TimeoutRemoteMetadata, EvidenceFields: fields("configuration_state", EvidenceEnum, "archive_count", EvidenceInteger, "latest_age_seconds", EvidenceInteger, "latest_size_bytes", EvidenceInteger, "warn_after_seconds", EvidenceInteger, "fail_after_seconds", EvidenceInteger, "listing_complete", EvidenceBoolean)},
		RegistryEntry{ID: CheckDurabilityOKFFreshness, Category: CategoryDurability, Profiles: profilesAll, RequiredWhen: RequiredOKF, Timeout: TimeoutMetricsOrManifest, EvidenceFields: fields("manifest_valid", EvidenceBoolean, "exported_at", EvidenceTimestamp, "age_seconds", EvidenceInteger, "warn_after_seconds", EvidenceInteger, "fail_after_seconds", EvidenceInteger)},
		RegistryEntry{ID: CheckDurabilityOKFValidation, Category: CategoryDurability, Profiles: profilesStandardDeep, RequiredWhen: RequiredOKF, Timeout: TimeoutSQLiteOrOKFIntegrity, EvidenceFields: fields("manifest_valid", EvidenceBoolean, "document_count", EvidenceInteger, "broken_link_count", EvidenceInteger, "validation_error_count", EvidenceInteger, "traversal_complete", EvidenceBoolean)},
	)
	parities := []struct {
		source Source
		id     CheckID
	}{{SourceAppleNotes, CheckUpstreamAppleNotesParity}, {SourceSafariTabs, CheckUpstreamSafariTabsParity}, {SourceXBookmarks, CheckUpstreamXBookmarksParity}, {SourceGitHubStars, CheckUpstreamGitHubStarsParity}, {SourceYouTubeLiked, CheckUpstreamYouTubeLikedParity}, {SourceYouTubeWatchLater, CheckUpstreamYouTubeWatchLaterParity}, {SourceFeeds, CheckUpstreamFeedsParity}}
	for _, item := range parities {
		legacyRegistry = append(legacyRegistry, RegistryEntry{ID: item.id, Category: CategoryImports, Source: item.source, Profiles: profilesDeep, RequiredWhen: RequiredSource, Timeout: TimeoutUpstreamInventory, EvidenceFields: fields("upstream_count", EvidenceInteger, "matched_local_count", EvidenceInteger, "missing_local_count", EvidenceInteger, "page_count", EvidenceInteger, "inventory_complete", EvidenceBoolean)})
	}
	legacyRegistry = append(legacyRegistry,
		RegistryEntry{ID: CheckDurabilityMediaRemoteOnly, Category: CategoryDurability, Profiles: profilesDeep, RequiredWhen: RequiredNever, Timeout: TimeoutRemoteMetadata, EvidenceFields: fields("remote_only_count", EvidenceInteger, "inventory_complete", EvidenceBoolean)},
		RegistryEntry{ID: CheckDurabilitySQLiteRestore, Category: CategoryDurability, Profiles: profilesDeep, RequiredWhen: RequiredSQLiteBackup, Timeout: TimeoutDeepStream, EvidenceFields: fields("compressed_bytes", EvidenceInteger, "decompressed_bytes", EvidenceInteger, "quick_check", EvidenceEnum, "foreign_key_violation_count", EvidenceInteger, "schema_compatibility", EvidenceEnum, "migration_compatibility", EvidenceEnum, "archive_authenticity", EvidenceEnum, "cleanup_complete", EvidenceBoolean)},
	)
	for i := range legacyRegistry {
		legacyRegistry[i].index = i
	}
	registry = cloneRegistryEntries(legacyRegistry)
	registry = append(registry,
		RegistryEntry{ID: CheckSemanticCurrentReadiness, Category: CategorySemantic, Profiles: profilesAll, RequiredWhen: RequiredSemantic, Timeout: TimeoutLocalQuery, EvidenceFields: fields("configured", EvidenceBoolean, "capability", EvidenceEnum, "backend", EvidenceEnum, "profile_id", EvidenceIdentifier, "active_generation_id", EvidenceIdentifier, "readiness", EvidenceEnum, "dirty_parent_count", EvidenceInteger, "pending_parent_count", EvidenceInteger, "due_embedding_count", EvidenceInteger, "blocked_embedding_count", EvidenceInteger, "failed_embedding_count", EvidenceInteger, "indexed_vector_count", EvidenceInteger, "l0_vector_count", EvidenceInteger, "tombstone_count", EvidenceInteger, "segment_count", EvidenceInteger)},
		RegistryEntry{ID: CheckSemanticLatestAttachedRefresh, Category: CategorySemantic, Profiles: profilesAll, RequiredWhen: RequiredSemantic, Timeout: TimeoutMetricsOrManifest, EvidenceFields: fields("refresh_state", EvidenceEnum, "started_at", EvidenceTimestamp, "completed_at", EvidenceTimestamp, "age_seconds", EvidenceInteger, "duration_seconds", EvidenceInteger, "failure_at", EvidenceTimestamp, "semantic_error_code", EvidenceEnum, "projected_parent_count", EvidenceInteger, "embedded_chunk_count", EvidenceInteger, "flushed_vector_count", EvidenceInteger, "compacted_vector_count", EvidenceInteger, "verified_vector_count", EvidenceInteger, "successor_run_count", EvidenceInteger)},
		RegistryEntry{ID: CheckSemanticStageSummary, Category: CategorySemantic, Profiles: profilesAll, RequiredWhen: RequiredNever, Timeout: TimeoutMetricsOrManifest, EvidenceFields: fields("stages", EvidenceSemanticStages)},
	)
	for i := range registry {
		registry[i].index = i
	}
}

func Registry() []RegistryEntry {
	return cloneRegistryEntries(registry)
}

// RegistryForSchema returns the exact registry valid for persisted reports of
// the supplied schema. V1 is frozen so old reports cannot acquire new checks.
func RegistryForSchema(schema string) ([]RegistryEntry, bool) {
	switch schema {
	case SchemaV1:
		return cloneRegistryEntries(legacyRegistry), true
	case SchemaV2:
		return Registry(), true
	default:
		return nil, false
	}
}

func cloneRegistryEntries(entries []RegistryEntry) []RegistryEntry {
	out := make([]RegistryEntry, len(entries))
	for index, entry := range entries {
		out[index] = entry
		out[index].Profiles = append([]Profile(nil), entry.Profiles...)
		out[index].EvidenceFields = make(map[string]EvidenceKind, len(entry.EvidenceFields))
		for key, kind := range entry.EvidenceFields {
			out[index].EvidenceFields[key] = kind
		}
	}
	return out
}

func Lookup(id CheckID) (RegistryEntry, bool) {
	return lookupRegistryEntry(registry, id)
}

func lookupRegistryEntry(entries []RegistryEntry, id CheckID) (RegistryEntry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return RegistryEntry{}, false
}
