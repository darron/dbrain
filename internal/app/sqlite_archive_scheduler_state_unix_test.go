//go:build unix

package app

import (
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWriteScheduledSQLiteArchiveAttemptSyncsParentDirectory(t *testing.T) {
	cfg := openSchedulerTestConfig(t)
	calls := 0
	err := writeScheduledSQLiteArchiveAttemptWithDirSync(cfg, time.Now(), func(fd int) error {
		calls++
		return unix.Fsync(fd)
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("new marker directory sync calls = %d, want data parent plus marker directory", calls)
	}

	calls = 0
	err = writeScheduledSQLiteArchiveAttemptWithDirSync(cfg, time.Now().Add(time.Minute), func(fd int) error {
		calls++
		return unix.Fsync(fd)
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("existing marker directory sync calls = %d, want marker directory only", calls)
	}
}
