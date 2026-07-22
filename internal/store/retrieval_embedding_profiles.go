package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/embedding"
)

var (
	ErrRetrievalPurgeEpochChanged        = errors.New("retrieval purge epoch changed")
	ErrRetrievalEmbeddingProfileNotFound = errors.New("retrieval embedding profile not found")
)

var retrievalEmbeddingProfileTriggersV19 = []retrievalConstraintTrigger{
	{
		name: "trg_retrieval_embeddings_profile_invariants_insert", table: "retrieval_embeddings",
		sql: `CREATE TRIGGER trg_retrieval_embeddings_profile_invariants_insert
			BEFORE INSERT ON retrieval_embeddings
			WHEN NOT EXISTS (
				SELECT 1 FROM retrieval_embedding_profiles p, retrieval_chunks c
				WHERE p.profile_id=NEW.profile_id AND p.provider=NEW.provider
					AND p.model=NEW.model AND p.dimensions=NEW.dimensions
					AND c.chunk_id=NEW.chunk_id
					AND p.projection_version=c.projection_version
					AND p.chunker_version=c.chunker_version
					AND p.representation=NEW.representation AND p.normalization=NEW.normalization
			)
			BEGIN SELECT RAISE(ABORT, 'retrieval embedding profile invariants do not match'); END`,
	},
	{
		name: "trg_retrieval_embeddings_profile_invariants_update", table: "retrieval_embeddings",
		sql: `CREATE TRIGGER trg_retrieval_embeddings_profile_invariants_update
			BEFORE UPDATE OF chunk_id,profile_id,provider,model,dimensions,representation,normalization ON retrieval_embeddings
			WHEN NOT EXISTS (
				SELECT 1 FROM retrieval_embedding_profiles p, retrieval_chunks c
				WHERE p.profile_id=NEW.profile_id AND p.provider=NEW.provider
					AND p.model=NEW.model AND p.dimensions=NEW.dimensions
					AND c.chunk_id=NEW.chunk_id
					AND p.projection_version=c.projection_version
					AND p.chunker_version=c.chunker_version
					AND p.representation=NEW.representation AND p.normalization=NEW.normalization
			)
			BEGIN SELECT RAISE(ABORT, 'retrieval embedding profile invariants do not match'); END`,
	},
	{
		name: "trg_retrieval_embedding_profiles_definition_immutable", table: "retrieval_embedding_profiles",
		sql: `CREATE TRIGGER trg_retrieval_embedding_profiles_definition_immutable
			BEFORE UPDATE OF provider,model,dimensions,projection_version,chunker_version,representation,normalization
			ON retrieval_embedding_profiles
			WHEN OLD.provider!=NEW.provider OR OLD.model!=NEW.model OR OLD.dimensions!=NEW.dimensions
				OR OLD.projection_version!=NEW.projection_version OR OLD.chunker_version!=NEW.chunker_version
				OR OLD.representation!=NEW.representation OR OLD.normalization!=NEW.normalization
			BEGIN SELECT RAISE(ABORT, 'retrieval embedding profile definition is immutable'); END`,
	},
	{
		name: "trg_retrieval_embeddings_account_delete", table: "retrieval_embeddings",
		sql: `CREATE TRIGGER trg_retrieval_embeddings_account_delete
			BEFORE DELETE ON retrieval_embeddings
			BEGIN
				SELECT CASE WHEN NOT EXISTS (
					SELECT 1 FROM retrieval_embedding_profiles WHERE profile_id=OLD.profile_id
				) THEN RAISE(ABORT, 'retrieval embedding profile aggregate row is missing') END;
				SELECT CASE WHEN OLD.status='ready' AND EXISTS (
				SELECT 1 FROM retrieval_embedding_profiles profile
					WHERE profile.profile_id=OLD.profile_id AND profile.l0_ready_count<=0
						AND NOT EXISTS (
							SELECT 1 FROM retrieval_generation_segments generation
							JOIN retrieval_index_segment_members member ON member.segment_hash=generation.segment_hash
							WHERE generation.generation_id=profile.active_generation_id AND member.chunk_id=OLD.chunk_id
								AND member.revision=OLD.revision AND member.vector_hash=OLD.vector_hash
						)
				) THEN RAISE(ABORT, 'retrieval embedding profile L0 aggregate drift') END;
				UPDATE retrieval_embedding_profiles SET
					l0_ready_count=l0_ready_count-CASE WHEN OLD.status='ready' AND NOT EXISTS (
						SELECT 1 FROM retrieval_generation_segments generation
						JOIN retrieval_index_segment_members member ON member.segment_hash=generation.segment_hash
						WHERE generation.generation_id=retrieval_embedding_profiles.active_generation_id AND member.chunk_id=OLD.chunk_id
							AND member.revision=OLD.revision AND member.vector_hash=OLD.vector_hash
					) THEN 1 ELSE 0 END,
					active_tombstone_count=active_tombstone_count+CASE WHEN OLD.status='ready' AND EXISTS (
						SELECT 1 FROM retrieval_generation_segments generation
						JOIN retrieval_index_segment_members member ON member.segment_hash=generation.segment_hash
						WHERE generation.generation_id=retrieval_embedding_profiles.active_generation_id AND member.chunk_id=OLD.chunk_id
							AND member.revision=OLD.revision AND member.vector_hash=OLD.vector_hash
					) THEN 1 ELSE 0 END,
					updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now')
				WHERE profile_id=OLD.profile_id;
			END`,
	},
}

