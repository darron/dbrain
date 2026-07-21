package store

import (
	"context"
	"fmt"
	"strings"
)

type VectorPage struct {
	AfterChunkID string
	Limit        int
}

type RetrievalVectorRow struct {
	ChunkID, ProfileID            string
	Provider, Model               string
	Dimensions                    int
	Representation, Normalization string
	VectorBytes                   []byte
	VectorHash, ChunkTextHash     string
	CurrentChunkTextHash          string
	Revision                      int64
}

func (s *Store) ListRetrievalVectors(ctx context.Context, profileID string, page VectorPage) ([]RetrievalVectorRow, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return nil, fmt.Errorf("retrieval embedding profile is required")
	}
	if page.Limit <= 0 || page.Limit > maxRetrievalEmbeddingBatchSize {
		return nil, fmt.Errorf("retrieval vector page limit must be between 1 and %d", maxRetrievalEmbeddingBatchSize)
	}
	rows, err := s.queryer().QueryContext(ctx, `
		SELECT e.chunk_id, e.profile_id, e.provider, e.model, e.dimensions,
			e.representation, e.normalization, e.vector_bytes, e.vector_hash,
			e.chunk_text_hash, c.chunk_text_hash, e.revision
		FROM retrieval_embeddings e
		JOIN retrieval_chunks c ON c.chunk_id=e.chunk_id
		WHERE e.profile_id=? AND e.status='ready' AND e.chunk_id>?
		ORDER BY e.chunk_id
		LIMIT ?`, profileID, page.AfterChunkID, page.Limit)
	if err != nil {
		return nil, fmt.Errorf("list retrieval vectors: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]RetrievalVectorRow, 0, page.Limit)
	for rows.Next() {
		var row RetrievalVectorRow
		if err := rows.Scan(&row.ChunkID, &row.ProfileID, &row.Provider, &row.Model, &row.Dimensions,
			&row.Representation, &row.Normalization, &row.VectorBytes, &row.VectorHash,
			&row.ChunkTextHash, &row.CurrentChunkTextHash, &row.Revision); err != nil {
			return nil, fmt.Errorf("scan retrieval vector: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retrieval vectors: %w", err)
	}
	return result, nil
}
