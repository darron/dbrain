package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/embedding"
)

const maxRetrievalEmbeddingBatchSize = 5_000

var (
	ErrRetrievalUnavailable              = errors.New("retrieval storage is unavailable")
	ErrRetrievalEmbeddingNoLongerCorrupt = errors.New("retrieval embedding is no longer corrupt")
)

type RetrievalEmbeddingStatus string

const (
	RetrievalEmbeddingPending RetrievalEmbeddingStatus = "pending"
	RetrievalEmbeddingReady   RetrievalEmbeddingStatus = "ready"
	RetrievalEmbeddingBlocked RetrievalEmbeddingStatus = "blocked"
	RetrievalEmbeddingError   RetrievalEmbeddingStatus = "error"
)

type RetrievalEmbeddingRow struct {
	ChunkID           string
	ProfileID         string
	Provider          string
	Model             string
	Dimensions        int
	Representation    string
	Normalization     string
	VectorBytes       []byte
	VectorHash        string
	ChunkTextHash     string
	Revision          int64
	Status            RetrievalEmbeddingStatus
	AttemptCount      int
	LastError         string
	NextAttemptAt     time.Time
	EmbeddedAt        time.Time
	ParentKind        string
	ParentSourceKey   string
	EvidenceRole      string
	SourceType        string
	SectionOrdinal    int
	Text              string
	ProjectionVersion string
	ChunkerVersion    string
}

type RetrievalEmbeddingCorruptionError struct {
	ChunkID   string
	ProfileID string
	Reason    string
}

type RetrievalChunkProfileMismatchError struct {
	ProjectionVersion string
	ChunkerVersion    string
	Count             int
}

func (e *RetrievalChunkProfileMismatchError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d retrieval chunks do not match embedding profile projection %q and chunker %q; run semantic chunk before semantic embed", e.Count, e.ProjectionVersion, e.ChunkerVersion)
}

func (e *RetrievalEmbeddingCorruptionError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("corrupt ready retrieval embedding for chunk %s profile %s: %s", e.ChunkID, e.ProfileID, e.Reason)
}

func (s *Store) ListChunksNeedingEmbedding(ctx context.Context, profileID, afterChunkID string, limit int) ([]RetrievalChunkRow, error) {
	return s.ListChunksNeedingEmbeddingAt(ctx, profileID, afterChunkID, limit, time.Now().UTC())
}

const retrievalEmbeddingDueSQL = `(e.chunk_id IS NULL OR e.chunk_text_hash != c.chunk_text_hash OR e.status = 'pending' OR (e.status = 'error' AND (e.next_attempt_at = '' OR e.next_attempt_at <= ?)))`

func (s *Store) CountChunksNeedingEmbeddingForProfileAt(ctx context.Context, profile embedding.Profile, now time.Time) (int, error) {
	profileID, err := validateEmbeddingCandidateProfile(profile)
	if err != nil {
		return 0, err
	}
	if err := s.rejectMismatchedRetrievalChunks(ctx, profile); err != nil {
		return 0, err
	}
	return s.countChunksNeedingEmbeddingAt(ctx, profileID, profile.ProjectionVersion, profile.ChunkerVersion, now)
}

func (s *Store) ListChunksNeedingEmbeddingForProfileAt(ctx context.Context, profile embedding.Profile, afterChunkID string, limit int, now time.Time) ([]RetrievalChunkRow, error) {
	profileID, err := validateEmbeddingCandidateProfile(profile)
	if err != nil {
		return nil, err
	}
	if err := s.rejectMismatchedRetrievalChunks(ctx, profile); err != nil {
		return nil, err
	}
	return s.listChunksNeedingEmbeddingAt(ctx, profileID, profile.ProjectionVersion, profile.ChunkerVersion, afterChunkID, limit, now)
}

func validateEmbeddingCandidateProfile(profile embedding.Profile) (string, error) {
	profileID, err := profile.ID()
	if err != nil {
		return "", fmt.Errorf("invalid retrieval embedding profile: %w", err)
	}
	return profileID, nil
}

