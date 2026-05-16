package serviceauth

import (
	"net/http"
	"testing"
	"time"
)

func TestServiceAuthHeaderVerifies(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	header, err := SignHeader(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", now)
	if err != nil {
		t.Fatalf("SignHeader: %v", err)
	}

	if !VerifyHeader(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", header, now) {
		t.Fatal("expected signed header to verify")
	}
}

func TestServiceAuthHeaderRejectsWrongPathAndStaleTimestamp(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	header, err := SignHeader(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", now)
	if err != nil {
		t.Fatalf("SignHeader: %v", err)
	}

	if VerifyHeader(http.MethodGet, "/api/bootstrap", "test-secret", header, now) {
		t.Fatal("expected path mismatch to fail")
	}
	if VerifyHeader(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", header, now.Add(3*time.Minute)) {
		t.Fatal("expected stale header to fail")
	}
}
