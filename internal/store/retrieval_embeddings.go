package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/embedding"
)

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
	ChunkID         string
	ProfileID       string
	Provider        string
	Model           string
	Dimensions      int
	Representation  string
	Normalization   string
	VectorBytes     []byte
	ChunkTextHash   string
	Status          RetrievalEmbeddingStatus
	AttemptCount    int
	LastError       string
	NextAttemptAt   time.Time
	EmbeddedAt      time.Time
	ParentKind      string
	ParentSourceKey string
	EvidenceRole    string
	Text            string
}

type RetrievalEmbeddingCorruptionError struct {
	ChunkID   string
	ProfileID string
	Reason    string
}

func (e *RetrievalEmbeddingCorruptionError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("corrupt ready retrieval embedding for chunk %s profile %s: %s", e.ChunkID, e.ProfileID, e.Reason)
}

func (s *Store) ListChunksNeedingEmbedding(ctx context.Context, profileID, afterChunkID string, limit int) ([]RetrievalChunkRow, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return nil, fmt.Errorf("retrieval embedding profile is required")
	}
	query := `
		SELECT c.chunk_id, c.parent_kind, c.parent_source_key, c.evidence_role, c.section_ordinal,
			c.ordinal, c.start_char, c.end_char, c.heading, c.chunker_version,
			c.input_content_hash, c.chunk_text_hash, c.text
		FROM retrieval_chunks c
		LEFT JOIN retrieval_embeddings e
			ON e.chunk_id = c.chunk_id AND e.profile_id = ?
		WHERE c.chunk_id > ?
			AND (e.chunk_id IS NULL OR e.chunk_text_hash != c.chunk_text_hash OR e.status IN ('pending', 'error'))
		ORDER BY c.chunk_id`
	args := []any{profileID, afterChunkID}
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
		row, err := scanRetrievalChunk(rows)
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