func (s *Store) rejectMismatchedRetrievalChunks(ctx context.Context, profile embedding.Profile) error {
	var count int
	if err := s.queryer().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM retrieval_chunks c
		JOIN retrieval_parent_projections parent
			ON parent.parent_kind = c.parent_kind
			AND parent.parent_source_key = c.parent_source_key
			AND parent.status = 'current'
		WHERE c.projection_version != ? OR c.chunker_version != ?`, profile.ProjectionVersion, profile.ChunkerVersion).Scan(&count); err != nil {
		return fmt.Errorf("count retrieval chunks with stale provenance: %w", err)
	}
	if count > 0 {
		return &RetrievalChunkProfileMismatchError{ProjectionVersion: profile.ProjectionVersion, ChunkerVersion: profile.ChunkerVersion, Count: count}
	}
	return nil
}

func (s *Store) CountChunksNeedingEmbeddingAt(ctx context.Context, profileID string, now time.Time) (int, error) {
	return s.countChunksNeedingEmbeddingAt(ctx, profileID, "", "", now)
}

func (s *Store) countChunksNeedingEmbeddingAt(ctx context.Context, profileID, projectionVersion, chunkerVersion string, now time.Time) (int, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return 0, fmt.Errorf("retrieval embedding profile is required")
	}
	var count int
	query := `
		SELECT COUNT(*)
		FROM retrieval_chunks c
		JOIN retrieval_parent_projections parent
			ON parent.parent_kind = c.parent_kind
			AND parent.parent_source_key = c.parent_source_key
			AND parent.status = 'current'
		LEFT JOIN retrieval_embeddings e ON e.chunk_id = c.chunk_id AND e.profile_id = ?
		WHERE ` + retrievalEmbeddingDueSQL
	args := []any{profileID, now.UTC().Format(time.RFC3339)}
	if projectionVersion != "" || chunkerVersion != "" {
		query += ` AND c.projection_version = ? AND c.chunker_version = ?`
		args = append(args, projectionVersion, chunkerVersion)
	}
	err := s.queryer().QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count chunks needing embedding: %w", err)
	}
	return count, nil
}

func (s *Store) ListChunksNeedingEmbeddingAt(ctx context.Context, profileID, afterChunkID string, limit int, now time.Time) ([]RetrievalChunkRow, error) {
	return s.listChunksNeedingEmbeddingAt(ctx, profileID, "", "", afterChunkID, limit, now)
}

func (s *Store) listChunksNeedingEmbeddingAt(ctx context.Context, profileID, projectionVersion, chunkerVersion, afterChunkID string, limit int, now time.Time) ([]RetrievalChunkRow, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return nil, fmt.Errorf("retrieval embedding profile is required")
	}
	query := `
		SELECT c.chunk_id, c.parent_kind, c.parent_source_key, c.evidence_role, c.section_ordinal,
			c.ordinal, c.start_char, c.end_char, c.heading, c.projection_version, c.chunker_version,
			c.input_content_hash, c.chunk_text_hash, c.text, COALESCE(e.attempt_count, 0)
		FROM retrieval_chunks c
		JOIN retrieval_parent_projections parent
			ON parent.parent_kind = c.parent_kind
			AND parent.parent_source_key = c.parent_source_key
			AND parent.status = 'current'
		LEFT JOIN retrieval_embeddings e
			ON e.chunk_id = c.chunk_id AND e.profile_id = ?
		WHERE c.chunk_id > ? AND ` + retrievalEmbeddingDueSQL
	args := []any{profileID, afterChunkID, now.UTC().Format(time.RFC3339)}
	if projectionVersion != "" || chunkerVersion != "" {
		query += ` AND c.projection_version = ? AND c.chunker_version = ?`
		args = append(args, projectionVersion, chunkerVersion)
	}
	query += ` ORDER BY c.chunk_id`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.queryer().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list chunks needing embedding: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]RetrievalChunkRow, 0)
	for rows.Next() {
		var row RetrievalChunkRow
		err := rows.Scan(&row.ChunkID, &row.ParentKind, &row.ParentSourceKey, &row.EvidenceRole, &row.SectionOrdinal,
			&row.Ordinal, &row.StartChar, &row.EndChar, &row.Heading, &row.ProjectionVersion, &row.ChunkerVersion,
			&row.InputContentHash, &row.ChunkTextHash, &row.Text, &row.AttemptCount)
		if err != nil {
			return nil, fmt.Errorf("scan chunk needing embedding: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chunks needing embedding: %w", err)
	}
	return result, nil
}

type PutRetrievalEmbeddingBatchInput struct {
	Profile            embedding.Profile
	Rows               []RetrievalEmbeddingRow
	ExpectedPurgeEpoch int64
}

func (s *Store) PutRetrievalEmbedding(ctx context.Context, row RetrievalEmbeddingRow) error {
	var projectionVersion, chunkerVersion string
	if err := s.queryer().QueryRowContext(ctx, `
		SELECT projection_version, chunker_version
		FROM retrieval_chunks WHERE chunk_id=?`, row.ChunkID).Scan(&projectionVersion, &chunkerVersion); err != nil {
		return fmt.Errorf("load retrieval chunk profile for %s: %w", row.ChunkID, err)
	}
	profile := embedding.Profile{
		Provider: row.Provider, Model: row.Model, Dimensions: row.Dimensions,
		ProjectionVersion: projectionVersion, ChunkerVersion: chunkerVersion,
		Representation: row.Representation, Normalization: row.Normalization,
	}
	epoch, err := s.RetrievalPurgeEpoch(ctx)
	if err != nil {
		return err
	}
	_, err = s.putRetrievalEmbeddingBatch(ctx, PutRetrievalEmbeddingBatchInput{
		Profile: profile, Rows: []RetrievalEmbeddingRow{row}, ExpectedPurgeEpoch: epoch,
	}, row.ProfileID, false)
	return err
}

func (s *Store) PutRetrievalEmbeddingBatch(ctx context.Context, input PutRetrievalEmbeddingBatchInput) (int64, error) {
	profileID, err := input.Profile.ID()
	if err != nil {
		return 0, fmt.Errorf("invalid retrieval embedding profile: %w", err)
	}
	return s.putRetrievalEmbeddingBatch(ctx, input, profileID, true)
}

func (s *Store) putRetrievalEmbeddingBatch(ctx context.Context, input PutRetrievalEmbeddingBatchInput, profileID string, requireCurrentProfile bool) (int64, error) {
	rows, err := validateRetrievalEmbeddingBatch(input.Profile, profileID, input.Rows, requireCurrentProfile)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin retrieval embedding batch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var purgeEpoch int64
	if err := tx.QueryRowContext(ctx, `SELECT purge_epoch FROM retrieval_state WHERE singleton=1`).Scan(&purgeEpoch); err != nil {
		return 0, fmt.Errorf("read current retrieval purge epoch: %w", err)
	}
	if purgeEpoch != input.ExpectedPurgeEpoch {
		return 0, fmt.Errorf("%w: database is at epoch %d, expected %d", ErrRetrievalPurgeEpochChanged, purgeEpoch, input.ExpectedPurgeEpoch)
	}
	profileRow, err := ensureRetrievalEmbeddingProfileTx(ctx, tx, profileID, purgeEpoch)
	if err != nil {
		return 0, err
	}
	if err := validateRetrievalEmbeddingProfileInvariantsTx(ctx, tx, profileID, input.Profile); err != nil {
		return 0, err
	}
	for _, row := range rows {
		var currentHash, projectionVersion, chunkerVersion string
		if err := tx.QueryRowContext(ctx, `
			SELECT chunk_text_hash, projection_version, chunker_version
			FROM retrieval_chunks WHERE chunk_id=?`, row.ChunkID).Scan(&currentHash, &projectionVersion, &chunkerVersion); err != nil {
			return 0, fmt.Errorf("load current chunk for retrieval embedding %s profile %s: %w", row.ChunkID, profileID, err)
		}
		if row.ChunkTextHash != currentHash {
			return 0, retrievalEmbeddingCorruption(row.ChunkID, profileID, fmt.Sprintf("chunk text hash %q does not match current hash %q", row.ChunkTextHash, currentHash))
		}
		if projectionVersion != input.Profile.ProjectionVersion || chunkerVersion != input.Profile.ChunkerVersion {
			return 0, fmt.Errorf("retrieval chunk %s provenance %q/%q does not match profile %q/%q", row.ChunkID, projectionVersion, chunkerVersion, input.Profile.ProjectionVersion, input.Profile.ChunkerVersion)
		}
	}
	revision := profileRow.LatestRevision + 1
	now := time.Now().UTC().Format(time.RFC3339)
	l0Delta := 0
	embeddingChanged := false
	for _, row := range rows {
		var old RetrievalEmbeddingRow
		var oldRevision int64
		readErr := tx.QueryRowContext(ctx, `
			SELECT provider, model, dimensions, representation, normalization,
				vector_bytes, chunk_text_hash, status, revision
			FROM retrieval_embeddings WHERE chunk_id=? AND profile_id=?`, row.ChunkID, profileID).Scan(
			&old.Provider, &old.Model, &old.Dimensions, &old.Representation, &old.Normalization,
			&old.VectorBytes, &old.ChunkTextHash, &old.Status, &oldRevision,
		)
		if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
			return 0, fmt.Errorf("load prior retrieval embedding for chunk %s profile %s: %w", row.ChunkID, profileID, readErr)
		}
		oldL0Ready := readErr == nil && old.Status == RetrievalEmbeddingReady && oldRevision > profileRow.ActiveSnapshotRevision
		newL0Ready := row.Status == RetrievalEmbeddingReady
		if !oldL0Ready && newL0Ready {
			l0Delta++
		} else if oldL0Ready && !newL0Ready {
			l0Delta--
		}
		if errors.Is(readErr, sql.ErrNoRows) || old.Provider != row.Provider || old.Model != row.Model || old.Dimensions != row.Dimensions ||
			old.Representation != row.Representation || old.Normalization != row.Normalization || !bytes.Equal(old.VectorBytes, row.VectorBytes) ||
			old.ChunkTextHash != row.ChunkTextHash || old.Status != row.Status {
			embeddingChanged = true
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO retrieval_embeddings (
				chunk_id, profile_id, provider, model, dimensions, representation,
				normalization, vector_bytes, chunk_text_hash, status, attempt_count,
				last_error, next_attempt_at, embedded_at, revision, vector_hash, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(chunk_id, profile_id) DO UPDATE SET
				provider=excluded.provider, model=excluded.model, dimensions=excluded.dimensions,
				representation=excluded.representation, normalization=excluded.normalization,
				vector_bytes=excluded.vector_bytes, chunk_text_hash=excluded.chunk_text_hash,
				status=excluded.status, attempt_count=excluded.attempt_count,
				last_error=excluded.last_error, next_attempt_at=excluded.next_attempt_at,
				embedded_at=excluded.embedded_at, revision=excluded.revision,
				vector_hash=excluded.vector_hash, updated_at=excluded.updated_at`,
			row.ChunkID, profileID, row.Provider, row.Model, row.Dimensions, row.Representation,
			row.Normalization, row.VectorBytes, row.ChunkTextHash, row.Status, row.AttemptCount,
			row.LastError, formatOptionalTime(row.NextAttemptAt), formatOptionalTime(row.EmbeddedAt),
			revision, retrievalVectorHash(row.VectorBytes), now)
		if err != nil {
			return 0, fmt.Errorf("put retrieval embedding batch row %s profile %s: %w", row.ChunkID, profileID, err)
		}
	}
	if embeddingChanged {
		if err := markRetrievalProfileGenerationsStaleTx(ctx, tx, profileID); err != nil {
			return 0, err
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE retrieval_embedding_profiles
		SET latest_revision=?, l0_ready_count=l0_ready_count+?, updated_at=?
		WHERE profile_id=? AND latest_revision=? AND purge_epoch=?`,
		revision, l0Delta, now, profileID, profileRow.LatestRevision, purgeEpoch)
	if err != nil {
		return 0, fmt.Errorf("update retrieval embedding profile %s: %w", profileID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return 0, fmt.Errorf("retrieval embedding profile %s changed while writing batch", profileID)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit retrieval embedding batch profile %s revision %d: %w", profileID, revision, err)
	}
	return revision, nil
}

func validateRetrievalEmbeddingBatch(profile embedding.Profile, profileID string, input []RetrievalEmbeddingRow, requireCurrentProfile bool) ([]RetrievalEmbeddingRow, error) {
	if requireCurrentProfile {
		if err := profile.Validate(); err != nil {
			return nil, fmt.Errorf("invalid retrieval embedding profile: %w", err)
		}
	} else if err := (embedding.Info{Provider: profile.Provider, Model: profile.Model, Dimensions: profile.Dimensions}).Validate(); err != nil {
		return nil, fmt.Errorf("invalid retrieval embedding profile: %w", err)
	}
	if len(input) == 0 || len(input) > maxRetrievalEmbeddingBatchSize {
		return nil, fmt.Errorf("retrieval embedding batch must contain between 1 and %d rows", maxRetrievalEmbeddingBatchSize)
	}
	rows := append([]RetrievalEmbeddingRow(nil), input...)
	seen := make(map[string]struct{}, len(rows))
	for i := range rows {
		row := &rows[i]
		if strings.TrimSpace(row.ChunkID) == "" || strings.TrimSpace(row.ProfileID) == "" {
			return nil, fmt.Errorf("retrieval embedding chunk and profile are required")
		}
		if row.ProfileID != profileID {
			return nil, fmt.Errorf("retrieval embedding row profile %q does not match batch profile %q", row.ProfileID, profileID)
		}
		if _, found := seen[row.ChunkID]; found {
			return nil, fmt.Errorf("retrieval embedding batch repeats chunk %s", row.ChunkID)
		}
		seen[row.ChunkID] = struct{}{}
		if row.Provider != profile.Provider || row.Model != profile.Model || row.Dimensions != profile.Dimensions || row.Representation != profile.Representation || row.Normalization != profile.Normalization {
			return nil, fmt.Errorf("retrieval embedding row %s invariants do not match batch profile", row.ChunkID)
		}
		if row.Status == "" {
			row.Status = RetrievalEmbeddingPending
		}
		if !validRetrievalEmbeddingStatus(row.Status) {
			return nil, fmt.Errorf("invalid retrieval embedding status %q", row.Status)
		}
		if row.Status == RetrievalEmbeddingReady {
			if err := embedding.ValidateEncodedVector(row.VectorBytes, row.Dimensions, row.Representation, row.Normalization); err != nil {
				return nil, retrievalEmbeddingCorruption(row.ChunkID, row.ProfileID, err.Error())
			}
		} else if row.VectorBytes == nil {
			row.VectorBytes = []byte{}
		}
	}
	return rows, nil
}

func validateRetrievalEmbeddingProfileInvariantsTx(ctx context.Context, tx *sql.Tx, profileID string, profile embedding.Profile) error {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM retrieval_embeddings
		WHERE profile_id=? AND (provider!=? OR model!=? OR dimensions!=? OR representation!=? OR normalization!=?)`,
		profileID, profile.Provider, profile.Model, profile.Dimensions, profile.Representation, profile.Normalization).Scan(&count); err != nil {
		return fmt.Errorf("validate retrieval embedding profile %s invariants: %w", profileID, err)
	}
	if count != 0 {
		return fmt.Errorf("retrieval embedding profile %s invariants do not match", profileID)
	}
	return nil
}

