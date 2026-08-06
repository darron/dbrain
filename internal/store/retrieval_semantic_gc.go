package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

type RetrievalSemanticGCOptions struct {
	Now             time.Time
	GracePeriod     time.Duration
	RetainPublished int
}

type RetrievalSemanticGCArtifact struct {
	ID                string    `json:"id"`
	ProfileID         string    `json:"profile_id"`
	RelativeCachePath string    `json:"relative_cache_path"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type RetrievalSemanticGCPlan struct {
	CatalogProfiles     []string                      `json:"catalog_profiles"`
	RetainedGenerations []RetrievalSemanticGCArtifact `json:"retained_generations"`
	PrunableGenerations []RetrievalSemanticGCArtifact `json:"prunable_generations"`
	RetainedSegments    []RetrievalSemanticGCArtifact `json:"retained_segments"`
	PrunableSegments    []RetrievalSemanticGCArtifact `json:"prunable_segments"`
	PrunableMemberRows  int64                         `json:"prunable_member_rows"`
}

type retrievalSemanticGCGeneration struct {
	artifact       RetrievalSemanticGCArtifact
	status         RetrievalGenerationStatus
	sourceManifest string
	active         bool
}

func (s *Store) PlanRetrievalSemanticGC(ctx context.Context, opts RetrievalSemanticGCOptions) (RetrievalSemanticGCPlan, error) {
	if err := validateRetrievalSemanticGCOptions(opts); err != nil {
		return RetrievalSemanticGCPlan{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RetrievalSemanticGCPlan{}, fmt.Errorf("begin retrieval semantic GC plan: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	plan, err := planRetrievalSemanticGCTx(ctx, tx, opts)
	if err != nil {
		return RetrievalSemanticGCPlan{}, err
	}
	if err := tx.Commit(); err != nil {
		return RetrievalSemanticGCPlan{}, fmt.Errorf("commit retrieval semantic GC plan: %w", err)
	}
	return plan, nil
}

func validateRetrievalSemanticGCOptions(opts RetrievalSemanticGCOptions) error {
	if opts.Now.IsZero() {
		return fmt.Errorf("retrieval semantic GC current time is required")
	}
	if opts.GracePeriod < 0 {
		return fmt.Errorf("retrieval semantic GC grace period must not be negative")
	}
	if opts.RetainPublished < 0 {
		return fmt.Errorf("retrieval semantic GC retained generation count must not be negative")
	}
	return nil
}

func planRetrievalSemanticGCTx(ctx context.Context, tx *sql.Tx, opts RetrievalSemanticGCOptions) (RetrievalSemanticGCPlan, error) {
	profiles, err := loadRetrievalSemanticGCProfiles(ctx, tx)
	if err != nil {
		return RetrievalSemanticGCPlan{}, err
	}
	generations, err := loadRetrievalSemanticGCGenerations(ctx, tx)
	if err != nil {
		return RetrievalSemanticGCPlan{}, err
	}
	retained := make(map[string]struct{})
	for id, generation := range generations {
		if generation.active || !generation.artifact.UpdatedAt.Before(opts.Now.Add(-opts.GracePeriod)) {
			retained[id] = struct{}{}
		}
	}
	if err := retainRetrievalSemanticGCProfilePointers(ctx, tx, generations, retained); err != nil {
		return RetrievalSemanticGCPlan{}, err
	}
	if err := retainRetrievalSemanticGCRunPointers(ctx, tx, generations, retained); err != nil {
		return RetrievalSemanticGCPlan{}, err
	}
	retainRetrievalSemanticGCPublished(generations, retained, opts.RetainPublished)

	segments, generationSegments, memberCounts, err := loadRetrievalSemanticGCSegments(ctx, tx, generations)
	if err != nil {
		return RetrievalSemanticGCPlan{}, err
	}
	reachableSegments := make(map[string]struct{})
	for generationID := range retained {
		for _, segmentHash := range generationSegments[generationID] {
			reachableSegments[segmentHash] = struct{}{}
		}
	}
	plan := RetrievalSemanticGCPlan{
		CatalogProfiles:     profiles,
		RetainedGenerations: make([]RetrievalSemanticGCArtifact, 0, len(retained)),
		PrunableGenerations: make([]RetrievalSemanticGCArtifact, 0, len(generations)-len(retained)),
		RetainedSegments:    make([]RetrievalSemanticGCArtifact, 0, len(reachableSegments)),
		PrunableSegments:    make([]RetrievalSemanticGCArtifact, 0, len(segments)-len(reachableSegments)),
	}
	for id, generation := range generations {
		if _, keep := retained[id]; keep {
			plan.RetainedGenerations = append(plan.RetainedGenerations, generation.artifact)
		} else {
			plan.PrunableGenerations = append(plan.PrunableGenerations, generation.artifact)
		}
	}
	for hash, segment := range segments {
		if _, keep := reachableSegments[hash]; keep {
			plan.RetainedSegments = append(plan.RetainedSegments, segment)
		} else {
			plan.PrunableSegments = append(plan.PrunableSegments, segment)
			plan.PrunableMemberRows += memberCounts[hash]
		}
	}
	sortRetrievalSemanticGCArtifacts(plan.RetainedGenerations)
	sortRetrievalSemanticGCArtifacts(plan.PrunableGenerations)
	sortRetrievalSemanticGCArtifacts(plan.RetainedSegments)
	sortRetrievalSemanticGCArtifacts(plan.PrunableSegments)
	return plan, nil
}

func loadRetrievalSemanticGCProfiles(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT profile_id FROM retrieval_embedding_profiles ORDER BY profile_id`)
	if err != nil {
		return nil, fmt.Errorf("list retrieval semantic GC profiles: %w", err)
	}
	defer func() { _ = rows.Close() }()
	profiles := make([]string, 0)
	for rows.Next() {
		var profileID string
		if err := rows.Scan(&profileID); err != nil {
			return nil, fmt.Errorf("scan retrieval semantic GC profile: %w", err)
		}
		profiles = append(profiles, profileID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retrieval semantic GC profiles: %w", err)
	}
	return profiles, nil
}