func (s *Store) PutRetrievalEmbedding(ctx context.Context, row RetrievalEmbeddingRow) error {
	if strings.TrimSpace(row.ChunkID) == "" || strings.TrimSpace(row.ProfileID) == "" {
		return fmt.Errorf("retrieval embedding chunk and profile are required")
	}
	if row.Dimensions <= 0 {
		return fmt.Errorf("retrieval embedding dimensions must be positive")
	}
	if row.Status == "" {
		row.Status = RetrievalEmbeddingPending
	}
	if !validRetrievalEmbeddingStatus(row.Status) {
		return fmt.Errorf("invalid retrieval embedding status %q", row.Status)
	}
	if row.Status == RetrievalEmbeddingReady {
		if err := (embedding.Info{
			Provider: row.Provider, Model: row.Model, Dimensions: row.Dimensions,
		}).Validate(); err != nil {
			return retrievalEmbeddingCorruption(row.ChunkID, row.ProfileID, err.Error())
		}
		if err := embedding.ValidateEncodedVector(
			row.VectorBytes, row.Dimensions, row.Representation, row.Normalization,
		); err != nil {
			return retrievalEmbeddingCorruption(row.ChunkID, row.ProfileID, err.Error())
		}
	} else if row.VectorBytes == nil {
		row.VectorBytes = []byte{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin retrieval embedding write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if row.Status == RetrievalEmbeddingReady {
		var currentChunkTextHash string
		if err := tx.QueryRowContext(ctx, `SELECT chunk_text_hash FROM retrieval_chunks WHERE chunk_id = ?`, row.ChunkID).Scan(&currentChunkTextHash); err != nil {
			return fmt.Errorf("load current chunk hash for ready retrieval embedding %s profile %s: %w", row.ChunkID, row.ProfileID, err)
		}
		if row.ChunkTextHash != currentChunkTextHash {
			return retrievalEmbeddingCorruption(row.ChunkID, row.ProfileID, fmt.Sprintf("chunk text hash %q does not match current hash %q", row.ChunkTextHash, currentChunkTextHash))
		}
	}
	var profileProvider, profileModel, profileRepresentation, profileNormalization string
	var profileDimensions int
	profileErr := tx.QueryRowContext(ctx, `
		SELECT provider, model, dimensions, representation, normalization
		FROM retrieval_embeddings
		WHERE profile_id = ?
		ORDER BY chunk_id
		LIMIT 1`, row.ProfileID).Scan(
		&profileProvider, &profileModel, &profileDimensions, &profileRepresentation, &profileNormalization,
	)
	if profileErr != nil && !errors.Is(profileErr, sql.ErrNoRows) {
		return fmt.Errorf("load retrieval embedding profile %s invariants: %w", row.ProfileID, profileErr)
	}
	if profileErr == nil && (profileProvider != row.Provider || profileModel != row.Model ||
		profileDimensions != row.Dimensions || profileRepresentation != row.Representation ||
		profileNormalization != row.Normalization) {
		return fmt.Errorf("retrieval embedding profile %s invariants do not match", row.ProfileID)
	}
	var oldDimensions int
	var oldProvider, oldModel, oldRepresentation, oldNormalization, oldTextHash string
	var oldStatus RetrievalEmbeddingStatus
	var oldVector []byte
	readErr := tx.QueryRowContext(ctx, `
		SELECT provider, model, dimensions, representation, normalization, vector_bytes, chunk_text_hash, status
		FROM retrieval_embeddings WHERE chunk_id = ? AND profile_id = ?`, row.ChunkID, row.ProfileID).Scan(
		&oldProvider, &oldModel, &oldDimensions, &oldRepresentation, &oldNormalization, &oldVector, &oldTextHash, &oldStatus,
	)
	if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
		return fmt.Errorf("load prior retrieval embedding for chunk %s profile %s: %w", row.ChunkID, row.ProfileID, readErr)
	}
	embeddingChanged := errors.Is(readErr, sql.ErrNoRows) ||
		oldProvider != row.Provider || oldModel != row.Model || oldDimensions != row.Dimensions || oldRepresentation != row.Representation ||
		oldNormalization != row.Normalization || !bytes.Equal(oldVector, row.VectorBytes) ||
		oldTextHash != row.ChunkTextHash || oldStatus != row.Status
	if embeddingChanged {
		if err := markRetrievalProfileGenerationsStaleTx(ctx, tx, row.ProfileID); err != nil {
			return err
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO retrieval_embeddings (
			chunk_id, profile_id, provider, model, dimensions, representation,
			normalization, vector_bytes, chunk_text_hash, status, attempt_count,
			last_error, next_attempt_at, embedded_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chunk_id, profile_id) DO UPDATE SET
			provider = excluded.provider,
			model = excluded.model,
			dimensions = excluded.dimensions,
			representation = excluded.representation,
			normalization = excluded.normalization,
			vector_bytes = excluded.vector_bytes,
			chunk_text_hash = excluded.chunk_text_hash,
			status = excluded.status,
			attempt_count = excluded.attempt_count,
			last_error = excluded.last_error,
			next_attempt_at = excluded.next_attempt_at,
			embedded_at = excluded.embedded_at,
			updated_at = excluded.updated_at`,
		row.ChunkID, row.ProfileID, row.Provider, row.Model, row.Dimensions, row.Representation,
		row.Normalization, row.VectorBytes, row.ChunkTextHash, row.Status, row.AttemptCount,
		row.LastError, formatOptionalTime(row.NextAttemptAt), formatOptionalTime(row.EmbeddedAt), now)
	if err != nil {
		return fmt.Errorf("put retrieval embedding for chunk %s profile %s: %w", row.ChunkID, row.ProfileID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit retrieval embedding for chunk %s profile %s: %w", row.ChunkID, row.ProfileID, err)
	}
	return nil
}

func (s *Store) ListReadyEmbeddings(ctx context.Context, profileID string, limit int) ([]RetrievalEmbeddingRow, error) {
	query := `
		SELECT e.chunk_id, e.profile_id, e.provider, e.model, e.dimensions,
			e.representation, e.normalization, e.vector_bytes, e.chunk_text_hash,
			e.status, e.attempt_count, e.last_error, e.next_attempt_at, e.embedded_at,
			c.parent_kind, c.parent_source_key, c.evidence_role, c.text, c.chunk_text_hash
		FROM retrieval_embeddings e
		JOIN retrieval_chunks c ON c.chunk_id = e.chunk_id
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
			&row.Representation, &row.Normalization, &row.VectorBytes, &row.ChunkTextHash,
			&row.Status, &row.AttemptCount, &row.LastError, &nextAttemptAt, &embeddedAt,
			&row.ParentKind, &row.ParentSourceKey, &row.EvidenceRole, &row.Text, &currentChunkTextHash); err != nil {
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
			e.vector_bytes, e.chunk_text_hash, e.status, c.chunk_text_hash
		FROM retrieval_embeddings e
		JOIN retrieval_chunks c ON c.chunk_id = e.chunk_id
		WHERE e.chunk_id = ? AND e.profile_id = ?`, chunkID, profileID).Scan(
		&row.Provider, &row.Model, &row.Dimensions, &row.Representation, &row.Normalization,
		&row.VectorBytes, &row.ChunkTextHash, &row.Status, &currentChunkTextHash,
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
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := tx.ExecContext(ctx, `
		UPDATE retrieval_embeddings
		SET status = 'blocked', last_error = ?, next_attempt_at = '', updated_at = ?
		WHERE chunk_id = ? AND profile_id = ? AND status = 'ready'`,
		"corrupt: "+reason, now, chunkID, profileID)
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
	return ""
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
