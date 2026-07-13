package serviceauth

import (
	"net/http"
	"sync"
	"sync/atomic"
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

func TestServiceAuthReplayVerifierRejectsReplayAndAcceptsDistinctNonce(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	first, err := SignHeader(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", now)
	if err != nil {
		t.Fatalf("SignHeader first: %v", err)
	}
	second, err := SignHeader(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", now)
	if err != nil {
		t.Fatalf("SignHeader second: %v", err)
	}
	if first == second {
		t.Fatal("fresh signed headers reused a nonce")
	}

	var verifier ReplayVerifier
	if !verifier.VerifyAndConsume(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", first, now) {
		t.Fatal("expected first header use to succeed")
	}
	if verifier.VerifyAndConsume(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", first, now) {
		t.Fatal("expected identical replay to fail")
	}
	if !verifier.VerifyAndConsume(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", second, now) {
		t.Fatal("expected distinct nonce at the same timestamp to succeed")
	}
}

func TestServiceAuthReplayVerifierInvalidAttemptsDoNotConsumeNonce(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	wrongPathHeader, err := SignHeader(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", now)
	if err != nil {
		t.Fatalf("SignHeader wrong-path case: %v", err)
	}
	staleHeader, err := SignHeader(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", now)
	if err != nil {
		t.Fatalf("SignHeader stale case: %v", err)
	}

	var verifier ReplayVerifier
	if verifier.VerifyAndConsume(http.MethodGet, "/api/bootstrap", "test-secret", wrongPathHeader, now) {
		t.Fatal("wrong-path header succeeded")
	}
	if !verifier.VerifyAndConsume(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", wrongPathHeader, now) {
		t.Fatal("wrong-path attempt consumed a valid nonce")
	}
	if verifier.VerifyAndConsume(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", staleHeader, now.Add(3*time.Minute)) {
		t.Fatal("stale header succeeded")
	}
	if !verifier.VerifyAndConsume(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", staleHeader, now) {
		t.Fatal("stale attempt consumed a valid nonce")
	}
}

func TestServiceAuthReplayVerifierPurgesExpiredNoncesAfterValidVerification(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	first, err := SignHeader(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", now)
	if err != nil {
		t.Fatalf("SignHeader first: %v", err)
	}

	var verifier ReplayVerifier
	if !verifier.VerifyAndConsume(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", first, now) {
		t.Fatal("expected first header use to succeed")
	}

	later := now.Add(3 * time.Minute)
	second, err := SignHeader(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", later)
	if err != nil {
		t.Fatalf("SignHeader second: %v", err)
	}
	if !verifier.VerifyAndConsume(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", second, later) {
		t.Fatal("expected later valid header to succeed")
	}
	if len(verifier.seen) != 1 {
		t.Fatalf("seen nonce count = %d, want one live nonce", len(verifier.seen))
	}
}

func TestServiceAuthReplayVerifierAllowsOnlyOneConcurrentUse(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	header, err := SignHeader(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", now)
	if err != nil {
		t.Fatalf("SignHeader: %v", err)
	}

	var verifier ReplayVerifier
	start := make(chan struct{})
	var successes atomic.Int32
	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if verifier.VerifyAndConsume(http.MethodGet, "/api/doctor/full-disk-access", "test-secret", header, now) {
				successes.Add(1)
			}
		}()
	}
	close(start)
	workers.Wait()
	if got := successes.Load(); got != 1 {
		t.Fatalf("concurrent successes = %d, want exactly one", got)
	}
}