type RetrievalEmbeddingVerificationState struct {
	ProfileID, ActiveGenerationID                                         string
	Profile                                                               embedding.Profile
	LatestRevision, PurgeEpoch, GlobalPurgeEpoch, ActiveSnapshotRevision  int64
	ActiveIndexedCount, L0ReadyCount, ActiveTombstoneCount                int
	GenerationBackend, GenerationBackendVersion, GenerationDistanceMetric string
	GenerationStatus                                                      RetrievalGenerationStatus
	GenerationDimensions, GenerationIndexedChunkCount                     int
	GenerationActive                                                      bool
}

func (s *Store) RetrievalPurgeEpoch(ctx context.Context) (int64, error) {
	var epoch int64
	if err := s.queryer().QueryRowContext(ctx, `SELECT purge_epoch FROM retrieval_state WHERE singleton=1`).Scan(&epoch); err != nil {
		return 0, fmt.Errorf("read retrieval purge epoch: %w", err)
	}
	return epoch, nil
}

func (s *Store) RetrievalEmbeddingProfile(ctx context.Context, profileID string) (RetrievalEmbeddingProfileRow, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return RetrievalEmbeddingProfileRow{}, fmt.Errorf("retrieval embedding profile is required")
	}
	var row RetrievalEmbeddingProfileRow
	err := s.queryer().QueryRowContext(ctx, `
		SELECT profile_id, latest_revision, purge_epoch, active_generation_id,
			active_snapshot_revision, active_indexed_count, l0_ready_count,
			active_tombstone_count
		FROM retrieval_embedding_profiles
		WHERE profile_id=?`, profileID).Scan(
		&row.ProfileID, &row.LatestRevision, &row.PurgeEpoch, &row.ActiveGenerationID,
		&row.ActiveSnapshotRevision, &row.ActiveIndexedCount, &row.L0ReadyCount,
		&row.ActiveTombstoneCount,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RetrievalEmbeddingProfileRow{}, fmt.Errorf("retrieval embedding profile %s: %w", profileID, sql.ErrNoRows)
		}
		return RetrievalEmbeddingProfileRow{}, fmt.Errorf("read retrieval embedding profile %s: %w", profileID, err)
	}
	return row, nil
}

