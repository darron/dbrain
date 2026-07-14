package audit

import (
	"context"
	"time"
)

func executeDurability(ctx context.Context, s *runState, e RegistryEntry) Check {
	switch e.ID {
	case CheckDurabilityMediaLocalCoverage:
		if s.localErr != nil {
			return unknownCheck(e, ErrorRead, s.now)
		}
		status := StatusPass
		if s.local.UncoveredPrunedCount > 0 {
			status = StatusFail
		} else if s.local.OrphanCount > 0 {
			status = StatusWarn
		}
		return baseCheck(e, s.now, status, ConfidenceHigh, Evidence{"eligible_local_count": s.local.EligibleLocalCount, "uncovered_pruned_count": s.local.UncoveredPrunedCount, "orphan_count": s.local.OrphanCount})
	case CheckDurabilityMediaRemote:
		return executeMediaRemote(ctx, s, e)
	case CheckDurabilitySQLiteBackupConfiguration:
		state := sqliteConfigurationState(s.deps.Features)
		status := StatusPass
		if state == "configured_disabled" {
			status = StatusWarn
		}
		return baseCheck(e, s.now, status, ConfidenceHigh, Evidence{"capability_configured": s.deps.Features.SQLiteArchiveCapabilityConfigured, "scheduler_enabled": s.deps.Features.SQLiteBackupSchedulerEnabled, "audit_required": s.deps.Features.SQLiteBackupAuditRequired, "configuration_state": state})
	case CheckDurabilitySQLiteBackupAge:
		return executeBackupAge(s, e)
	case CheckDurabilityOKFFreshness:
		if s.okfFastErr != nil {
			return unknownCheck(e, ErrorManifest, s.now)
		}
		interval := s.deps.Features.SchedulerInterval
		if interval <= 0 {
			interval = time.Hour
		}
		warn, fail := 2*interval, 4*interval
		age := s.now.Sub(s.okfFast.ExportedAt)
		status := ClassifyAge(age, warn, fail)
		if !s.okfFast.ManifestValid {
			status = StatusFail
		}
		ev := Evidence{"manifest_valid": s.okfFast.ManifestValid, "age_seconds": seconds(age), "warn_after_seconds": seconds(warn), "fail_after_seconds": seconds(fail)}
		if !s.okfFast.ExportedAt.IsZero() {
			ev["exported_at"] = s.okfFast.ExportedAt.UTC().Format(time.RFC3339)
		}
		return baseCheck(e, s.now, status, ConfidenceHigh, ev)
	case CheckDurabilityOKFValidation:
		if s.okfFullErr != nil {
			return unknownCheck(e, ErrorManifest, s.now)
		}
		status := StatusPass
		if !s.okfFull.ManifestValid || s.okfFull.BrokenLinkCount > 0 || s.okfFull.ValidationErrorCount > 0 {
			status = StatusFail
		} else if !s.okfFull.TraversalComplete {
			status = StatusUnknown
		}
		confidence := ConfidenceHigh
		if status == StatusUnknown {
			confidence = ConfidenceUnknown
		}
		return baseCheck(e, s.now, status, confidence, Evidence{"manifest_valid": s.okfFull.ManifestValid, "document_count": s.okfFull.DocumentCount, "broken_link_count": s.okfFull.BrokenLinkCount, "validation_error_count": s.okfFull.ValidationErrorCount, "traversal_complete": s.okfFull.TraversalComplete})
	}
	return unknownCheck(e, ErrorUnavailable, s.now)
}
func executeMediaRemote(ctx context.Context, s *runState, e RegistryEntry) Check {
	if s.mediaErr != nil || s.deps.Media == nil {
		return unknownCheck(e, ErrorUnavailable, s.now)
	}
	provider := s.deps.Features.MediaProvider
	if provider == "" {
		provider = "configured"
	}
	sample := SelectMediaSample(s.media, s.req.Since, s.now, provider)
	missing, mismatch, invalid, checked := 0, 0, sample.InvalidCount, 0
	for _, record := range sample.Records {
		metadata, err := s.deps.Media.HeadObject(ctx, record.Key)
		if err != nil {
			return baseCheck(e, s.now, StatusUnknown, ConfidenceUnknown, mediaRemoteEvidence(sample, checked, missing, mismatch, invalid, false))
		}
		checked++
		if !metadata.Exists {
			missing++
		} else if metadata.SizeBytes != record.SizeBytes {
			mismatch++
		}
	}
	status := StatusPass
	if missing > 0 || mismatch > 0 || invalid > 0 {
		status = StatusFail
	}
	return baseCheck(e, s.now, status, sample.Confidence, mediaRemoteEvidence(sample, checked, missing, mismatch, invalid, true))
}
func mediaRemoteEvidence(sample MediaSample, checked, missing, mismatch, invalid int, complete bool) Evidence {
	return Evidence{"population_count": sample.RecentPopulation + sample.OlderPopulation + sample.InvalidCount, "checked_count": checked, "recent_population_count": sample.RecentPopulation, "recent_checked_count": sample.RecentChecked, "older_population_count": sample.OlderPopulation, "older_checked_count": sample.OlderChecked, "missing_count": missing, "size_mismatch_count": mismatch, "invalid_timestamp_count": invalid, "sample_mode": sample.Mode, "inventory_complete": complete}
}
func sqliteConfigurationState(f Features) string {
	switch {
	case f.SQLiteResolutionError:
		return "resolution_error"
	case (f.SQLiteBackupSchedulerEnabled || f.SQLiteBackupAuditRequired) && !f.SQLiteProviderConfigured:
		return "required_missing_provider"
	case (f.SQLiteBackupSchedulerEnabled || f.SQLiteBackupAuditRequired) && !f.SQLiteCredentialConfigured:
		return "required_missing_credential"
	case f.SQLiteBackupSchedulerEnabled || f.SQLiteBackupAuditRequired:
		return "required_ready"
	case f.SQLiteArchiveCapabilityConfigured:
		return "configured_disabled"
	default:
		return "not_configured"
	}
}
func executeBackupAge(s *runState, e RegistryEntry) Check {
	state := sqliteConfigurationState(s.deps.Features)
	ev := Evidence{"configuration_state": state, "archive_count": 0, "latest_age_seconds": 0, "latest_size_bytes": 0, "warn_after_seconds": seconds(DefaultBackupWarn), "fail_after_seconds": seconds(DefaultBackupFail), "listing_complete": false}
	if state == "required_missing_provider" || state == "required_missing_credential" {
		return baseCheck(e, s.now, StatusFail, ConfidenceHigh, ev)
	}
	if state == "resolution_error" || s.archivesErr != nil {
		return baseCheck(e, s.now, StatusUnknown, ConfidenceUnknown, ev)
	}
	ev["listing_complete"] = s.archives.Complete
	ev["archive_count"] = len(s.archives.Objects)
	if !s.archives.Complete {
		return baseCheck(e, s.now, StatusUnknown, ConfidenceUnknown, ev)
	}
	if len(s.archives.Objects) == 0 {
		return baseCheck(e, s.now, StatusFail, ConfidenceHigh, ev)
	}
	latest := s.archives.Objects[0]
	for _, object := range s.archives.Objects[1:] {
		if object.LastModified.After(latest.LastModified) {
			latest = object
		}
	}
	age := s.now.Sub(latest.LastModified)
	ev["latest_age_seconds"] = seconds(age)
	ev["latest_size_bytes"] = latest.SizeBytes
	return baseCheck(e, s.now, ClassifyAge(age, DefaultBackupWarn, DefaultBackupFail), ConfidenceHigh, ev)
}