func (s *Store) ListReadyEmbeddings(ctx context.Context, profileID string, limit int) ([]RetrievalEmbeddingRow, error) {
	query := `
		SELECT e.chunk_id, e.profile_id, e.provider, e.model, e.dimensions,
			e.representation, e.normalization, e.vector_bytes, e.vector_hash, e.chunk_text_hash, e.revision,
			e.status, e.attempt_count, e.last_error, e.next_attempt_at, e.embedded_at,
			c.parent_kind, c.parent_source_key, c.evidence_role,
			CASE WHEN c.parent_kind = 'source' THEN COALESCE(s.source_type, '') ELSE COALESCE(i.source_type, '') END,
			c.section_ordinal, c.text, c.projection_version, c.chunker_version, c.chunk_text_hash
		FROM retrieval_embeddings e
		JOIN retrieval_chunks c ON c.chunk_id = e.chunk_id
		JOIN retrieval_parent_projections parent
			ON parent.parent_kind = c.parent_kind
			AND parent.parent_source_key = c.parent_source_key
			AND parent.status = 'current'
		LEFT JOIN items i ON c.parent_kind = 'item' AND i.source_key = c.parent_source_key
		LEFT JOIN sources s ON c.parent_kind = 'source' AND s.source_key = c.parent_source_key
		WHERE e.profile_id = ? AND e.status = 'ready'
		ORDER BY e.chunk_id`
	args := []any{profileID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.queryer().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list ready retrieval embeddings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]RetrievalEmbeddingRow, 0)
	for rows.Next() {
		var row RetrievalEmbeddingRow
		var nextAttemptAt, embeddedAt, currentChunkTextHash string
		if err := rows.Scan(&row.ChunkID, &row.ProfileID, &row.Provider, &row.Model, &row.Dimensions,
			&row.Representation, &row.Normalization, &row.VectorBytes, &row.VectorHash, &row.ChunkTextHash, &row.Revision,
			&row.Status, &row.AttemptCount, &row.LastError, &nextAttemptAt, &embeddedAt,
			&row.ParentKind, &row.ParentSourceKey, &row.EvidenceRole, &row.SourceType,
			&row.SectionOrdinal, &row.Text, &row.ProjectionVersion, &row.ChunkerVersion, &currentChunkTextHash); err != nil {
			return nil, fmt.Errorf("scan ready retrieval embedding: %w", err)
		}
		if reason := retrievalEmbeddingCorruptionReason(row, currentChunkTextHash); reason != "" {
			return nil, retrievalEmbeddingCorruption(row.ChunkID, row.ProfileID, reason)
		}
		row.NextAttemptAt = parseStoredTime(nextAttemptAt)
		row.EmbeddedAt = parseStoredTime(embeddedAt)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ready retrieval embeddings: %w", err)
	}
	return result, nil
}

