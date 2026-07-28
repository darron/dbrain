package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticreadiness"
)

const activeSemanticGenerationMetadataQuery = `
	SELECT profile_id,active,build_status,backend,backend_version,dimensions,distance_metric,
		indexed_chunk_count,source_manifest_hash,relative_cache_path
	FROM retrieval_index_generations
	WHERE generation_id=?`

const activeSemanticGenerationSegmentsQuery = `
	SELECT
		COUNT(gs.segment_hash),
		COALESCE(SUM(s.indexed_chunk_count), 0),
		COALESCE(SUM(
			s.profile_id != g.profile_id OR
			s.backend != g.backend OR
			s.backend_version != g.backend_version OR
			s.dimensions != g.dimensions OR
			s.distance_metric != g.distance_metric OR
			s.indexed_chunk_count <= 0 OR
			TRIM(s.relative_cache_path) = '' OR
			TRIM(s.membership_hash) = '' OR
			TRIM(s.payload_hash) = '' OR
			TRIM(s.manifest_hash) = ''
		), 0)
	FROM retrieval_index_generations g
	LEFT JOIN retrieval_generation_segments gs
		ON gs.generation_id = g.generation_id
	LEFT JOIN retrieval_index_segments s
		ON s.segment_hash = gs.segment_hash
	WHERE g.generation_id = ?
	GROUP BY g.generation_id`

func proveActiveSemanticGenerationMetadata(
	ctx context.Context,
	tx *sql.Tx,
	profile embedding.Profile,
	snapshot *semanticreadiness.Snapshot,
) error {
	snapshot.ActiveGenerationValid = false
	snapshot.ActiveGenerationProblem = ""
	snapshot.ActiveGenerationBackend = ""
	snapshot.ActiveGenerationBackendVersion = ""
	snapshot.ActiveGenerationDistanceMetric = ""
	snapshot.ActiveGenerationDimensions = 0
	if snapshot.ActiveGenerationID == "" {
		snapshot.ActiveGenerationValid = true
		return nil
	}
	fail := func(problem string) error {
		snapshot.ActiveGenerationProblem = problem
		return nil
	}

	var generationProfileID, buildStatus, sourceManifestHash, relativeCachePath string
	var active, indexedChunkCount int
	err := tx.QueryRowContext(ctx, activeSemanticGenerationMetadataQuery, snapshot.ActiveGenerationID).Scan(
		&generationProfileID, &active, &buildStatus, &snapshot.ActiveGenerationBackend,
		&snapshot.ActiveGenerationBackendVersion, &snapshot.ActiveGenerationDimensions,
		&snapshot.ActiveGenerationDistanceMetric, &indexedChunkCount, &sourceManifestHash, &relativeCachePath,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return fail("active generation row is missing")
	}
	if err != nil {
		return fmt.Errorf("read active semantic generation metadata: %w", err)
	}
	switch {
	case generationProfileID != snapshot.ProfileID:
		return fail("active generation profile does not match")
	case active != 1:
		return fail("active generation row is not active")
	case buildStatus != string(RetrievalGenerationCompleted):
		return fail("active generation row is not completed")
	case strings.TrimSpace(snapshot.ActiveGenerationBackend) == "":
		return fail("active generation backend is empty")
	case strings.TrimSpace(snapshot.ActiveGenerationBackendVersion) == "":
		return fail("active generation backend version is empty")
	case strings.TrimSpace(sourceManifestHash) == "":
		return fail("active generation source manifest hash is empty")
	case strings.TrimSpace(relativeCachePath) == "":
		return fail("active generation relative cache path is empty")
	case snapshot.ActiveGenerationDistanceMetric != "cosine":
		return fail("active generation distance metric is unsupported")
	case snapshot.ActiveGenerationDimensions != profile.Dimensions:
		return fail("active generation dimensions do not match")
	case indexedChunkCount != snapshot.ActiveIndexedCount:
		return fail("active generation indexed count does not match profile")
	case snapshot.ActiveSnapshotRevision <= 0:
		return fail("active generation snapshot revision is not positive")
	case snapshot.ActiveSnapshotRevision > snapshot.LatestRevision:
		return fail("active generation snapshot revision exceeds latest revision")
	}

	var segmentCount, segmentIndexedCount, segmentMismatches int
	if err := tx.QueryRowContext(ctx, activeSemanticGenerationSegmentsQuery, snapshot.ActiveGenerationID).Scan(
		&segmentCount, &segmentIndexedCount, &segmentMismatches,
	); err != nil {
		return fmt.Errorf("prove active semantic generation segments: %w", err)
	}
	switch {
	case segmentCount == 0:
		return fail("active generation has no segments")
	case segmentMismatches != 0:
		return fail("active generation segment provenance does not match")
	case segmentIndexedCount != indexedChunkCount:
		return fail("active generation segment count does not match")
	}

	snapshot.ActiveGenerationValid = true
	return nil
}
