//go:build windows

package app

import (
	"testing"
	"time"
)

func TestWindowsSQLiteArchiveSchedulerStateHandleRelativeRoundTrip(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	want := time.Date(2026, time.July, 14, 12, 0, 0, 123, time.UTC)
	if err := writeScheduledSQLiteArchiveAttempt(cfg, want); err != nil {
		t.Fatalf("writeScheduledSQLiteArchiveAttempt: %v", err)
	}
	got, err := readScheduledSQLiteArchiveAttempt(cfg)
	if err != nil {
		t.Fatalf("readScheduledSQLiteArchiveAttempt: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("attempt = %s, want %s", got, want)
	}
}
