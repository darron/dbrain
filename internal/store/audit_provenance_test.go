package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestAuditReadSnapshotProvenanceUsesMigrationCutoverForAllChecks(t *testing.T) {
	t.Parallel()

	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	cutover := time.Date(2026, 7, 13, 18, 0, 0, 0, time.UTC)
	if _, err := st.db.Exec(`UPDATE schema_migrations SET applied_at = ? WHERE name = 'audit_provenance_v1'`, cutover.Format(time.RFC3339)); err != nil {
		t.Fatalf("set provenance cutover: %v", err)
	}

	seedAuditItemProvenance(t, st, model.ItemEnrichmentRoleSummary, cutover, "raw_json")
	seedAuditItemProvenance(t, st, model.ItemEnrichmentRoleOCR, cutover, "raw_json")
	seedAuditItemProvenance(t, st, model.ItemEnrichmentRoleXMediaTranscript, cutover, "tool_version")
	seedAuditSourceSummaryProvenance(t, st, cutover, "summary_tool_version")

	snapshot, err := st.BeginAuditReadSnapshot(context.Background())
	if err != nil {
		t.Fatalf("BeginAuditReadSnapshot: %v", err)
	}
	defer func() { _ = snapshot.Close() }()
	got, err := snapshot.Provenance(context.Background())
	if err != nil {
		t.Fatalf("Provenance: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("provenance evidence count = %d, want 4: %+v", len(got), got)
	}

	wantMissingField := map[string]string{
		"pipeline.item_summary.provenance":       "raw_json",
		"pipeline.item_ocr.provenance":           "raw_json",
		"pipeline.x_media_transcript.provenance": "tool_version",
		"pipeline.source_summary.provenance":     "summary_tool_version",
	}
	seen := map[string]bool{}
	for _, evidence := range got {
		missingField, ok := wantMissingField[evidence.CheckID]
		if !ok {
			t.Fatalf("unexpected provenance check ID %q", evidence.CheckID)
		}
		seen[evidence.CheckID] = true
		if !evidence.CutoverKnown || !evidence.CutoverAt.Equal(cutover) {
			t.Fatalf("%s cutover = known %v at %s, want known at %s", evidence.CheckID, evidence.CutoverKnown, evidence.CutoverAt, cutover)
		}
		if evidence.SuccessfulCount != 3 || evidence.CompleteCount != 1 || evidence.LegacyMissingCount != 1 || evidence.PostCutoverMissingCount != 1 {
			t.Fatalf("%s counts = %+v", evidence.CheckID, evidence)
		}
		if evidence.MissingByField[missingField] != 2 {
			t.Fatalf("%s missing_by_field[%s] = %d, want 2: %+v", evidence.CheckID, missingField, evidence.MissingByField[missingField], evidence.MissingByField)
		}
	}
	for checkID := range wantMissingField {
		if !seen[checkID] {
			t.Fatalf("missing provenance evidence for %s", checkID)
		}
	}
}

func TestAuditReadSnapshotProvenanceTreatsMissingOrContradictoryCutoverAsUnknown(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, st *Store)
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, st *Store) {
				t.Helper()
				if _, err := st.db.Exec(`DELETE FROM schema_migrations WHERE name = 'audit_provenance_v1'`); err != nil {
					t.Fatalf("delete cutover migration: %v", err)
				}
			},
		},
		{
			name: "contradictory",
			mutate: func(t *testing.T, st *Store) {
				t.Helper()
				if _, err := st.db.Exec(`UPDATE schema_migrations SET name = 'wrong_audit_provenance' WHERE name = 'audit_provenance_v1'`); err != nil {
					t.Fatalf("rename cutover migration: %v", err)
				}
				if _, err := st.db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES(99, 'audit_provenance_v1', '2026-07-13T18:00:00Z')`); err != nil {
					t.Fatalf("insert contradictory cutover migration: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
			defer func() { _ = st.Close() }()
			tc.mutate(t, st)

			snapshot, err := st.BeginAuditReadSnapshot(context.Background())
			if err != nil {
				t.Fatalf("BeginAuditReadSnapshot: %v", err)
			}
			defer func() { _ = snapshot.Close() }()
			got, err := snapshot.Provenance(context.Background())
			if err != nil {
				t.Fatalf("Provenance: %v", err)
			}
			if len(got) != 4 {
				t.Fatalf("provenance evidence count = %d, want 4", len(got))
			}
			for _, evidence := range got {
				if evidence.CutoverKnown || !evidence.CutoverAt.IsZero() {
					t.Fatalf("%s guessed contradictory/missing cutover: %+v", evidence.CheckID, evidence)
				}
			}
		})
	}
}

func seedAuditItemProvenance(t *testing.T, st *Store, role string, cutover time.Time, missingField string) {
	t.Helper()
	for i, updatedAt := range []time.Time{cutover.Add(time.Minute), cutover.Add(-time.Minute), cutover.Add(time.Minute)} {
		result, err := st.UpsertItem(t.Context(), model.Item{
			SourceKey:    role + ":audit-provenance:" + string(rune('a'+i)),
			SourceType:   "x_bookmark",
			ExternalID:   role + "-audit-provenance-" + string(rune('a'+i)),
			CanonicalURL: "https://x.com/example/status/" + role + string(rune('a'+i)),
			Title:        "Audit provenance fixture",
			ContentHash:  role + "-hash-" + string(rune('a'+i)),
			NotePath:     "items/x/audit-provenance.md",
			RawJSON:      `{}`,
			UpdatedAt:    cutover,
			LastSeenAt:   cutover,
		})
		if err != nil {
			t.Fatalf("insert %s provenance item %d: %v", role, i, err)
		}
		values := map[string]string{
			"raw_json":       `{"ok":true}`,
			"model":          "test-model",
			"prompt_version": "prompt-v1",
			"tool":           "test-tool",
			"tool_version":   "tool-v1",
			"input_hash":     "sha256:test-input",
			"completed_at":   updatedAt.Format(time.RFC3339),
		}
		if i > 0 {
			values[missingField] = ""
		}
		if _, err := st.db.Exec(`
			INSERT INTO item_enrichments(
				item_id, role, status, text, raw_json, error, model, prompt_version,
				tool, tool_version, input_hash, completed_at, created_at, updated_at
			) VALUES(?, ?, 'ok', 'derived text', ?, '', ?, ?, ?, ?, ?, ?, ?, ?)`,
			result.ItemID, role, values["raw_json"], values["model"], values["prompt_version"],
			values["tool"], values["tool_version"], values["input_hash"], values["completed_at"],
			updatedAt.Format(time.RFC3339), updatedAt.Format(time.RFC3339),
		); err != nil {
			t.Fatalf("insert %s provenance row %d: %v", role, i, err)
		}
	}
}

func seedAuditSourceSummaryProvenance(t *testing.T, st *Store, cutover time.Time, missingField string) {
	t.Helper()
	for i, summarizedAt := range []time.Time{cutover.Add(time.Minute), cutover.Add(-time.Minute), cutover.Add(time.Minute)} {
		result, err := st.db.Exec(`
			INSERT INTO sources(source_key, canonical_url, normalized_url, source_type, domain, created_at, updated_at)
			VALUES(?, ?, ?, 'web', 'example.com', ?, ?)`,
			"src:audit-provenance:"+string(rune('a'+i)),
			"https://example.com/audit-provenance/"+string(rune('a'+i)),
			"https://example.com/audit-provenance/"+string(rune('a'+i)),
			summarizedAt.Format(time.RFC3339), summarizedAt.Format(time.RFC3339),
		)
		if err != nil {
			t.Fatalf("insert source provenance fixture %d: %v", i, err)
		}
		sourceID, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("source provenance fixture ID %d: %v", i, err)
		}
		values := map[string]string{
			"summary_json":           `{"ok":true}`,
			"summary_model":          "test-model",
			"summary_prompt_version": "prompt-v1",
			"summary_tool":           "test-tool",
			"summary_tool_version":   "tool-v1",
			"content_hash":           "sha256:source-input",
		}
		if i > 0 {
			values[missingField] = ""
		}
		if _, err := st.db.Exec(`
			INSERT INTO source_summary_versions(
				source_id, content_hash, summary_text, summary_json, summary_status, summary_error,
				summary_model, summary_prompt_version, summary_tool, summary_tool_version, summarized_at
			) VALUES(?, ?, 'derived summary', ?, 'ok', '', ?, ?, ?, ?, ?)`,
			sourceID, values["content_hash"], values["summary_json"], values["summary_model"],
			values["summary_prompt_version"], values["summary_tool"], values["summary_tool_version"], summarizedAt.Format(time.RFC3339),
		); err != nil {
			t.Fatalf("insert source summary provenance row %d: %v", i, err)
		}
	}
}