// BlockCorruptRetrievalEmbedding explicitly revalidates and transitions the
// row identified by a ListReadyEmbeddings diagnostic. Keeping it separate makes
// read-only consumers non-mutating. Revalidation inside the write transaction
// prevents a stale diagnostic from blocking a row repaired in the meantime.
func (s *Store) BlockCorruptRetrievalEmbedding(ctx context.Context, corruption *RetrievalEmbeddingCorruptionError) error {
	if corruption == nil {
		return fmt.Errorf("retrieval embedding corruption diagnostic is required")
	}
	chunkID := strings.TrimSpace(corruption.ChunkID)
	profileID := strings.TrimSpace(corruption.ProfileID)
	if chunkID == "" || profileID == "" || strings.TrimSpace(corruption.Reason) == "" {
		return fmt.Errorf("corrupt retrieval embedding chunk, profile, and reason are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin corrupt retrieval embedding transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var row RetrievalEmbeddingRow
	var currentChunkTextHash string
	if err := tx.QueryRowContext(ctx, `
		SELECT e.provider, e.model, e.dimensions, e.representation, e.normalization,
			e.vector_bytes, e.vector_hash, e.chunk_text_hash, e.status, e.revision, c.chunk_text_hash
		FROM retrieval_embeddings e
		JOIN retrieval_chunks c ON c.chunk_id = e.chunk_id
		WHERE e.chunk_id = ? AND e.profile_id = ?`, chunkID, profileID).Scan(
		&row.Provider, &row.Model, &row.Dimensions, &row.Representation, &row.Normalization,
		&row.VectorBytes, &row.VectorHash, &row.ChunkTextHash, &row.Status, &row.Revision, &currentChunkTextHash,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: chunk %s profile %s was removed", ErrRetrievalEmbeddingNoLongerCorrupt, chunkID, profileID)
		}
		return fmt.Errorf("reload corrupt retrieval embedding for chunk %s profile %s: %w", chunkID, profileID, err)
	}
	if row.Status != RetrievalEmbeddingReady {
		return fmt.Errorf("%w: chunk %s profile %s status is %q", ErrRetrievalEmbeddingNoLongerCorrupt, chunkID, profileID, row.Status)
	}
	reason := retrievalEmbeddingCorruptionReason(row, currentChunkTextHash)
	if reason == "" {
		return fmt.Errorf("%w: chunk %s profile %s now passes validation", ErrRetrievalEmbeddingNoLongerCorrupt, chunkID, profileID)
	}
	var purgeEpoch int64
	if err := tx.QueryRowContext(ctx, `SELECT purge_epoch FROM retrieval_state WHERE singleton=1`).Scan(&purgeEpoch); err != nil {
		return fmt.Errorf("read retrieval purge epoch while blocking corrupt embedding: %w", err)
	}
	profileRow, err := ensureRetrievalEmbeddingProfileTx(ctx, tx, profileID, purgeEpoch)
	if err != nil {
		return err
	}
	revision := profileRow.LatestRevision + 1
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := tx.ExecContext(ctx, `
		UPDATE retrieval_embeddings
		SET status = 'blocked', last_error = ?, next_attempt_at = '', revision = ?, updated_at = ?
		WHERE chunk_id = ? AND profile_id = ? AND status = 'ready'`,
		"corrupt: "+reason, revision, now, chunkID, profileID)
	if err != nil {
		return fmt.Errorf("block corrupt retrieval embedding for chunk %s profile %s: %w", chunkID, profileID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count blocked corrupt retrieval embedding for chunk %s profile %s: %w", chunkID, profileID, err)
	}
	if affected != 1 {
		return fmt.Errorf("ready retrieval embedding for chunk %s profile %s was not found", chunkID, profileID)
	}
	l0Delta := 0
	if row.Revision > profileRow.ActiveSnapshotRevision {
		l0Delta = -1
	}
	profileResult, err := tx.ExecContext(ctx, `
		UPDATE retrieval_embedding_profiles
		SET latest_revision=?, l0_ready_count=MAX(l0_ready_count+?, 0), updated_at=?
		WHERE profile_id=? AND latest_revision=? AND purge_epoch=?`,
		revision, l0Delta, now, profileID, profileRow.LatestRevision, purgeEpoch)
	if err != nil {
		return fmt.Errorf("update retrieval embedding profile after corruption %s: %w", profileID, err)
	}
	profileAffected, err := profileResult.RowsAffected()
	if err != nil || profileAffected != 1 {
		return fmt.Errorf("retrieval embedding profile %s changed while blocking corruption", profileID)
	}
	if err := markRetrievalProfileGenerationsStaleTx(ctx, tx, profileID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit corrupt retrieval embedding transition for chunk %s profile %s: %w", chunkID, profileID, err)
	}
	return nil
}

func retrievalEmbeddingCorruption(chunkID, profileID, reason string) error {
	return &RetrievalEmbeddingCorruptionError{ChunkID: chunkID, ProfileID: profileID, Reason: reason}
}

func retrievalEmbeddingCorruptionReason(row RetrievalEmbeddingRow, currentChunkTextHash string) string {
	if err := (embedding.Info{
		Provider: row.Provider, Model: row.Model, Dimensions: row.Dimensions,
	}).Validate(); err != nil {
		return err.Error()
	}
	if row.ChunkTextHash != currentChunkTextHash {
		return fmt.Sprintf("chunk text hash %q does not match current hash %q", row.ChunkTextHash, currentChunkTextHash)
	}
	if err := embedding.ValidateEncodedVector(
		row.VectorBytes, row.Dimensions, row.Representation, row.Normalization,
	); err != nil {
		return err.Error()
	}
	if got := retrievalVectorHash(row.VectorBytes); got != row.VectorHash {
		return fmt.Sprintf("vector hash %q does not match stored bytes hash %q", row.VectorHash, got)
	}
	return ""
}

func retrievalVectorHash(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func validRetrievalEmbeddingStatus(status RetrievalEmbeddingStatus) bool {
	switch status {
	case RetrievalEmbeddingPending, RetrievalEmbeddingReady, RetrievalEmbeddingBlocked, RetrievalEmbeddingError:
		return true
	default:
		return false
	}
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
