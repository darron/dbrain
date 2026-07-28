package store

import (
	"context"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticreadiness"
)

func TestSemanticReadinessActiveGenerationMetadata(t *testing.T) {
	st, profile, generationID, _, snapshotRevision := seedCompletedSemanticGeneration(t)
	defer func() { _ = st.Close() }()

	for name, read := range semanticGenerationReadinessReaders(st) {
		t.Run(name, func(t *testing.T) {
			snapshot, err := read(context.Background(), profile)
			if err != nil {
				t.Fatal(err)
			}
			if !snapshot.ActiveGenerationValid ||
				snapshot.ActiveGenerationID != generationID ||
				snapshot.ActiveGenerationBackend != "usearch" ||
				snapshot.ActiveGenerationBackendVersion != "2.26.0" ||
				snapshot.ActiveGenerationDistanceMetric != "cosine" ||
				snapshot.ActiveGenerationDimensions != profile.Dimensions ||
				snapshot.ActiveSnapshotRevision != snapshotRevision {
				t.Fatalf("active generation metadata was not admitted: snapshot=%+v", snapshot)
			}
			if snapshot.ActiveGenerationProblem != "" {
				t.Fatalf("valid active generation has problem %q", snapshot.ActiveGenerationProblem)
			}
		})
	}
}

