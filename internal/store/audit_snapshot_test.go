package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestAuditReadSnapshotRetainsOneConsistentView(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	writer := openStoreAtPath(t, path)
	defer func() { _ = writer.Close() }()
	seedAuditSnapshotItem(t, writer, "x:snapshot-one")

	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer func() { _ = reader.Close() }()
	snapshot, err := reader.BeginAuditReadSnapshot(t.Context())
	if err != nil {
		t.Fatalf("BeginAuditReadSnapshot: %v", err)
	}
	defer func() { _ = snapshot.Close() }()

	first, err := snapshot.PipelinePartitions(t.Context())
	if err != nil {
		t.Fatalf("first PipelinePartitions: %v", err)
	}
	seedAuditSnapshotItem(t, writer, "x:snapshot-two")
	second, err := snapshot.PipelinePartitions(t.Context())
	if err != nil {
		t.Fatalf("second PipelinePartitions: %v", err)
	}

	if auditPipelineTotal(first.Hydration) != 1 || auditPipelineTotal(second.Hydration) != 1 {
		t.Fatalf("snapshot view changed: first=%+v second=%+v", first.Hydration, second.Hydration)
	}
}

func TestAuditReadSnapshotCancellationAndClose(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.BeginAuditReadSnapshot(canceled); err == nil {
		t.Fatal("expected canceled snapshot begin to fail")
	}
	bootstrap, bootstrapCancel := context.WithCancel(context.Background())
	bootstrapSnapshot, err := st.BeginAuditReadSnapshot(bootstrap)
	if err != nil {
		t.Fatalf("BeginAuditReadSnapshot bootstrap: %v", err)
	}
	bootstrapCancel()
	if _, err := bootstrapSnapshot.PipelinePartitions(context.Background()); err != nil {
		t.Fatalf("bootstrap cancellation killed opened snapshot: %v", err)
	}
	if err := bootstrapSnapshot.Close(); err != nil {
		t.Fatalf("close bootstrap snapshot: %v", err)
	}

	snapshot, err := st.BeginAuditReadSnapshot(t.Context())
	if err != nil {
		t.Fatalf("BeginAuditReadSnapshot: %v", err)
	}
	if _, err := snapshot.PipelinePartitions(canceled); err == nil {
		t.Fatal("expected canceled snapshot query to fail")
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := snapshot.PipelinePartitions(t.Context()); err == nil {
		t.Fatal("expected closed snapshot query to fail")
	}
	seedAuditSnapshotItem(t, st, "x:after-snapshot-close")
}

func TestAuditReadSnapshotRestoresWritableQueryOnlyState(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	if got := auditQueryOnlyState(t, st); got != 0 {
		t.Fatalf("writable store query_only before snapshot = %d, want 0", got)
	}
	snapshot, err := st.BeginAuditReadSnapshot(t.Context())
	if err != nil {
		t.Fatalf("BeginAuditReadSnapshot: %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := auditQueryOnlyState(t, st); got != 0 {
		t.Fatalf("writable store query_only after snapshot = %d, want 0", got)
	}
}

func TestAuditReadSnapshotRestoresReadOnlyQueryOnlyState(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	writer := openStoreAtPath(t, path)
	seedAuditSnapshotItem(t, writer, "x:readonly-query-only")
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if got := auditQueryOnlyState(t, reader); got != 1 {
		t.Fatalf("read-only store query_only before snapshot = %d, want 1", got)
	}
	snapshot, err := reader.BeginAuditReadSnapshot(t.Context())
	if err != nil {
		t.Fatalf("BeginAuditReadSnapshot: %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := auditQueryOnlyState(t, reader); got != 1 {
		t.Fatalf("read-only store query_only after snapshot = %d, want 1", got)
	}
}

func TestAuditReadSnapshotMediaEvidenceIsReadOnlyAndPreservesInvalidTimestamps(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.db.ExecContext(t.Context(), `INSERT INTO media_assets
		(remote_url, byte_size, download_status, archive_key, archive_status, archived_at, updated_at)
		VALUES ('https://example.invalid/orphan', 7, 'downloaded', '', '', '', ''),
		('https://example.invalid/archive-valid', 11, 'downloaded', 'media/a', 'archived', '2026-07-14T12:00:00Z', ''),
		('https://example.invalid/archive-invalid', 13, 'downloaded', 'media/b', 'archived', 'not-a-time', '')`); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.BeginAuditReadSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	local, err := snapshot.MediaLocalEvidence(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if local.OrphanCount != 3 {
		t.Fatalf("orphan count = %d, want 3", local.OrphanCount)
	}
	records, err := snapshot.ArchivedMediaRecords(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || !records[0].ArchivedAtValid || records[1].ArchivedAtValid {
		t.Fatalf("records = %#v", records)
	}
}

func auditQueryOnlyState(t *testing.T, st *Store) int {
	t.Helper()
	var state int
	if err := st.db.QueryRowContext(t.Context(), `PRAGMA query_only`).Scan(&state); err != nil {
		t.Fatalf("read PRAGMA query_only: %v", err)
	}
	return state
}

func seedAuditSnapshotItem(t *testing.T, st *Store, sourceKey string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := st.UpsertItem(t.Context(), model.Item{
		SourceKey: sourceKey, SourceType: "x_bookmark", ExternalID: sourceKey,
		CanonicalURL: "https://x.com/example/status/" + sourceKey,
		Title:        sourceKey, ContentHash: sourceKey, LinksJSON: "[]",
		NotePath: "items/x/snapshot.md", RawJSON: `{}`,
		ImportedAt: now, UpdatedAt: now, LastSeenAt: now,
	}); err != nil {
		t.Fatalf("seed audit snapshot item: %v", err)
	}
}

func auditPipelineTotal(rows []PipelineStageRow) int {
	for _, row := range rows {
		if row.Kind == pipelineKindAll {
			return row.Total
		}
	}
	return 0
}
