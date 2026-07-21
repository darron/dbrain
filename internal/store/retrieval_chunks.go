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
	Updated int `json:"updated"`
	Deleted int `json:"deleted"`
}

type RetrievalChunkRow struct {
	ChunkID           string
	ParentKind        string
	ParentSourceKey   string
	EvidenceRole      string
	SectionOrdinal    int
	Ordinal           int
	StartChar         int
	EndChar           int
	Heading           string
	ProjectionVersion string
	ChunkerVersion    string
	InputContentHash  string
	ChunkTextHash     string
	Text              string
	// AttemptCount is populated by embedding candidate selectors. Ordinary
	// chunk reads leave it zero.
	AttemptCount int
}

// RetrievalChunkEvidenceRow hydrates a chunk with its current parent evidence.
// Parents whose rendered note has been purged are deliberately absent.
type RetrievalChunkEvidenceRow struct {
	ChunkID         string
	ParentKind      string
	ParentSourceKey string
	EvidenceRole    string
	Ordinal         int
	StartChar       int
	EndChar         int
	Heading         string
	ChunkTextHash   string
	Text            string
	Title           string
	URL             string
	NotePath        string
	Summary         string
	Author          string
	SourceType      string
	PublishedAt     string
	ExtractedAt     string
	SummarizedAt    string
	UserTags        string
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
	incomingChunks := make(map[string]retrievalchunk.Chunk, len(chunks))
	for _, chunk := range chunks {
		if chunk.ID == "" || chunk.ParentKind != parentKind || chunk.ParentSourceKey != parentSourceKey {
			return ChunkReplaceResult{}, fmt.Errorf("chunk %q does not belong to retrieval parent %s %s", chunk.ID, parentKind, parentSourceKey)
		}
		if _, duplicate := seen[chunk.ID]; duplicate {
			return ChunkReplaceResult{}, fmt.Errorf("duplicate retrieval chunk ID %q", chunk.ID)
		}
		seen[chunk.ID] = struct{}{}
		incomingChunks[chunk.ID] = chunk
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("begin retrieval chunk replacement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing := make(map[string]RetrievalChunkRow)
	rows, err := tx.QueryContext(ctx, `
		SELECT chunk_id, parent_kind, parent_source_key, evidence_role, section_ordinal,
			ordinal, start_char, end_char, heading, projection_version, chunker_version,
			input_content_hash, chunk_text_hash, text
		FROM retrieval_chunks
		WHERE parent_kind = ? AND parent_source_key = ?`, parentKind, parentSourceKey)
	if err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("list existing retrieval chunks: %w", err)
	}
	for rows.Next() {
		row, err := scanRetrievalChunk(rows)
		if err != nil {
			_ = rows.Close()
			return ChunkReplaceResult{}, fmt.Errorf("scan existing retrieval chunk: %w", err)
		}
		existing[row.ChunkID] = row
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
		incoming, keep := incomingChunks[chunkID]
		old := existing[chunkID]
		if !keep || oldHash != incoming.TextHash || old.ProjectionVersion != incoming.ProjectionVersion || old.ChunkerVersion != incoming.ChunkerVersion {
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
		old, existed := existing[chunk.ID]
		if existed && retrievalChunkMatches(old, chunk) {
			result.Reused++
			continue
		} else if existed && old.ChunkTextHash == chunk.TextHash {
			result.Updated++
		} else {
			result.Created++
		}
		writeResult, err := tx.ExecContext(ctx, `
			INSERT INTO retrieval_chunks (
				chunk_id, parent_kind, parent_source_key, evidence_role, section_ordinal,
				ordinal, start_char, end_char, heading, projection_version, chunker_version,
				input_content_hash, chunk_text_hash, text, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(chunk_id) DO UPDATE SET
				evidence_role = excluded.evidence_role,
				section_ordinal = excluded.section_ordinal,
				ordinal = excluded.ordinal,
				start_char = excluded.start_char,
				end_char = excluded.end_char,
				heading = excluded.heading,
				projection_version = excluded.projection_version,
				chunker_version = excluded.chunker_version,
				input_content_hash = excluded.input_content_hash,
				chunk_text_hash = excluded.chunk_text_hash,
				text = excluded.text,
				updated_at = excluded.updated_at
			WHERE retrieval_chunks.parent_kind = excluded.parent_kind
				AND retrieval_chunks.parent_source_key = excluded.parent_source_key`,
			chunk.ID, parentKind, parentSourceKey, chunk.EvidenceRole, chunk.SectionOrdinal,
			chunk.Ordinal, chunk.StartChar, chunk.EndChar, chunk.Heading, chunk.ProjectionVersion, chunk.ChunkerVersion,
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
		if existed && (old.ChunkTextHash != chunk.TextHash || old.ProjectionVersion != chunk.ProjectionVersion || old.ChunkerVersion != chunk.ChunkerVersion) {
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

func replaceRetrievalProjectionTx(ctx context.Context, tx *sql.Tx, parentKind, parentSourceKey string, projection retrievalchunk.Projection) (ChunkReplaceResult, error) {
	chunks := append([]retrievalchunk.Chunk(nil), projection.Chunks...)
	occurrences := append([]retrievalchunk.Occurrence(nil), projection.Occurrences...)
	incoming := make(map[string]retrievalchunk.Chunk, len(chunks))
	firstOccurrence := make(map[string]retrievalchunk.Occurrence, len(chunks))
	for ordinal, chunk := range chunks {
		if chunk.ID == "" || chunk.ParentKind != parentKind || chunk.ParentSourceKey != parentSourceKey {
			return ChunkReplaceResult{}, fmt.Errorf("chunk %q does not belong to retrieval parent %s %s", chunk.ID, parentKind, parentSourceKey)
		}
		if strings.TrimSpace(chunk.SectionKey) == "" {
			return ChunkReplaceResult{}, fmt.Errorf("retrieval chunk %q section key is required", chunk.ID)
		}
		if _, duplicate := incoming[chunk.ID]; duplicate {
			return ChunkReplaceResult{}, fmt.Errorf("duplicate retrieval chunk ID %q", chunk.ID)
		}
		chunk.Ordinal = ordinal
		chunks[ordinal] = chunk
		incoming[chunk.ID] = chunk
	}
	seenOccurrences := make(map[string]struct{}, len(occurrences))
	for _, occurrence := range occurrences {
		chunk, ok := incoming[occurrence.ChunkID]
		if !ok {
			return ChunkReplaceResult{}, fmt.Errorf("retrieval occurrence references unknown chunk %q", occurrence.ChunkID)
		}
		if occurrence.SectionKey != chunk.SectionKey {
			return ChunkReplaceResult{}, fmt.Errorf("retrieval occurrence section %q does not match chunk %q section %q", occurrence.SectionKey, occurrence.ChunkID, chunk.SectionKey)
		}
		if occurrence.StartChar < 0 || occurrence.EndChar <= occurrence.StartChar {
			return ChunkReplaceResult{}, fmt.Errorf("invalid retrieval occurrence offsets %d:%d for chunk %q", occurrence.StartChar, occurrence.EndChar, occurrence.ChunkID)
		}
		identity := fmt.Sprintf("%s\x00%s\x00%d\x00%d", occurrence.ChunkID, occurrence.SectionKey, occurrence.StartChar, occurrence.EndChar)
		if _, duplicate := seenOccurrences[identity]; duplicate {
			return ChunkReplaceResult{}, fmt.Errorf("duplicate retrieval occurrence for chunk %q section %q offsets %d:%d", occurrence.ChunkID, occurrence.SectionKey, occurrence.StartChar, occurrence.EndChar)
		}
		seenOccurrences[identity] = struct{}{}
		if _, found := firstOccurrence[occurrence.ChunkID]; !found {
			firstOccurrence[occurrence.ChunkID] = occurrence
		}
	}
	for ordinal, chunk := range chunks {
		occurrence, ok := firstOccurrence[chunk.ID]
		if !ok {
			return ChunkReplaceResult{}, fmt.Errorf("retrieval chunk %q has no occurrence", chunk.ID)
		}
		chunk.StartChar = occurrence.StartChar
		chunk.EndChar = occurrence.EndChar
		chunks[ordinal] = chunk
		incoming[chunk.ID] = chunk
	}

	existing := make(map[string]RetrievalChunkRow)
	rows, err := tx.QueryContext(ctx, `
		SELECT chunk_id, parent_kind, parent_source_key, evidence_role, section_ordinal,
			ordinal, start_char, end_char, heading, projection_version, chunker_version,
			input_content_hash, chunk_text_hash, text
		FROM retrieval_chunks
		WHERE parent_kind = ? AND parent_source_key = ?`, parentKind, parentSourceKey)
	if err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("list existing retrieval projection chunks: %w", err)
	}
	for rows.Next() {
		row, err := scanRetrievalChunk(rows)
		if err != nil {
			_ = rows.Close()
			return ChunkReplaceResult{}, fmt.Errorf("scan existing retrieval projection chunk: %w", err)
		}
		existing[row.ChunkID] = row
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ChunkReplaceResult{}, fmt.Errorf("iterate existing retrieval projection chunks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("close existing retrieval projection chunks: %w", err)
	}

	affectedProfiles := make(map[string]struct{})
	rows, err = tx.QueryContext(ctx, `
		SELECT DISTINCT e.profile_id, c.chunk_id, c.chunk_text_hash, c.projection_version, c.chunker_version
		FROM retrieval_chunks c
		JOIN retrieval_embeddings e ON e.chunk_id = c.chunk_id
		WHERE c.parent_kind = ? AND c.parent_source_key = ?`, parentKind, parentSourceKey)
	if err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("list affected retrieval projection profiles: %w", err)
	}
	for rows.Next() {
		var profileID, chunkID, textHash, projectionVersion, chunkerVersion string
		if err := rows.Scan(&profileID, &chunkID, &textHash, &projectionVersion, &chunkerVersion); err != nil {
			_ = rows.Close()
			return ChunkReplaceResult{}, fmt.Errorf("scan affected retrieval projection profile: %w", err)
		}
		chunk, keep := incoming[chunkID]
		if !keep || chunk.TextHash != textHash || chunk.ProjectionVersion != projectionVersion || chunk.ChunkerVersion != chunkerVersion {
			affectedProfiles[profileID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ChunkReplaceResult{}, fmt.Errorf("iterate affected retrieval projection profiles: %w", err)
	}
	if err := rows.Close(); err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("close affected retrieval projection profiles: %w", err)
	}
	for profileID := range affectedProfiles {
		if err := markRetrievalProfileGenerationsStaleTx(ctx, tx, profileID); err != nil {
			return ChunkReplaceResult{}, err
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM retrieval_chunk_occurrences WHERE parent_kind=? AND parent_source_key=?`, parentKind, parentSourceKey); err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("delete prior retrieval chunk occurrences: %w", err)
	}
	result := ChunkReplaceResult{}
	for id := range existing {
		if _, keep := incoming[id]; keep {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM retrieval_chunks WHERE chunk_id=?`, id); err != nil {
			return ChunkReplaceResult{}, fmt.Errorf("delete obsolete retrieval projection chunk %s: %w", id, err)
		}
		result.Deleted++
	}
	// Move retained ordinals out of the unique parent-ordinal namespace before
	// assigning the new deterministic ordering. This avoids transient conflicts
	// when two retained identities exchange positions.
	if _, err := tx.ExecContext(ctx, `UPDATE retrieval_chunks SET ordinal=-(ordinal+1) WHERE parent_kind=? AND parent_source_key=?`, parentKind, parentSourceKey); err != nil {
		return ChunkReplaceResult{}, fmt.Errorf("stage retrieval projection ordinals: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, chunk := range chunks {
		old, existed := existing[chunk.ID]
		if existed && retrievalChunkMatches(old, chunk) {
			result.Reused++
		} else if existed && old.ChunkTextHash == chunk.TextHash {
			result.Updated++
		} else {
			result.Created++
		}
		write, err := tx.ExecContext(ctx, `
			INSERT INTO retrieval_chunks (
				chunk_id, parent_kind, parent_source_key, evidence_role, section_key, section_ordinal,
				ordinal, start_char, end_char, heading, heading_hash, derived, projection_version,
				chunker_version, input_content_hash, chunk_text_hash, text, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(chunk_id) DO UPDATE SET
				evidence_role=excluded.evidence_role, section_key=excluded.section_key,
				section_ordinal=excluded.section_ordinal, ordinal=excluded.ordinal,
				start_char=excluded.start_char, end_char=excluded.end_char,
				heading=excluded.heading, heading_hash=excluded.heading_hash, derived=excluded.derived,
				projection_version=excluded.projection_version, chunker_version=excluded.chunker_version,
				input_content_hash=excluded.input_content_hash, chunk_text_hash=excluded.chunk_text_hash,
				text=excluded.text, updated_at=excluded.updated_at
			WHERE retrieval_chunks.parent_kind=excluded.parent_kind AND retrieval_chunks.parent_source_key=excluded.parent_source_key`,
			chunk.ID, parentKind, parentSourceKey, chunk.EvidenceRole, chunk.SectionKey, chunk.SectionOrdinal,
			chunk.Ordinal, chunk.StartChar, chunk.EndChar, chunk.Heading, chunk.HeadingHash, boolInt(chunk.Derived),
			chunk.ProjectionVersion, chunk.ChunkerVersion, chunk.InputContentHash, chunk.TextHash, chunk.Text, now, now)
		if err != nil {
			return ChunkReplaceResult{}, fmt.Errorf("write retrieval projection chunk %s: %w", chunk.ID, err)
		}
		affected, err := write.RowsAffected()
		if err != nil {
			return ChunkReplaceResult{}, fmt.Errorf("inspect retrieval projection chunk write %s: %w", chunk.ID, err)
		}
		if affected != 1 {
			return ChunkReplaceResult{}, fmt.Errorf("retrieval chunk ID %q already belongs to another parent", chunk.ID)
		}
		if existed && (old.ChunkTextHash != chunk.TextHash || old.ProjectionVersion != chunk.ProjectionVersion || old.ChunkerVersion != chunk.ChunkerVersion) {
			if _, err := tx.ExecContext(ctx, `DELETE FROM retrieval_embeddings WHERE chunk_id=?`, chunk.ID); err != nil {
				return ChunkReplaceResult{}, fmt.Errorf("invalidate changed retrieval projection chunk %s embeddings: %w", chunk.ID, err)
			}
		}
	}
	for _, occurrence := range occurrences {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO retrieval_chunk_occurrences (
				parent_kind,parent_source_key,chunk_id,section_key,start_char,end_char,created_at,updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			parentKind, parentSourceKey, occurrence.ChunkID, occurrence.SectionKey,
			occurrence.StartChar, occurrence.EndChar, now, now); err != nil {
			return ChunkReplaceResult{}, fmt.Errorf("write retrieval chunk occurrence for %s: %w", occurrence.ChunkID, err)
		}
	}
	return result, nil
}

func retrievalChunkMatches(old RetrievalChunkRow, chunk retrievalchunk.Chunk) bool {
	return old.ChunkID == chunk.ID &&
		old.ParentKind == chunk.ParentKind &&
		old.ParentSourceKey == chunk.ParentSourceKey &&
		old.EvidenceRole == chunk.EvidenceRole &&
		old.SectionOrdinal == chunk.SectionOrdinal &&
		old.Ordinal == chunk.Ordinal &&
		old.StartChar == chunk.StartChar &&
		old.EndChar == chunk.EndChar &&
		old.Heading == chunk.Heading &&
		old.ProjectionVersion == chunk.ProjectionVersion &&
		old.ChunkerVersion == chunk.ChunkerVersion &&
		old.InputContentHash == chunk.InputContentHash &&
		old.ChunkTextHash == chunk.TextHash &&
		old.Text == chunk.Text
}

func (s *Store) GetRetrievalChunk(ctx context.Context, chunkID string) (RetrievalChunkRow, error) {
	row := s.queryer().QueryRowContext(ctx, `
		SELECT chunk_id, parent_kind, parent_source_key, evidence_role, section_ordinal,
			ordinal, start_char, end_char, heading, projection_version, chunker_version,
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
		&row.Ordinal, &row.StartChar, &row.EndChar, &row.Heading, &row.ProjectionVersion, &row.ChunkerVersion,
		&row.InputContentHash, &row.ChunkTextHash, &row.Text)
	return row, err
}

func (s *Store) HydrateRetrievalChunks(ctx context.Context, chunkIDs []string) ([]RetrievalChunkEvidenceRow, error) {
	const maxHydrationChunks = 1000
	clean := make([]string, 0, len(chunkIDs))
	seen := make(map[string]struct{}, len(chunkIDs))
	for _, chunkID := range chunkIDs {
		chunkID = strings.TrimSpace(chunkID)
		if chunkID == "" {
			continue
		}
		if _, ok := seen[chunkID]; ok {
			continue
		}
		seen[chunkID] = struct{}{}
		clean = append(clean, chunkID)
	}
	if len(clean) == 0 {
		return make([]RetrievalChunkEvidenceRow, 0), nil
	}
	if len(clean) > maxHydrationChunks {
		return nil, fmt.Errorf("hydrate retrieval chunks: %d IDs exceeds maximum %d", len(clean), maxHydrationChunks)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(clean)), ",")
	args := make([]any, len(clean))
	for i := range clean {
		args[i] = clean[i]
	}
	query := `
		SELECT c.chunk_id, c.parent_kind, c.parent_source_key, c.evidence_role,
			c.ordinal, c.start_char, c.end_char, c.heading, c.chunk_text_hash, c.text,
			p.title, p.canonical_url, p.note_path, p.summary_text,
			p.author_name, p.author_handle, p.source_type, p.published_at,
			p.extracted_at, p.summarized_at, p.user_tags
		FROM retrieval_chunks c
		JOIN (
			SELECT 'item' AS parent_kind, source_key, title, canonical_url, note_path,
				` + itemSummaryTextExpr() + ` AS summary_text, author_name, author_handle, source_type, published_at,
				imported_at AS extracted_at, summarized_at, user_tags
			FROM items WHERE trim(note_path) != ''
			UNION ALL
			SELECT 'source' AS parent_kind, source_key, title, canonical_url, note_path,
				summary_text, '' AS author_name, '' AS author_handle, source_type,
				'' AS published_at, extracted_at, summarized_at, user_tags
			FROM sources WHERE trim(note_path) != ''
		) p ON p.parent_kind = c.parent_kind AND p.source_key = c.parent_source_key
		WHERE c.chunk_id IN (` + placeholders + `)
		ORDER BY c.chunk_id`
	rows, err := s.queryer().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("hydrate retrieval chunks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]RetrievalChunkEvidenceRow, 0, len(clean))
	for rows.Next() {
		var row RetrievalChunkEvidenceRow
		var authorName, authorHandle string
		if err := rows.Scan(
			&row.ChunkID, &row.ParentKind, &row.ParentSourceKey, &row.EvidenceRole,
			&row.Ordinal, &row.StartChar, &row.EndChar, &row.Heading, &row.ChunkTextHash, &row.Text,
			&row.Title, &row.URL, &row.NotePath, &row.Summary,
			&authorName, &authorHandle, &row.SourceType, &row.PublishedAt,
			&row.ExtractedAt, &row.SummarizedAt, &row.UserTags,
		); err != nil {
			return nil, fmt.Errorf("scan hydrated retrieval chunk: %w", err)
		}
		row.Author = strings.TrimSpace(strings.TrimSpace(authorName) + " " + prefixedHandle(authorHandle))
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hydrated retrieval chunks: %w", err)
	}
	return result, nil
}

func prefixedHandle(handle string) string {
	handle = strings.TrimSpace(handle)
	if handle == "" || strings.HasPrefix(handle, "@") {
		return handle
	}
	return "@" + handle
}
