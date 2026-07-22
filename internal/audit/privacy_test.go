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