func loadRetrievalSemanticGCGenerations(ctx context.Context, tx *sql.Tx) (map[string]retrievalSemanticGCGeneration, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT generation_id,profile_id,relative_cache_path,source_manifest_hash,build_status,active,created_at,updated_at
		FROM retrieval_index_generations ORDER BY generation_id`)
	if err != nil {
		return nil, fmt.Errorf("list retrieval semantic GC generations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make(map[string]retrievalSemanticGCGeneration)
	for rows.Next() {
		var generation retrievalSemanticGCGeneration
		var active int
		var createdAt, updatedAt string
		if err := rows.Scan(&generation.artifact.ID, &generation.artifact.ProfileID,
			&generation.artifact.RelativeCachePath, &generation.sourceManifest, &generation.status,
			&active, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan retrieval semantic GC generation: %w", err)
		}
		generation.active = active == 1
		generation.artifact.CreatedAt, err = parseRetrievalSemanticGCTime("generation created_at", createdAt)
		if err != nil {
			return nil, err
		}
		generation.artifact.UpdatedAt, err = parseRetrievalSemanticGCTime("generation updated_at", updatedAt)
		if err != nil {
			return nil, err
		}
		if _, duplicate := result[generation.artifact.ID]; duplicate {
			return nil, fmt.Errorf("retrieval semantic GC generation %s is duplicated", generation.artifact.ID)
		}
		result[generation.artifact.ID] = generation
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retrieval semantic GC generations: %w", err)
	}
	return result, nil
}

func retainRetrievalSemanticGCProfilePointers(ctx context.Context, tx *sql.Tx, generations map[string]retrievalSemanticGCGeneration, retained map[string]struct{}) error {
	rows, err := tx.QueryContext(ctx, `SELECT profile_id,active_generation_id FROM retrieval_embedding_profiles WHERE active_generation_id!='' ORDER BY profile_id`)
	if err != nil {
		return fmt.Errorf("list retrieval semantic GC profile pointers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var profileID, generationID string
		if err := rows.Scan(&profileID, &generationID); err != nil {
			return fmt.Errorf("scan retrieval semantic GC profile pointer: %w", err)
		}
		generation, exists := generations[generationID]
		if !exists || generation.artifact.ProfileID != profileID {
			return fmt.Errorf("retrieval semantic GC profile %s points to missing generation %s", profileID, generationID)
		}
		retained[generationID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate retrieval semantic GC profile pointers: %w", err)
	}
	return nil
}

func retainRetrievalSemanticGCRunPointers(ctx context.Context, tx *sql.Tx, generations map[string]retrievalSemanticGCGeneration, retained map[string]struct{}) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT profile_id,current_generation_id FROM semantic_refresh_runs
		WHERE state IN ('running','failed','cancelled') AND current_generation_id!='' ORDER BY run_id`)
	if err != nil {
		return fmt.Errorf("list retrieval semantic GC run pointers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var profileID, generationID string
		if err := rows.Scan(&profileID, &generationID); err != nil {
			return fmt.Errorf("scan retrieval semantic GC run pointer: %w", err)
		}
		generation, exists := generations[generationID]
		if !exists || generation.artifact.ProfileID != profileID {
			return fmt.Errorf("retrieval semantic GC refresh run for profile %s points to missing generation %s", profileID, generationID)
		}
		retained[generationID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate retrieval semantic GC run pointers: %w", err)
	}
	return nil
}

func (s *Store) PruneRetrievalSemanticCatalog(ctx context.Context, opts RetrievalSemanticGCOptions) (RetrievalSemanticGCPlan, error) {
	if err := validateRetrievalSemanticGCOptions(opts); err != nil {
		return RetrievalSemanticGCPlan{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RetrievalSemanticGCPlan{}, fmt.Errorf("begin retrieval semantic GC prune: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	plan, err := planRetrievalSemanticGCTx(ctx, tx, opts)
	if err != nil {
		return RetrievalSemanticGCPlan{}, err
	}
	for _, generation := range plan.PrunableGenerations {
		if _, err := tx.ExecContext(ctx, `DELETE FROM retrieval_generation_segments WHERE generation_id=?`, generation.ID); err != nil {
			return RetrievalSemanticGCPlan{}, fmt.Errorf("delete retrieval semantic GC generation links %s: %w", generation.ID, err)
		}
	}
	for _, generation := range plan.PrunableGenerations {
		if _, err := tx.ExecContext(ctx, `DELETE FROM retrieval_index_generations WHERE generation_id=?`, generation.ID); err != nil {
			return RetrievalSemanticGCPlan{}, fmt.Errorf("delete retrieval semantic GC generation %s: %w", generation.ID, err)
		}
	}
	for _, segment := range plan.PrunableSegments {
		if _, err := tx.ExecContext(ctx, `DELETE FROM retrieval_index_segment_members WHERE segment_hash=?`, segment.ID); err != nil {
			return RetrievalSemanticGCPlan{}, fmt.Errorf("delete retrieval semantic GC segment members %s: %w", segment.ID, err)
		}
	}
	for _, segment := range plan.PrunableSegments {
		if _, err := tx.ExecContext(ctx, `DELETE FROM retrieval_index_segments WHERE segment_hash=?`, segment.ID); err != nil {
			return RetrievalSemanticGCPlan{}, fmt.Errorf("delete retrieval semantic GC segment %s: %w", segment.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return RetrievalSemanticGCPlan{}, fmt.Errorf("commit retrieval semantic GC prune: %w", err)
	}
	return plan, nil
}

func (s *Store) VacuumRetrievalDatabase(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return retrievalSemanticGCVacuumError("checkpoint before VACUUM", err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return retrievalSemanticGCVacuumError("VACUUM", err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return retrievalSemanticGCVacuumError("checkpoint after VACUUM", err)
	}
	return nil
}

func retrievalSemanticGCVacuumError(operation string, err error) error {
	if isBusyError(err) {
		return fmt.Errorf("semantic GC %s: database is busy; stop the dbrain daemon and other writers, then retry: %w", operation, err)
	}
	return fmt.Errorf("semantic GC %s: %w", operation, err)
}

func retainRetrievalSemanticGCPublished(generations map[string]retrievalSemanticGCGeneration, retained map[string]struct{}, count int) {
	byProfile := make(map[string][]retrievalSemanticGCGeneration)
	for _, generation := range generations {
		published := (generation.status == RetrievalGenerationCompleted || generation.status == RetrievalGenerationStale) &&
			strings.TrimSpace(generation.artifact.RelativeCachePath) != "" && strings.TrimSpace(generation.sourceManifest) != ""
		if published {
			byProfile[generation.artifact.ProfileID] = append(byProfile[generation.artifact.ProfileID], generation)
		}
	}
	for _, candidates := range byProfile {
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].artifact.CreatedAt.Equal(candidates[j].artifact.CreatedAt) {
				return candidates[i].artifact.ID > candidates[j].artifact.ID
			}
			return candidates[i].artifact.CreatedAt.After(candidates[j].artifact.CreatedAt)
		})
		for index := 0; index < len(candidates) && index < count; index++ {
			retained[candidates[index].artifact.ID] = struct{}{}
		}
	}
}

func loadRetrievalSemanticGCSegments(ctx context.Context, tx *sql.Tx, generations map[string]retrievalSemanticGCGeneration) (map[string]RetrievalSemanticGCArtifact, map[string][]string, map[string]int64, error) {
	segments := make(map[string]RetrievalSemanticGCArtifact)
	rows, err := tx.QueryContext(ctx, `SELECT segment_hash,profile_id,relative_cache_path,created_at FROM retrieval_index_segments ORDER BY segment_hash`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list retrieval semantic GC segments: %w", err)
	}
	for rows.Next() {
		var artifact RetrievalSemanticGCArtifact
		var createdAt string
		if err := rows.Scan(&artifact.ID, &artifact.ProfileID, &artifact.RelativeCachePath, &createdAt); err != nil {
			_ = rows.Close()
			return nil, nil, nil, fmt.Errorf("scan retrieval semantic GC segment: %w", err)
		}
		artifact.CreatedAt, err = parseRetrievalSemanticGCTime("segment created_at", createdAt)
		if err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		segments[artifact.ID] = artifact
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, nil, fmt.Errorf("iterate retrieval semantic GC segments: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, fmt.Errorf("close retrieval semantic GC segments: %w", err)
	}

	generationSegments := make(map[string][]string)
	rows, err = tx.QueryContext(ctx, `SELECT generation_id,segment_hash FROM retrieval_generation_segments ORDER BY generation_id,segment_hash`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list retrieval semantic GC generation segments: %w", err)
	}
	for rows.Next() {
		var generationID, segmentHash string
		if err := rows.Scan(&generationID, &segmentHash); err != nil {
			_ = rows.Close()
			return nil, nil, nil, fmt.Errorf("scan retrieval semantic GC generation segment: %w", err)
		}
		generation, generationExists := generations[generationID]
		segment, segmentExists := segments[segmentHash]
		if !generationExists || !segmentExists || generation.artifact.ProfileID != segment.ProfileID {
			_ = rows.Close()
			return nil, nil, nil, fmt.Errorf("retrieval semantic GC catalog link %s/%s is inconsistent", generationID, segmentHash)
		}
		generationSegments[generationID] = append(generationSegments[generationID], segmentHash)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, nil, fmt.Errorf("iterate retrieval semantic GC generation segments: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, fmt.Errorf("close retrieval semantic GC generation segments: %w", err)
	}

	memberCounts := make(map[string]int64)
	rows, err = tx.QueryContext(ctx, `SELECT segment_hash,COUNT(*) FROM retrieval_index_segment_members GROUP BY segment_hash ORDER BY segment_hash`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("count retrieval semantic GC segment members: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var segmentHash string
		var count int64
		if err := rows.Scan(&segmentHash, &count); err != nil {
			return nil, nil, nil, fmt.Errorf("scan retrieval semantic GC segment member count: %w", err)
		}
		if _, exists := segments[segmentHash]; !exists {
			return nil, nil, nil, fmt.Errorf("retrieval semantic GC member segment %s is missing", segmentHash)
		}
		memberCounts[segmentHash] = count
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("iterate retrieval semantic GC segment member counts: %w", err)
	}
	return segments, generationSegments, memberCounts, nil
}

func parseRetrievalSemanticGCTime(name, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse retrieval semantic GC %s %q: %w", name, value, err)
	}
	return parsed.UTC(), nil
}

func sortRetrievalSemanticGCArtifacts(artifacts []RetrievalSemanticGCArtifact) {
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].ProfileID == artifacts[j].ProfileID {
			return artifacts[i].ID < artifacts[j].ID
		}
		return artifacts[i].ProfileID < artifacts[j].ProfileID
	})
}