func ensureRetrievalEmbeddingProfileTx(ctx context.Context, tx *sql.Tx, profileID string, purgeEpoch int64, profile embedding.Profile) (RetrievalEmbeddingProfileRow, error) {
	requested := profile
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO retrieval_embedding_profiles (
			profile_id, latest_revision, purge_epoch, active_generation_id,
			active_snapshot_revision, active_indexed_count, l0_ready_count,
			active_tombstone_count, provider, model, dimensions, projection_version,
			chunker_version, representation, normalization, updated_at
		) VALUES (?, 0, ?, '', 0, 0, 0, 0, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id) DO NOTHING`, profileID, purgeEpoch, profile.Provider, profile.Model,
		profile.Dimensions, profile.ProjectionVersion, profile.ChunkerVersion,
		profile.Representation, profile.Normalization, now); err != nil {
		return RetrievalEmbeddingProfileRow{}, fmt.Errorf("ensure retrieval embedding profile %s: %w", profileID, err)
	}
	var row RetrievalEmbeddingProfileRow
	if err := tx.QueryRowContext(ctx, `
		SELECT profile_id, latest_revision, purge_epoch, active_generation_id,
			active_snapshot_revision, active_indexed_count, l0_ready_count,
			active_tombstone_count, provider, model, dimensions, projection_version,
			chunker_version, representation, normalization
		FROM retrieval_embedding_profiles WHERE profile_id=?`, profileID).Scan(
		&row.ProfileID, &row.LatestRevision, &row.PurgeEpoch, &row.ActiveGenerationID,
		&row.ActiveSnapshotRevision, &row.ActiveIndexedCount, &row.L0ReadyCount,
		&row.ActiveTombstoneCount, &profile.Provider, &profile.Model, &profile.Dimensions,
		&profile.ProjectionVersion, &profile.ChunkerVersion, &profile.Representation, &profile.Normalization,
	); err != nil {
		return RetrievalEmbeddingProfileRow{}, fmt.Errorf("load retrieval embedding profile %s: %w", profileID, err)
	}
	if row.PurgeEpoch != purgeEpoch {
		return RetrievalEmbeddingProfileRow{}, fmt.Errorf("%w: profile %s is at epoch %d, expected %d", ErrRetrievalPurgeEpochChanged, profileID, row.PurgeEpoch, purgeEpoch)
	}
	if profile != requested {
		return RetrievalEmbeddingProfileRow{}, fmt.Errorf("retrieval embedding profile %s immutable definition does not match", profileID)
	}
	storedID, idErr := profile.ID()
	if idErr == nil && strings.HasPrefix(profileID, "embedding-profile-v1:") && storedID != profileID {
		return RetrievalEmbeddingProfileRow{}, fmt.Errorf("retrieval embedding profile %s definition does not match its ID", profileID)
	}
	return row, nil
}

func (s *Store) RetrievalEmbeddingVerificationState(ctx context.Context, profileID string) (RetrievalEmbeddingVerificationState, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return RetrievalEmbeddingVerificationState{}, fmt.Errorf("retrieval embedding profile is required")
	}
	var state RetrievalEmbeddingVerificationState
	var active int
	err := s.queryer().QueryRowContext(ctx, `
		SELECT p.profile_id,p.latest_revision,p.purge_epoch,rs.purge_epoch,p.active_generation_id,
			p.active_snapshot_revision,p.active_indexed_count,p.l0_ready_count,p.active_tombstone_count,
			p.provider,p.model,p.dimensions,p.projection_version,p.chunker_version,p.representation,p.normalization,
			COALESCE(g.backend,''),COALESCE(g.backend_version,''),COALESCE(g.dimensions,0),COALESCE(g.indexed_chunk_count,0),
			COALESCE(g.distance_metric,''),COALESCE(g.build_status,''),COALESCE(g.active,0)
		FROM retrieval_embedding_profiles p CROSS JOIN retrieval_state rs
		LEFT JOIN retrieval_index_generations g ON g.generation_id=p.active_generation_id
		WHERE p.profile_id=?`, profileID).Scan(
		&state.ProfileID, &state.LatestRevision, &state.PurgeEpoch, &state.GlobalPurgeEpoch, &state.ActiveGenerationID,
		&state.ActiveSnapshotRevision, &state.ActiveIndexedCount, &state.L0ReadyCount, &state.ActiveTombstoneCount,
		&state.Profile.Provider, &state.Profile.Model, &state.Profile.Dimensions, &state.Profile.ProjectionVersion,
		&state.Profile.ChunkerVersion, &state.Profile.Representation, &state.Profile.Normalization,
		&state.GenerationBackend, &state.GenerationBackendVersion, &state.GenerationDimensions,
		&state.GenerationIndexedChunkCount,
		&state.GenerationDistanceMetric, &state.GenerationStatus, &active,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RetrievalEmbeddingVerificationState{}, fmt.Errorf("%w: %s", ErrRetrievalEmbeddingProfileNotFound, profileID)
		}
		return RetrievalEmbeddingVerificationState{}, fmt.Errorf("read retrieval embedding verification state %s: %w", profileID, err)
	}
	state.GenerationActive = active == 1
	return state, nil
}

func (s *Store) ensureRetrievalEmbeddingProfileDefinitions() error {
	if err := s.ensureColumns("retrieval_embedding_profiles", []columnDefinition{
		{Name: "provider", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "model", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "dimensions", Definition: "INTEGER NOT NULL DEFAULT 0"},
		{Name: "projection_version", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "chunker_version", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "representation", Definition: "TEXT NOT NULL DEFAULT ''"},
		{Name: "normalization", Definition: "TEXT NOT NULL DEFAULT ''"},
	}); err != nil {
		return fmt.Errorf("ensure retrieval embedding profile definitions: %w", err)
	}
	var mixed string
	err := s.db.QueryRow(`SELECT profile_id FROM retrieval_embeddings GROUP BY profile_id HAVING
		MIN(provider)!=MAX(provider) OR MIN(model)!=MAX(model) OR MIN(dimensions)!=MAX(dimensions)
		OR MIN(representation)!=MAX(representation) OR MIN(normalization)!=MAX(normalization) LIMIT 1`).Scan(&mixed)
	if err == nil {
		return fmt.Errorf("retrieval embedding profile %s has mixed immutable definitions", mixed)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("validate retrieval embedding profile definitions: %w", err)
	}
	err = s.db.QueryRow(`SELECT e.profile_id FROM retrieval_embeddings e
		JOIN retrieval_chunks c ON c.chunk_id=e.chunk_id
		GROUP BY e.profile_id HAVING MIN(c.projection_version)!=MAX(c.projection_version)
			OR MIN(c.chunker_version)!=MAX(c.chunker_version) LIMIT 1`).Scan(&mixed)
	if err == nil {
		return fmt.Errorf("retrieval embedding profile %s has mixed chunk provenance", mixed)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("validate retrieval embedding profile chunk provenance: %w", err)
	}
	if _, err := s.db.Exec(`
		DELETE FROM retrieval_embedding_profiles
		WHERE NOT EXISTS (
			SELECT 1 FROM retrieval_embeddings e
			WHERE e.profile_id=retrieval_embedding_profiles.profile_id
		)`); err != nil {
		return fmt.Errorf("remove unverifiable empty retrieval embedding profiles: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`
		INSERT INTO retrieval_embedding_profiles (
			profile_id,latest_revision,purge_epoch,active_generation_id,active_snapshot_revision,
			active_indexed_count,l0_ready_count,active_tombstone_count,provider,model,dimensions,
			projection_version,chunker_version,representation,normalization,updated_at
		)
		SELECT e.profile_id,MAX(e.revision),state.purge_epoch,'',0,0,
			SUM(CASE WHEN e.status='ready' THEN 1 ELSE 0 END),0,
			MIN(e.provider),MIN(e.model),MIN(e.dimensions),MIN(c.projection_version),MIN(c.chunker_version),
			MIN(e.representation),MIN(e.normalization),?
		FROM retrieval_embeddings e JOIN retrieval_chunks c ON c.chunk_id=e.chunk_id
		CROSS JOIN retrieval_state state
		GROUP BY e.profile_id
		ON CONFLICT(profile_id) DO UPDATE SET
			provider=CASE WHEN provider='' THEN excluded.provider ELSE provider END,
			model=CASE WHEN model='' THEN excluded.model ELSE model END,
			dimensions=CASE WHEN dimensions=0 THEN excluded.dimensions ELSE dimensions END,
			projection_version=CASE WHEN projection_version='' THEN excluded.projection_version ELSE projection_version END,
			chunker_version=CASE WHEN chunker_version='' THEN excluded.chunker_version ELSE chunker_version END,
			representation=CASE WHEN representation='' THEN excluded.representation ELSE representation END,
			normalization=CASE WHEN normalization='' THEN excluded.normalization ELSE normalization END,
			latest_revision=MAX(latest_revision,excluded.latest_revision),
			purge_epoch=excluded.purge_epoch, active_generation_id='', active_snapshot_revision=0,
			active_indexed_count=0, l0_ready_count=excluded.l0_ready_count,
			active_tombstone_count=0, updated_at=?`, now, now); err != nil {
		return fmt.Errorf("backfill retrieval embedding profile definitions: %w", err)
	}
	// Generations created before the profile-definition contract cannot prove
	// their projection, chunker, purge-epoch, or revision-watermark provenance.
	// Keep their completed metadata, but require an explicit post-migration
	// activation/build before treating any of them as a usable root.
	if _, err := s.db.Exec(`UPDATE retrieval_index_generations SET active=0, activated_at='' WHERE active=1`); err != nil {
		return fmt.Errorf("deactivate unverifiable pre-v19 retrieval generations: %w", err)
	}
	var mismatch string
	err = s.db.QueryRow(`SELECT e.profile_id FROM retrieval_embeddings e
		JOIN retrieval_chunks c ON c.chunk_id=e.chunk_id
		JOIN retrieval_embedding_profiles p ON p.profile_id=e.profile_id
		WHERE e.provider!=p.provider OR e.model!=p.model OR e.dimensions!=p.dimensions
			OR c.projection_version!=p.projection_version OR c.chunker_version!=p.chunker_version
			OR e.representation!=p.representation OR e.normalization!=p.normalization LIMIT 1`).Scan(&mismatch)
	if err == nil {
		return fmt.Errorf("retrieval embedding profile %s conflicts with immutable definition", mismatch)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("verify retrieval embedding profile definition backfill: %w", err)
	}
	definitionRows, err := s.db.Query(`
		SELECT profile_id,provider,model,dimensions,projection_version,chunker_version,representation,normalization
		FROM retrieval_embedding_profiles`)
	if err != nil {
		return fmt.Errorf("list retrieval embedding profile definitions: %w", err)
	}
	for definitionRows.Next() {
		var profileID string
		var profile embedding.Profile
		if err := definitionRows.Scan(&profileID, &profile.Provider, &profile.Model, &profile.Dimensions,
			&profile.ProjectionVersion, &profile.ChunkerVersion, &profile.Representation, &profile.Normalization); err != nil {
			_ = definitionRows.Close()
			return fmt.Errorf("scan retrieval embedding profile definition: %w", err)
		}
		if !strings.HasPrefix(profileID, "embedding-profile-v1:") {
			continue
		}
		computed, err := profile.ID()
		if err != nil || computed != profileID {
			_ = definitionRows.Close()
			return fmt.Errorf("retrieval embedding profile %s definition does not match its ID", profileID)
		}
	}
	if err := definitionRows.Err(); err != nil {
		_ = definitionRows.Close()
		return fmt.Errorf("iterate retrieval embedding profile definitions: %w", err)
	}
	if err := definitionRows.Close(); err != nil {
		return fmt.Errorf("close retrieval embedding profile definitions: %w", err)
	}
	for _, trigger := range retrievalEmbeddingProfileTriggersV19 {
		if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
			return err
		}
		if _, err := s.db.Exec(trigger.sql); err != nil {
			return fmt.Errorf("create retrieval embedding profile trigger %s: %w", trigger.name, err)
		}
	}
	return nil
}

func (s *Store) repairRetrievalMembershipL0Activation() error {
	for _, trigger := range retrievalEmbeddingProfileTriggersV19 {
		if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
			return fmt.Errorf("drop retrieval embedding profile trigger %s: %w", trigger.name, err)
		}
		if _, err := s.db.Exec(trigger.sql); err != nil {
			return fmt.Errorf("create retrieval embedding profile trigger %s: %w", trigger.name, err)
		}
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin retrieval membership L0 counter repair: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query(`SELECT profile_id,active_generation_id FROM retrieval_embedding_profiles ORDER BY profile_id`)
	if err != nil {
		return fmt.Errorf("list retrieval profiles for membership L0 repair: %w", err)
	}
	type profileIdentity struct{ profileID, generationID string }
	profiles := make([]profileIdentity, 0)
	for rows.Next() {
		var profile profileIdentity
		if err := rows.Scan(&profile.profileID, &profile.generationID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan retrieval profile for membership L0 repair: %w", err)
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate retrieval profiles for membership L0 repair: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close retrieval profiles for membership L0 repair: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, profile := range profiles {
		var l0Ready, tombstones int
		if err := countRetrievalGenerationCountersTx(context.Background(), tx, profile.profileID, profile.generationID, &l0Ready, &tombstones); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE retrieval_embedding_profiles SET l0_ready_count=?,active_tombstone_count=?,updated_at=? WHERE profile_id=?`, l0Ready, tombstones, now, profile.profileID); err != nil {
			return fmt.Errorf("repair retrieval membership L0 counters for %s: %w", profile.profileID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit retrieval membership L0 counter repair: %w", err)
	}
	return nil
}

