package audit

import (
	"reflect"
	"testing"
)

func TestRegistryContainsExactClosedV2SetInOutputOrder(t *testing.T) {
	want := []CheckID{
		"boundary.config", "boundary.runtime", "boundary.security_baseline", "boundary.database",
		"integrity.schema_identity", "integrity.migration_compatibility", "integrity.sqlite_quick_check", "integrity.foreign_keys",
		"scheduler.latest_sync", "scheduler.stage_coverage", "scheduler.continuity", "metrics.window",
		"imports.apple_notes.poll", "imports.apple_notes.arrivals",
		"imports.safari_tabs.poll", "imports.safari_tabs.arrivals",
		"imports.x_bookmarks.poll", "imports.x_bookmarks.arrivals",
		"imports.github_stars.poll", "imports.github_stars.arrivals",
		"imports.youtube_liked.poll", "imports.youtube_liked.arrivals",
		"imports.youtube_watch_later.poll", "imports.youtube_watch_later.arrivals",
		"imports.feeds.poll", "imports.feeds.arrivals",
		"pipeline.hydration.partition", "pipeline.hydration.pending_age",
		"pipeline.extraction.partition", "pipeline.extraction.pending_age",
		"pipeline.summary.partition", "pipeline.summary.pending_age",
		"pipeline.transcription.partition", "pipeline.transcription.pending_age",
		"pipeline.ocr.partition", "pipeline.ocr.pending_age",
		"pipeline.item_summary.provenance", "pipeline.item_ocr.provenance", "pipeline.x_media_transcript.provenance", "pipeline.source_summary.provenance",
		"durability.media_local_coverage", "durability.media_remote",
		"durability.sqlite_backup_configuration", "durability.sqlite_backup_age",
		"durability.okf_freshness", "durability.okf_validation",
		"upstream.apple_notes.parity", "upstream.safari_tabs.parity", "upstream.x_bookmarks.parity",
		"upstream.github_stars.parity", "upstream.youtube_liked.parity", "upstream.youtube_watch_later.parity", "upstream.feeds.parity",
		"durability.media_remote_only", "durability.sqlite_restore",
		"semantic.current_readiness", "semantic.latest_attached_refresh", "semantic.stage_summary",
	}
	registry := Registry()
	if len(registry) != 58 {
		t.Fatalf("registry length = %d, want 58", len(registry))
	}
	got := make([]CheckID, len(registry))
	for i, entry := range registry {
		got[i] = entry.ID
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registry order mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestRegistryStandardMembershipIs49ApplicableAnd9ProfileExcluded(t *testing.T) {
	applicable := 0
	excluded := 0
	for _, entry := range Registry() {
		if entry.InProfile(ProfileStandard) {
			applicable++
		} else {
			excluded++
		}
	}
	if applicable != 49 || excluded != 9 {
		t.Fatalf("standard membership = %d applicable, %d excluded; want 49/9", applicable, excluded)
	}
}

func TestRegistryCategoryMappingAndEvidenceSchemasAreClosed(t *testing.T) {
	seen := map[CheckID]bool{}
	for _, entry := range Registry() {
		if seen[entry.ID] {
			t.Fatalf("duplicate registry ID %q", entry.ID)
		}
		seen[entry.ID] = true
		if !entry.Category.Valid() || !entry.Timeout.Valid() || !entry.RequiredWhen.Valid() {
			t.Fatalf("invalid registry metadata for %q: %#v", entry.ID, entry)
		}
		if len(entry.EvidenceFields) == 0 {
			t.Fatalf("missing evidence schema for %q", entry.ID)
		}
	}
}

func TestDeepDurabilityExecutorsAreExplicitAndProfileExclusive(t *testing.T) {
	for _, id := range []CheckID{CheckDurabilityMediaRemoteOnly, CheckDurabilitySQLiteRestore} {
		entry, ok := Lookup(id)
		if !ok || !entry.InProfile(ProfileDeep) || entry.InProfile(ProfileFast) || entry.InProfile(ProfileStandard) || !HasExecutor(id) {
			t.Fatalf("deep registry entry %s = %#v executor=%t", id, entry, HasExecutor(id))
		}
	}
}
