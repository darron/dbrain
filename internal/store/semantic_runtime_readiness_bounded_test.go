package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/semanticreadiness"
)

func TestSemanticRuntimeReadinessRejectsPlausibleDirtyCounterUndercounts(t *testing.T) {
	tests := []struct {
		name        string
		dirtyRows   int
		storedDirty int
	}{
		{name: "within identity cap", dirtyRows: 2, storedDirty: 1},
		{name: "beyond identity cap", dirtyRows: semanticreadiness.MaxDirtyParents + 1, storedDirty: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
			defer func() { _ = st.Close() }()
			if _, err := st.db.Exec(fmt.Sprintf(`
				WITH RECURSIVE n(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM n WHERE value<%d)
				INSERT INTO retrieval_parent_projections (
					parent_kind,parent_source_key,status,chunk_count,dirty_at,dirty_revision,projected_revision,updated_at
				)
				SELECT 'invalid',printf('bounded:%%04d',value),'pending',1,
					'2026-07-20T12:00:00Z',value,0,'2026-07-20T12:00:00Z' FROM n`, tc.dirtyRows)); err != nil {
				t.Fatal(err)
			}
			// pending_parent_count remains authoritative, so the understated
			// dirty count is superficially compatible with the total/status
			// counters and cannot be caught by arithmetic alone.
			if _, err := st.db.Exec(`UPDATE retrieval_state SET dirty_parent_count=? WHERE singleton=1`, tc.storedDirty); err != nil {
				t.Fatal(err)
			}

			snapshot, err := st.SemanticRuntimeReadinessSnapshotAt(context.Background(), readinessTestProfile(), 25_000,
				time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			if !snapshot.AggregateCountersCorrupt {
				t.Fatalf("dirty counter undercount was trusted: snapshot=%+v", snapshot)
			}
			if !snapshot.OldestDirtyAt.Equal(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)) {
				t.Fatalf("oldest dirty debt was skipped: %s", snapshot.OldestDirtyAt)
			}
			if tc.dirtyRows > semanticreadiness.MaxDirtyParents {
				if snapshot.PlanningError != "" {
					t.Fatalf("planned an identity after the bounded 501st-row probe: %s", snapshot.PlanningError)
				}
				if snapshot.EstimatedNotReadyChunks != semanticreadiness.MaxNotReadyChunks+1 {
					t.Fatalf("estimated_not_ready_chunks=%d", snapshot.EstimatedNotReadyChunks)
				}
			}
		})
	}
}

