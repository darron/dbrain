package audit

import (
	"strings"
	"testing"
	"time"
)

func TestEvidencePrivacyAllowlistAcceptsDeclaredContentFreeValues(t *testing.T) {
	e := Evidence{
		"population_count": 12, "checked_count": 10, "recent_population_count": 8,
		"recent_checked_count": 8, "older_population_count": 4, "older_checked_count": 2,
		"missing_count": 0, "size_mismatch_count": 0, "invalid_timestamp_count": 0,
		"sample_mode": "bounded_sample", "inventory_complete": false,
	}
	if err := ValidateEvidence(CheckDurabilityMediaRemote, e); err != nil {
		t.Fatalf("valid evidence rejected: %v", err)
	}
}

func TestEvidencePrivacyRejectsUnknownKeysAndContentStrings(t *testing.T) {
	bad := []string{
		"/Users/alice/brain.db", "https://example.com/private", "source:x:123", "token_secret_abc",
		"OCR: account number 123", "transcript of private call", "A private title", "provider error: body",
	}
	for _, value := range bad {
		name := strings.ReplaceAll(value, "/", "_")
		t.Run(name, func(t *testing.T) {
			if err := ValidateEvidence(CheckBoundaryRuntime, Evidence{"provider_error": value}); err == nil {
				t.Fatalf("accepted content-bearing key/value %q", value)
			}
			if err := ValidateEvidence(CheckBoundaryRuntime, Evidence{"git_status": value}); err == nil {
				t.Fatalf("accepted arbitrary string %q", value)
			}
		})
	}
	if err := ValidateEvidence(CheckDurabilityOKFFreshness, Evidence{"exported_at": time.Now().Format(time.RFC1123)}); err == nil {
		t.Fatal("accepted non-RFC3339 UTC timestamp")
	}
}

func TestSemanticEvidenceIsBoundedAndClosed(t *testing.T) {
	const profileID = "embedding-profile-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const generationID = "semantic-root-v1:0123456789abcdef0123456789abcdef"
	readiness := Evidence{
		"configured": true, "capability": "available", "backend": "ollama", "profile_id": profileID,
		"active_generation_id": generationID, "readiness": "ready", "dirty_parent_count": 0, "pending_parent_count": 0,
		"due_embedding_count": 0, "blocked_embedding_count": 0, "failed_embedding_count": 0, "indexed_vector_count": 8,
		"l0_vector_count": 2, "tombstone_count": 0, "segment_count": 1,
	}
	if err := ValidateEvidence("semantic.current_readiness", readiness); err != nil {
		t.Fatalf("valid semantic readiness rejected: %v", err)
	}
	latest := Evidence{
		"refresh_state": "succeeded", "started_at": "2026-07-14T01:02:03Z", "completed_at": "2026-07-14T01:02:08Z",
		"age_seconds": 12, "duration_seconds": 5, "projected_parent_count": 1, "embedded_chunk_count": 2,
		"flushed_vector_count": 2, "compacted_vector_count": 0, "verified_vector_count": 2, "successor_run_count": 0,
	}
	if err := ValidateEvidence("semantic.latest_attached_refresh", latest); err != nil {
		t.Fatalf("valid semantic latest refresh rejected: %v", err)
	}
	stages := Evidence{"stages": []map[string]any{
		{"stage": "projection", "status": "succeeded", "duration_seconds": 1},
		{"stage": "embedding", "status": "succeeded", "duration_seconds": 2},
		{"stage": "flush", "status": "succeeded", "duration_seconds": 0},
		{"stage": "compaction", "status": "succeeded", "duration_seconds": 0},
		{"stage": "verification", "status": "succeeded", "duration_seconds": 1},
		{"stage": "readiness", "status": "succeeded", "duration_seconds": 1},
	}}
	if err := ValidateEvidence("semantic.stage_summary", stages); err != nil {
		t.Fatalf("valid semantic stages rejected: %v", err)
	}
	for name, evidence := range []Evidence{
		{"profile_id": "/private/profile"},
		{"readiness": "free-form diagnosis"},
		{"segment_count": -1},
		{"stages": []map[string]any{{"stage": "projection", "status": "succeeded", "duration_seconds": -1}}},
	} {
		id := CheckID("semantic.current_readiness")
		if name == 3 {
			id = "semantic.stage_summary"
		}
		if err := ValidateEvidence(id, evidence); err == nil {
			t.Fatalf("semantic evidence case %d was accepted: %#v", name, evidence)
		}
	}
}

func TestSemanticGenerationEvidenceAcceptsOnlyCanonicalFactoryIDs(t *testing.T) {
	const canonical = "semantic-root-v1:0123456789abcdef0123456789abcdef"
	base := Evidence{"active_generation_id": canonical}
	if err := ValidateEvidence(CheckSemanticCurrentReadiness, base); err != nil {
		t.Fatalf("canonical generation ID rejected: %v", err)
	}
	for _, value := range []string{
		"generation-current",
		"semantic-root-v1:0123456789abcdef0123456789abcde",
		"semantic-root-v1:0123456789abcdef0123456789abcdeF",
		"checkpoint:private-run",
		"/private/generation",
	} {
		if err := ValidateEvidence(CheckSemanticCurrentReadiness, Evidence{"active_generation_id": value}); err == nil {
			t.Fatalf("non-canonical generation ID accepted: %q", value)
		}
	}
}

func TestSemanticIdentifierSeamUsesEvidencePrivacyBounds(t *testing.T) {
	for value, want := range map[SemanticIdentifier]bool{
		"nomic-embed-text-v1.5":  true,
		"root-20260714":          true,
		"/Users/alice/private":   false,
		"checkpoint:private-run": false,
		"":                       false,
	} {
		if got := value.Valid(); got != want {
			t.Fatalf("SemanticIdentifier(%q).Valid() = %t, want %t", value, got, want)
		}
	}
}

func TestSemanticProfileIdentifierAcceptsOnlyCanonicalProfileIDs(t *testing.T) {
	for value, want := range map[SemanticProfileIdentifier]bool{
		"embedding-profile-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": true,
		"embedding-profile-v1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA": false,
		"nomic-embed-text-v1.5":  false,
		"checkpoint:private-run": false,
		"/Users/alice/private":   false,
	} {
		if got := value.Valid(); got != want {
			t.Fatalf("SemanticProfileIdentifier(%q).Valid() = %t, want %t", value, got, want)
		}
	}
}

func FuzzEvidenceRejectsArbitraryStrings(f *testing.F) {
	for _, seed := range []string{"/tmp/x", "https://x", "secret", "title", "transcript"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		allowed := map[string]bool{"clean": true, "dirty": true, "unknown": true}
		err := ValidateEvidence(CheckBoundaryRuntime, Evidence{"git_status": value})
		if allowed[value] && err != nil {
			t.Fatalf("closed enum rejected: %v", err)
		}
		if !allowed[value] && err == nil {
			t.Fatalf("arbitrary string accepted: %q", value)
		}
	})
}
