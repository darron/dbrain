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