func TestSemanticRuntimeReadinessActiveGenerationMetadataCorruptionFailsClosed(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*testing.T, *Store, string, string, int)
		wantProblem string
	}{
		{
			name: "generation profile differs",
			mutate: func(t *testing.T, st *Store, generationID, _ string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_generations SET profile_id='wrong-profile' WHERE generation_id=?`, generationID)
			},
		},
		{
			name: "generation inactive",
			mutate: func(t *testing.T, st *Store, generationID, _ string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_generations SET active=0 WHERE generation_id=?`, generationID)
			},
		},
		{
			name:        "generation not completed",
			wantProblem: "active generation row is not completed",
			mutate: func(t *testing.T, st *Store, generationID, _ string, _ int) {
				corruptActiveSemanticGenerationStatus(t, st, generationID)
			},
		},
		{
			name: "generation backend differs from segment",
			mutate: func(t *testing.T, st *Store, _, segmentHash string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_segments SET backend='other' WHERE segment_hash=?`, segmentHash)
			},
		},
		{
			name: "generation backend is empty",
			mutate: func(t *testing.T, st *Store, generationID, _ string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_generations SET backend='' WHERE generation_id=?`, generationID)
			},
		},
		{
			name: "generation backend version differs from segment",
			mutate: func(t *testing.T, st *Store, _, segmentHash string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_segments SET backend_version='other' WHERE segment_hash=?`, segmentHash)
			},
		},
		{
			name: "generation backend version is empty",
			mutate: func(t *testing.T, st *Store, generationID, _ string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_generations SET backend_version='' WHERE generation_id=?`, generationID)
			},
		},
		{
			name: "generation metric differs from segment",
			mutate: func(t *testing.T, st *Store, _, segmentHash string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_segments SET distance_metric='dot' WHERE segment_hash=?`, segmentHash)
			},
		},
		{
			name: "generation metric is not cosine",
			mutate: func(t *testing.T, st *Store, generationID, _ string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_generations SET distance_metric='dot' WHERE generation_id=?`, generationID)
			},
		},
		{
			name: "generation dimensions differ from segment",
			mutate: func(t *testing.T, st *Store, _, segmentHash string, dimensions int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_segments SET dimensions=? WHERE segment_hash=?`, dimensions+1, segmentHash)
			},
		},
		{
			name: "generation dimensions differ from profile",
			mutate: func(t *testing.T, st *Store, generationID, _ string, dimensions int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_generations SET dimensions=? WHERE generation_id=?`, dimensions+1, generationID)
			},
		},
		{
			name: "generation indexed count differs from profile",
			mutate: func(t *testing.T, st *Store, generationID, _ string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_generations SET indexed_chunk_count=indexed_chunk_count+1 WHERE generation_id=?`, generationID)
			},
		},
		{
			name: "generation source manifest is empty",
			mutate: func(t *testing.T, st *Store, generationID, _ string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_generations SET source_manifest_hash='' WHERE generation_id=?`, generationID)
			},
		},
		{
			name: "generation relative path is empty",
			mutate: func(t *testing.T, st *Store, generationID, _ string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_generations SET relative_cache_path='' WHERE generation_id=?`, generationID)
			},
		},
		{
			name: "active snapshot revision is zero",
			mutate: func(t *testing.T, st *Store, _, _ string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_embedding_profiles SET active_snapshot_revision=0 WHERE active_generation_id!=''`)
			},
		},
		{
			name: "active snapshot revision exceeds latest revision",
			mutate: func(t *testing.T, st *Store, _, _ string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_embedding_profiles SET active_snapshot_revision=latest_revision+1 WHERE active_generation_id!=''`)
			},
		},
		{
			name: "generation segment relation is missing",
			mutate: func(t *testing.T, st *Store, generationID, _ string, _ int) {
				execSemanticGenerationCorruption(t, st, `DELETE FROM retrieval_generation_segments WHERE generation_id=?`, generationID)
			},
		},
		{
			name: "segment count sum differs from generation",
			mutate: func(t *testing.T, st *Store, _, segmentHash string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_segments SET indexed_chunk_count=indexed_chunk_count+1 WHERE segment_hash=?`, segmentHash)
			},
		},
		{
			name: "segment relative path is empty",
			mutate: func(t *testing.T, st *Store, _, segmentHash string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_segments SET relative_cache_path='' WHERE segment_hash=?`, segmentHash)
			},
		},
		{
			name: "segment membership hash is empty",
			mutate: func(t *testing.T, st *Store, _, segmentHash string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_segments SET membership_hash='' WHERE segment_hash=?`, segmentHash)
			},
		},
		{
			name: "segment payload hash is empty",
			mutate: func(t *testing.T, st *Store, _, segmentHash string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_segments SET payload_hash='' WHERE segment_hash=?`, segmentHash)
			},
		},
		{
			name: "segment manifest hash is empty",
			mutate: func(t *testing.T, st *Store, _, segmentHash string, _ int) {
				execSemanticGenerationCorruption(t, st, `UPDATE retrieval_index_segments SET manifest_hash='' WHERE segment_hash=?`, segmentHash)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, profile, generationID, segmentHash, _ := seedCompletedSemanticGeneration(t)
			defer func() { _ = st.Close() }()
			tc.mutate(t, st, generationID, segmentHash, profile.Dimensions)

			for name, read := range semanticGenerationReadinessReaders(st) {
				t.Run(name, func(t *testing.T) {
					snapshot, err := read(context.Background(), profile)
					if err != nil {
						t.Fatalf("metadata corruption escaped as SQL error: %v", err)
					}
					if snapshot.ActiveGenerationValid {
						t.Fatalf("metadata corruption was admitted: snapshot=%+v", snapshot)
					}
					if snapshot.ActiveGenerationProblem == "" {
						t.Fatalf("metadata corruption has no bounded diagnosis: snapshot=%+v", snapshot)
					}
					if tc.wantProblem != "" && snapshot.ActiveGenerationProblem != tc.wantProblem {
						t.Fatalf("active generation problem=%q want=%q", snapshot.ActiveGenerationProblem, tc.wantProblem)
					}
				})
			}
		})
	}
}

type semanticGenerationReadinessReader func(context.Context, embedding.Profile) (semanticreadiness.Snapshot, error)

func semanticGenerationReadinessReaders(st *Store) map[string]semanticGenerationReadinessReader {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	return map[string]semanticGenerationReadinessReader{
		"status": func(ctx context.Context, profile embedding.Profile) (semanticreadiness.Snapshot, error) {
			return st.SemanticReadinessSnapshotAt(ctx, profile, 25_000, now)
		},
		"runtime": func(ctx context.Context, profile embedding.Profile) (semanticreadiness.Snapshot, error) {
			return st.SemanticRuntimeReadinessSnapshotAt(ctx, profile, 25_000, now)
		},
	}
}

func seedCompletedSemanticGeneration(t *testing.T) (*Store, embedding.Profile, string, string, int64) {
	t.Helper()
	st, _, profile := seedReadySemanticReadinessStore(t, "source:active-generation")
	ctx := context.Background()
	profileID, err := profile.ID()
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	window, err := st.NextRetrievalFlushWindow(ctx, profileID, RetrievalSegmentTarget)
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	if len(window.Rows) == 0 {
		_ = st.Close()
		t.Fatal("ready profile produced an empty generation window")
	}
	const generationID = "generation-ready"
	const segmentHash = "segment-ready"
	indexed := len(window.Rows)
	if err := st.CompleteRetrievalIndexGeneration(ctx, CompleteRetrievalIndexGenerationInput{
		Generation: RetrievalIndexGenerationRow{
			GenerationID: generationID, ProfileID: profileID, Backend: "usearch", BackendVersion: "2.26.0",
			Dimensions: profile.Dimensions, DistanceMetric: "cosine", IndexedChunkCount: indexed,
			SourceManifestHash: "generation-manifest", BuildStatus: RetrievalGenerationCompleted,
			RelativeCachePath: "semantic/test/generations/generation-ready",
			BuildStartedAt:    time.Now().UTC(), BuildCompletedAt: time.Now().UTC(),
		},
		Segments: []RetrievalIndexSegmentRow{{
			SegmentHash: segmentHash, ProfileID: profileID, Backend: "usearch", BackendVersion: "2.26.0",
			Dimensions: profile.Dimensions, DistanceMetric: "cosine", IndexedChunkCount: indexed,
			RelativeCachePath: "semantic/test/segments/segment-ready", MembershipHash: "members-ready",
			PayloadHash: "payload-ready", ManifestHash: "manifest-ready",
		}},
		Members: retrievalSegmentMembers(window.Rows, segmentHash), SnapshotRevision: window.SnapshotRevision,
		ExpectedActiveGenerationID: window.Profile.ActiveGenerationID, ExpectedPurgeEpoch: window.Profile.PurgeEpoch,
		ExpectedActiveSnapshotRevision: window.Profile.ActiveSnapshotRevision,
		ActivationMode:                 RetrievalGenerationAdvanceSnapshot,
	}); err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	return st, profile, generationID, segmentHash, window.SnapshotRevision
}

func execSemanticGenerationCorruption(t *testing.T, st *Store, query string, args ...any) {
	t.Helper()
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func corruptActiveSemanticGenerationStatus(t *testing.T, st *Store, generationID string) {
	t.Helper()
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DROP TRIGGER trg_retrieval_generations_completed_active_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE retrieval_index_generations SET build_status='stale' WHERE generation_id=?`, generationID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