func (s *Store) repairRetrievalEmbeddingRevisionProvenance() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin retrieval embedding revision provenance repair: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query(`
		SELECT DISTINCT profile_id
		FROM retrieval_embeddings
		WHERE status='ready' AND (revision<=0 OR vector_hash='')
		ORDER BY profile_id`)
	if err != nil {
		return fmt.Errorf("list retrieval profiles with unproven ready embeddings: %w", err)
	}
	profiles := make([]string, 0)
	for rows.Next() {
		var profileID string
		if err := rows.Scan(&profileID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan retrieval profile with unproven ready embeddings: %w", err)
		}
		profiles = append(profiles, profileID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate retrieval profiles with unproven ready embeddings: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close retrieval profiles with unproven ready embeddings: %w", err)
	}
	for _, profileID := range profiles {
		if err := markRetrievalProfileGenerationsStaleTx(context.Background(), tx, profileID); err != nil {
			return err
		}
	}
	if len(profiles) == 0 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit empty retrieval embedding revision provenance repair: %w", err)
		}
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`
		UPDATE retrieval_embeddings SET
			status='pending', vector_bytes=X'', vector_hash=?,
			last_error='re-embedding required: unproven pre-v20 revision or vector hash',
			next_attempt_at='', embedded_at='', updated_at=?
		WHERE status='ready' AND (revision<=0 OR vector_hash='')`, retrievalVectorHash([]byte{}), now); err != nil {
		return fmt.Errorf("queue unproven ready embeddings for re-embedding: %w", err)
	}
	for _, profileID := range profiles {
		if _, err := tx.Exec(`
			UPDATE retrieval_embedding_profiles SET
				active_generation_id='', active_snapshot_revision=0, active_indexed_count=0,
				l0_ready_count=(SELECT COUNT(*) FROM retrieval_embeddings WHERE profile_id=? AND status='ready'),
				active_tombstone_count=0, updated_at=?
			WHERE profile_id=?`, profileID, now, profileID); err != nil {
			return fmt.Errorf("repair retrieval embedding profile counters %s: %w", profileID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit retrieval embedding revision provenance repair: %w", err)
	}
	return nil
}