func TestSemanticRuntimeReadinessRejectsImpossibleProjectionCounterRelations(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedRetrievalSource(t, st, "source:counter-relations")
	if _, err := st.db.Exec(`UPDATE retrieval_state SET pending_parent_count=0,dirty_parent_count=0 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.SemanticRuntimeReadinessSnapshotAt(context.Background(), readinessTestProfile(), 25_000, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.AggregateCountersCorrupt {
		t.Fatalf("impossible projection counter partition was trusted: snapshot=%+v", snapshot)
	}
}

func TestSemanticRuntimeGenerationPresenceUsesRequiredBoundedIndex(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	profile := readinessTestProfile()
	profileID, _ := profile.ID()
	for _, status := range []RetrievalGenerationStatus{RetrievalGenerationBuilding, RetrievalGenerationStale, RetrievalGenerationError} {
		for copy := 0; copy < 2; copy++ {
			if err := st.PutRetrievalIndexGeneration(context.Background(), RetrievalIndexGenerationRow{
				GenerationID: fmt.Sprintf("%s-%d", status, copy), ProfileID: profileID,
				Backend: "exact", BackendVersion: "v1", Dimensions: profile.Dimensions,
				DistanceMetric: "cosine", BuildStatus: status,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	query := `EXPLAIN QUERY PLAN SELECT
		EXISTS(SELECT 1 FROM retrieval_index_generations INDEXED BY idx_retrieval_generations_profile_status WHERE profile_id=? AND build_status='building' LIMIT 1),
		EXISTS(SELECT 1 FROM retrieval_index_generations INDEXED BY idx_retrieval_generations_profile_status WHERE profile_id=? AND build_status='stale' LIMIT 1),
		EXISTS(SELECT 1 FROM retrieval_index_generations INDEXED BY idx_retrieval_generations_profile_status WHERE profile_id=? AND build_status='error' LIMIT 1)`
	rows, err := st.db.Query(query, profileID, profileID, profileID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	joined := strings.Join(details, "\n")
	if got := strings.Count(joined, "idx_retrieval_generations_profile_status"); got != 3 || strings.Contains(joined, "USE TEMP B-TREE") {
		t.Fatalf("generation presence query did not use three bounded indexed probes:\n%s", joined)
	}
	snapshot, err := st.SemanticRuntimeReadinessSnapshotAt(context.Background(), profile, 25_000, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.BuildingGenerations != 1 || snapshot.StaleGenerations != 1 || snapshot.ErrorGenerations != 1 {
		t.Fatalf("runtime generation presence changed decision inputs: snapshot=%+v", snapshot)
	}

	if _, err := st.db.Exec(`DROP INDEX idx_retrieval_generations_profile_status`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SemanticRuntimeReadinessSnapshotAt(context.Background(), profile, 25_000, time.Now()); err == nil || !strings.Contains(err.Error(), "no such index: idx_retrieval_generations_profile_status") {
		t.Fatalf("runtime generation admission did not fail closed without required index: %v", err)
	}
}

func TestSemanticRuntimeReadinessGenerationMetadataPlansStayBounded(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	profileID, err := readinessTestProfile().ID()
	if err != nil {
		t.Fatal(err)
	}
	queries := []struct {
		name        string
		query       string
		wantIndexes []string
	}{
		{
			name:        "generation row",
			query:       "EXPLAIN QUERY PLAN " + activeSemanticGenerationMetadataQuery,
			wantIndexes: []string{"sqlite_autoindex_retrieval_index_generations_1"},
		},
		{
			name:  "generation segment aggregate",
			query: "EXPLAIN QUERY PLAN " + activeSemanticGenerationSegmentsQuery,
			wantIndexes: []string{
				"sqlite_autoindex_retrieval_index_generations_1",
				"sqlite_autoindex_retrieval_generation_segments_1",
				"sqlite_autoindex_retrieval_index_segments_1",
			},
		},
	}
	for _, tc := range queries {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := st.db.Query(tc.query, profileID)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(details, "\n")
			for _, index := range tc.wantIndexes {
				if !strings.Contains(joined, index) {
					t.Fatalf("generation metadata query did not use %s:\n%s", index, joined)
				}
			}
			if strings.Contains(joined, "retrieval_index_segment_members") || strings.Contains(joined, "USE TEMP B-TREE") {
				t.Fatalf("generation metadata query violated bounded no-member/no-sort contract:\n%s", joined)
			}
		})
	}
}

func TestSemanticRuntimeDirtyObservationPlansStayIndexedAndSortFree(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	queries := []struct {
		name      string
		query     string
		args      []any
		wantIndex string
	}{
		{
			name:      "oldest dirty",
			query:     `EXPLAIN QUERY PLAN SELECT dirty_at FROM retrieval_parent_projections INDEXED BY idx_retrieval_parent_projections_dirty_age WHERE projected_revision<dirty_revision ORDER BY dirty_at LIMIT 1`,
			wantIndex: "idx_retrieval_parent_projections_dirty_age",
		},
		{
			name:      "identity sentinel",
			query:     `EXPLAIN QUERY PLAN SELECT dirty_revision,parent_kind,parent_source_key FROM retrieval_parent_projections INDEXED BY idx_retrieval_parent_projections_dirty_keyset WHERE projected_revision<dirty_revision ORDER BY dirty_revision,parent_kind,parent_source_key LIMIT ?`,
			args:      []any{semanticreadiness.MaxDirtyParents + 1},
			wantIndex: "idx_retrieval_parent_projections_dirty_keyset",
		},
	}
	for _, tc := range queries {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := st.db.Query(tc.query, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			joined := strings.Join(details, "\n")
			if !strings.Contains(joined, tc.wantIndex) || strings.Contains(joined, "USE TEMP B-TREE") {
				t.Fatalf("dirty observation query violated bounded indexed/no-sort contract:\n%s", joined)
			}
		})
	}
}
