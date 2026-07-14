package audit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type progressMediaInspector struct {
	mu         sync.Mutex
	active     int
	maxActive  int
	failureKey string
}

func (i *progressMediaInspector) HeadObject(ctx context.Context, key string) (ObjectMetadata, error) {
	i.mu.Lock()
	i.active++
	if i.active > i.maxActive {
		i.maxActive = i.active
	}
	i.mu.Unlock()
	defer func() {
		i.mu.Lock()
		i.active--
		i.mu.Unlock()
	}()
	if key == i.failureKey {
		return ObjectMetadata{}, errors.New("head failed")
	}
	select {
	case <-time.After(2 * time.Millisecond):
		return ObjectMetadata{Exists: true, SizeBytes: 10}, nil
	case <-ctx.Done():
		return ObjectMetadata{}, ctx.Err()
	}
}

func TestMediaRemoteReportsActualPartialProgressAndCapsConcurrency(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	records := make([]ArchivedMediaRecord, 0, 20)
	for index := 0; index < 20; index++ {
		records = append(records, ArchivedMediaRecord{Key: fmt.Sprintf("key-%02d", index), SizeBytes: 10, ArchivedAt: now.Add(-time.Duration(index) * time.Hour), ArchivedAtValid: true})
	}
	inspector := &progressMediaInspector{failureKey: "key-00"}
	state := &runState{now: now, req: Request{Since: 8 * time.Hour}, deps: Dependencies{Features: Features{MediaProvider: "test"}, Media: inspector}, media: records}
	entry, _ := Lookup(CheckDurabilityMediaRemote)
	check := executeMediaRemote(t.Context(), state, entry)
	if check.Status != StatusUnknown || check.Evidence["inventory_complete"] != false {
		t.Fatalf("check = %#v", check)
	}
	checked := check.Evidence["checked_count"].(int)
	recent := check.Evidence["recent_checked_count"].(int)
	older := check.Evidence["older_checked_count"].(int)
	if checked != recent+older || checked >= len(records) {
		t.Fatalf("partial counts checked=%d recent=%d older=%d population=%d", checked, recent, older, len(records))
	}
	if inspector.maxActive > 8 {
		t.Fatalf("max concurrency = %d", inspector.maxActive)
	}
}

func TestBackupAgeIgnoresInvalidZeroAndFutureObjects(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	state := &runState{now: now, deps: Dependencies{Features: Features{SQLiteBackupSchedulerEnabled: true, SQLiteProviderConfigured: true, SQLiteCredentialConfigured: true}}, archives: SQLiteArchiveListing{Complete: true, Objects: []ArchiveObject{
		{Key: "archive/db/not-an-archive", SizeBytes: 10, LastModified: now.Add(-time.Minute)},
		{Key: "archive/db/brain-zero.db.gz", ValidKey: true, SizeBytes: 0, LastModified: now.Add(-time.Minute)},
		{Key: "archive/db/brain-future.db.gz", ValidKey: true, SizeBytes: 10, LastModified: now.Add(time.Minute)},
	}}}
	entry, _ := Lookup(CheckDurabilitySQLiteBackupAge)
	check := executeBackupAge(state, entry)
	if check.Status != StatusFail || check.Evidence["archive_count"] != 0 {
		t.Fatalf("check = %#v", check)
	}
}
