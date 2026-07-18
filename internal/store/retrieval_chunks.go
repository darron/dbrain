package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/retrievalchunk"
)

type ChunkReplaceResult struct {
	Created int `json:"created"`
	Reused  int `json:"reused"`
	Deleted int `json:"deleted"`
}

type RetrievalChunkRow struct {
	ChunkID          string
	ParentKind       string
	ParentSourceKey  string
	EvidenceRole     string
	SectionOrdinal   int
	Ordinal          int
	StartChar        int
	EndChar          int
	Heading          string
	ChunkerVersion   string
	InputContentHash string
	ChunkTextHash    string
	Text             string
}

func (s *Store) RetrievalAvailable(ctx context.Context) (bool, error) {
	for _, table := range []string{"retrieval_chunks", "retrieval_embeddings", "retrieval_index_generations"} {
		exists, err := s.tableExistsContext(ctx, table)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) ReplaceRetrievalChunks(ctx context.Context, parentKind, parentSourceKey string, chunks []retrievalchunk.Chunk) (ChunkReplaceResult, error) {
	parentKind = strings.TrimSpace(parentKind)
	parentSourceKey = strings.TrimSpace(parentSourceKey)
	if parentKind == "" || parentSourceKey == "" {
		return ChunkReplaceResult{}, fmt.Errorf("retrieval parent kind and source key are required")
	}
	seen := make(map[string]struct{}, len(chunks))
	incomingHashes := make(map[string]string, len(chunks))
	for _, chunk := range chunks {
		if chunk.ID == "" || chunk.ParentKind != parentKind || chunk.ParentSourceKey != parentSourceKey {
			return ChunkReplaceResult{}, fmt.Errorf("chunk %q does not belong to retrieval parent %s %s", chunk.ID, parentKind, parentSourceKey)
		}
		if _, duplicate := seen[chunk.ID]; duplicate {
			return ChunkReplaceResult{}, fmt.Errorf("duplicate retrieval chunk ID %q", chunk.ID)
		}
		seen[chunk.ID] = struct{}{}
		incomingHashes[chunk.ID] = chunk.TextHash
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("begin retrieval chunk replacement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing := make(map[string]string)
	rows, err := tx.QueryContext(ctx, `
		SELECT chunk_id, chunk_text_hash
		FROM retrieval_chunks
		WHERE parent_kind = ? AND parent_source_key = ?`, parentKind, parentSourceKey)
	if err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("list existing retrieval chunks: %w", err)
	}
	for rows.Next() {
		var id, textHash string
		if err := rows.Scan(&id, &textHash); err != nil {
			_ = rows.Close()
			return ChunkReplaceResult{}, fmt.Errorf("scan existing retrieval chunk: %w", err)
		}
		existing[id] = textHash
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ChunkReplaceResult{}, fmt.Errorf("iterate existing retrieval chunks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("close existing retrieval chunks: %w", err)
	}

	result := ChunkReplaceResult{}
	affectedProfiles := make(map[string]struct{})
	rows, err = tx.QueryContext(ctx, `
		SELECT DISTINCT e.profile_id, c.chunk_id, c.chunk_text_hash
		FROM retrieval_chunks c
		JOIN retrieval_embeddings e ON e.chunk_id = c.chunk_id
		WHERE c.parent_kind = ? AND c.parent_source_key = ?`, parentKind, parentSourceKey)
	if err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("list affected retrieval profiles: %w", err)
	}
	for rows.Next() {
		var profileID, chunkID, oldHash string
		if err := rows.Scan(&profileID, &chunkID, &oldHash); err != nil {
			_ = rows.Close()
			return ChunkReplaceResult{}, fmt.Errorf("scan affected retrieval profile: %w", err)
		}
		newHash, keep := incomingHashes[chunkID]
		if !keep || newHash != oldHash {
			affectedProfiles[profileID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ChunkReplaceResult{}, fmt.Errorf("iterate affected retrieval profiles: %w", err)
	}
	if err := rows.Close(); err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("close affected retrieval profiles: %w", err)
	}
	for profileID := range affectedProfiles {
		if err := markRetrievalProfileGenerationsStaleTx(ctx, tx, profileID); err != nil {
			return ChunkReplaceResult{}, err
		}
	}
	for id := range existing {
		if _, keep := seen[id]; keep {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM retrieval_chunks WHERE chunk_id = ?`, id); err != nil {
			return ChunkReplaceResult{}, fmt.Errorf("delete stale retrieval chunk %s: %w", id, err)
		}
		result.Deleted++
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, chunk := range chunks {
		oldHash, existed := existing[chunk.ID]
		if existed && oldHash == chunk.TextHash {
			result.Reused++
		} else {
			result.Created++
		}
		writeResult, err := tx.ExecContext(ctx, `
			INSERT INTO retrieval_chunks (
				chunk_id, parent_kind, parent_source_key, evidence_role, section_ordinal,
				ordinal, start_char, end_char, heading, chunker_version,
				input_content_hash, chunk_text_hash, text, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(chunk_id) DO UPDATE SET
				evidence_role = excluded.evidence_role,
				section_ordinal = excluded.section_ordinal,
				ordinal = excluded.ordinal,
				start_char = excluded.start_char,
				end_char = excluded.end_char,
				heading = excluded.heading,
				chunker_version = excluded.chunker_version,
				input_content_hash = excluded.input_content_hash,
				chunk_text_hash = excluded.chunk_text_hash,
				text = excluded.text,
				updated_at = excluded.updated_at
			WHERE retrieval_chunks.parent_kind = excluded.parent_kind
				AND retrieval_chunks.parent_source_key = excluded.parent_source_key`,
			chunk.ID, parentKind, parentSourceKey, chunk.EvidenceRole, chunk.SectionOrdinal,
			chunk.Ordinal, chunk.StartChar, chunk.EndChar, chunk.Heading, chunk.ChunkerVersion,
			chunk.InputContentHash, chunk.TextHash, chunk.Text, now, now)
		if err != nil {
			return ChunkReplaceResult{}, fmt.Errorf("write retrieval chunk %s: %w", chunk.ID, err)
		}
		affected, err := writeResult.RowsAffected()
		if err != nil {
			return ChunkReplaceResult{}, fmt.Errorf("inspect retrieval chunk write %s: %w", chunk.ID, err)
		}
		if affected != 1 {
			return ChunkReplaceResult{}, fmt.Errorf("retrieval chunk ID %q already belongs to another parent", chunk.ID)
		}
		if existed && oldHash != chunk.TextHash {
			if _, err := tx.ExecContext(ctx, `DELETE FROM retrieval_embeddings WHERE chunk_id = ?`, chunk.ID); err != nil {
				return ChunkReplaceResult{}, fmt.Errorf("invalidate changed retrieval chunk %s embeddings: %w", chunk.ID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("commit retrieval chunk replacement: %w", err)
	}
	return result, nil
}

func (s *Store) GetRetrievalChunk(ctx context.Context, chunkID string) (RetrievalChunkRow, error) {
	row := s.queryer().QueryRowContext(ctx, `
		SELECT chunk_id, parent_kind, parent_source_key, evidence_role, section_ordinal,
			ordinal, start_char, end_char, heading, chunker_version,
			input_content_hash, chunk_text_hash, text
		FROM retrieval_chunks WHERE chunk_id = ?`, chunkID)
	chunk, err := scanRetrievalChunk(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RetrievalChunkRow{}, fmt.Errorf("retrieval chunk not found: %s", chunkID)
	}
	if err != nil {
		return RetrievalChunkRow{}, fmt.Errorf("load retrieval chunk %s: %w", chunkID, err)
	}
	return chunk, nil
}

func scanRetrievalChunk(scanner interface{ Scan(...any) error }) (RetrievalChunkRow, error) {
	var row RetrievalChunkRow
	err := scanner.Scan(&row.ChunkID, &row.ParentKind, &row.ParentSourceKey, &row.EvidenceRole, &row.SectionOrdinal,
		&row.Ordinal, &row.StartChar, &row.EndChar, &row.Heading, &row.ChunkerVersion,
		&row.InputContentHash, &row.ChunkTextHash, &row.Text)
	return row, err
}
