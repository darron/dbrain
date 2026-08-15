package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestAuthoritativeWriteObserverReportsCanceledLeaseWait(t *testing.T) {
	t.Parallel()

	st, err := OpenWithSemanticCacheOptions(filepath.Join(t.TempDir(), "brain.db"), t.TempDir(), OpenOptions{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var observed AuthoritativeWriteWaitEvent
	st.authoritativeWriteObserver = func(event AuthoritativeWriteWaitEvent) { observed = event }
	exclusive, err := st.semanticLockScope.AcquireMaintenanceExclusive(t.Context(), "test-observer")
	if err != nil {
		t.Fatalf("acquire exclusive lease: %v", err)
	}
	t.Cleanup(func() { _ = exclusive.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	_, err = st.UpsertSource(ctx, model.SourceCandidate{
		OriginalURL:   "https://example.com/observer",
		CanonicalURL:  "https://example.com/observer",
		NormalizedURL: "https://example.com/observer",
		SourceType:    "web", Domain: "example.com", SourceKey: "src:observer",
		NotePath: "sources/web/observer.md",
	})
	if err == nil {
		t.Fatal("expected authoritative write to time out behind exclusive lease")
	}
	if observed.Outcome != "canceled" || observed.Metadata != "upsert-source" || observed.Wait < 20*time.Millisecond {
		t.Fatalf("lease wait event = %+v", observed)
	}
}
