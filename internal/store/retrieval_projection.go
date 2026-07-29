package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrievalchunk"
)

func loadCurrentRetrievalParent(ctx context.Context, q sqlQueryer, kind, sourceKey string) (retrievalchunk.Parent, bool, bool, error) {
	identity := retrievalchunk.Parent{Kind: kind, SourceKey: sourceKey}
	switch kind {
	case "item":
		var contentHash, title, sourceType, authorName, authorHandle, text, xPostText, ocrText string
		var articleTitle, articleText, transcriptText, summaryText, notePath string
		var hasAuthoritativeTranscript bool
		err := q.QueryRowContext(ctx, `
			SELECT content_hash, title, source_type, author_name, author_handle, text, x_post_text,
				`+itemOCRTextExpr()+`, article_title, article_text, `+itemXMediaTranscriptTextExpr()+`,
				EXISTS(SELECT 1 FROM item_enrichments e WHERE e.item_id=items.id AND e.role='`+model.ItemEnrichmentRoleXMediaTranscript+`'),
				`+itemSummaryTextExpr()+`, note_path
			FROM items WHERE source_key=?`, sourceKey).Scan(
			&contentHash, &title, &sourceType, &authorName, &authorHandle, &text, &xPostText,
			&ocrText, &articleTitle, &articleText, &transcriptText, &hasAuthoritativeTranscript,
			&summaryText, &notePath,
		)
		if err == sql.ErrNoRows {
			return identity, false, false, nil
		}
		if err != nil {
			return retrievalchunk.Parent{}, false, false, fmt.Errorf("load current retrieval item %s: %w", sourceKey, err)
		}
		if strings.TrimSpace(notePath) == "" {
			return identity, true, false, nil
		}
		if hasAuthoritativeTranscript && strings.TrimSpace(articleTitle) == model.XMediaTranscriptArticleTitle {
			articleTitle = ""
			articleText = ""
		}
		if strings.TrimSpace(transcriptText) != "" {
			articleTitle = model.XMediaTranscriptArticleTitle
			articleText = transcriptText
		}
		return retrievalchunk.ProjectItem(model.Item{
			SourceKey: sourceKey, ContentHash: contentHash, Title: title, SourceType: sourceType,
			AuthorName: authorName, AuthorHandle: authorHandle, Text: text, XPostText: xPostText,
			OCRText: ocrText, ArticleTitle: articleTitle, ArticleText: articleText, SummaryText: summaryText,
		}), true, true, nil
	case "source":
		var contentHash, title, sourceType, domain, extractedText, summaryText, notePath string
		err := q.QueryRowContext(ctx, `
			SELECT content_hash, title, source_type, domain, extracted_text, summary_text, note_path
			FROM sources WHERE source_key=?`, sourceKey).Scan(
			&contentHash, &title, &sourceType, &domain, &extractedText, &summaryText, &notePath,
		)
		if err == sql.ErrNoRows {
			return identity, false, false, nil
		}
		if err != nil {
			return retrievalchunk.Parent{}, false, false, fmt.Errorf("load current retrieval source %s: %w", sourceKey, err)
		}
		if strings.TrimSpace(notePath) == "" {
			return identity, true, false, nil
		}
		return retrievalchunk.ProjectSource(model.SourceDocument{
			SourceKey: sourceKey, ContentHash: contentHash, Title: title, SourceType: sourceType,
			Domain: domain, ExtractedText: extractedText, SummaryText: summaryText,
		}), true, true, nil
	default:
		return retrievalchunk.Parent{}, false, false, fmt.Errorf("invalid retrieval parent kind %q", kind)
	}
}

// ListRetrievalParents pages distinct source keys and returns every item/source
// parent for each selected key. limit is therefore a key-page size; a page can
// contain more parent rows when both tables contain the same source key.
func (s *Store) ListRetrievalParents(ctx context.Context, afterSourceKey string, limit int) ([]retrievalchunk.Parent, error) {
	summaryText := itemSummaryTextExpr()
	ocrText := itemOCRTextExpr()
	transcriptText := itemXMediaTranscriptTextExpr()
	transcriptAuthoritative := `EXISTS(
		SELECT 1 FROM item_enrichments e
		WHERE e.item_id = items.id AND e.role = '` + model.ItemEnrichmentRoleXMediaTranscript + `'
	)`
	query := `
		WITH page_keys AS (
			SELECT source_key FROM (
				SELECT source_key FROM items WHERE note_path != '' AND source_key > ?
				UNION
				SELECT source_key FROM sources WHERE note_path != '' AND source_key > ?
			)
			ORDER BY source_key`
	args := []any{afterSourceKey, afterSourceKey}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	query += `
		)
		SELECT parent_kind, source_key, content_hash, title, source_type,
			author_name, author_handle, domain, text, x_post_text, ocr_text,
			article_title, article_text, transcript_text, transcript_authoritative,
			extracted_text, summary_text
		FROM (
			SELECT 'item' AS parent_kind, source_key, content_hash, title, source_type,
				author_name, author_handle, '' AS domain, text, x_post_text, ` + ocrText + ` AS ocr_text,
				article_title, article_text, ` + transcriptText + ` AS transcript_text,
				` + transcriptAuthoritative + ` AS transcript_authoritative,
				'' AS extracted_text, ` + summaryText + ` AS summary_text
			FROM items JOIN page_keys USING(source_key)
			UNION ALL
			SELECT 'source' AS parent_kind, source_key, content_hash, title, source_type,
				'' AS author_name, '' AS author_handle, domain, '' AS text, '' AS x_post_text,
				'' AS ocr_text, '' AS article_title, '' AS article_text, '' AS transcript_text, 0 AS transcript_authoritative,
				extracted_text, summary_text
			FROM sources JOIN page_keys USING(source_key)
		)
		ORDER BY source_key, parent_kind`
	rows, err := s.queryer().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list retrieval parents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	parents := make([]retrievalchunk.Parent, 0)
	for rows.Next() {
		var kind, sourceKey, contentHash, title, sourceType string
		var authorName, authorHandle, domain, text, xPostText, ocrText string
		var articleTitle, articleText, transcriptText, extractedText, summaryText string
		var hasAuthoritativeTranscript bool
		if err := rows.Scan(&kind, &sourceKey, &contentHash, &title, &sourceType,
			&authorName, &authorHandle, &domain, &text, &xPostText, &ocrText,
			&articleTitle, &articleText, &transcriptText, &hasAuthoritativeTranscript,
			&extractedText, &summaryText); err != nil {
			return nil, fmt.Errorf("scan retrieval parent: %w", err)
		}
		if kind == "item" {
			if hasAuthoritativeTranscript && strings.TrimSpace(articleTitle) == model.XMediaTranscriptArticleTitle {
				articleTitle = ""
				articleText = ""
			}
			if strings.TrimSpace(transcriptText) != "" {
				articleTitle = model.XMediaTranscriptArticleTitle
				articleText = transcriptText
			}
			parents = append(parents, retrievalchunk.ProjectItem(model.Item{
				SourceKey: sourceKey, ContentHash: contentHash, Title: title, SourceType: sourceType,
				AuthorName: authorName, AuthorHandle: authorHandle, Text: text, XPostText: xPostText,
				OCRText: ocrText, ArticleTitle: articleTitle, ArticleText: articleText, SummaryText: summaryText,
			}))
			continue
		}
		parents = append(parents, retrievalchunk.ProjectSource(model.SourceDocument{
			SourceKey: sourceKey, ContentHash: contentHash, Title: title, SourceType: sourceType,
			Domain: domain, ExtractedText: extractedText, SummaryText: summaryText,
		}))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retrieval parents: %w", err)
	}
	return parents, nil
}

type RetrievalStatus struct {
	Available           bool   `json:"available"`
	ProfileID           string `json:"profile_id"`
	ChunkCount          int    `json:"chunk_count"`
	ReadyEmbeddings     int    `json:"ready_embeddings"`
	PendingEmbeddings   int    `json:"pending_embeddings"`
	BlockedEmbeddings   int    `json:"blocked_embeddings"`
	FailedEmbeddings    int    `json:"failed_embeddings"`
	EmbeddingCandidates int    `json:"embedding_candidates"`
	ActiveGenerationID  string `json:"active_generation_id"`
	StaleGenerations    int    `json:"stale_generations"`
}

func (s *Store) RetrievalStatus(ctx context.Context, profileID string) (RetrievalStatus, error) {
	return s.RetrievalStatusAt(ctx, profileID, time.Now().UTC())
}

func (s *Store) RetrievalStatusAt(ctx context.Context, profileID string, now time.Time) (RetrievalStatus, error) {
	available, err := s.RetrievalAvailable(ctx)
	if err != nil {
		return RetrievalStatus{}, err
	}
	if !available {
		return RetrievalStatus{ProfileID: profileID}, ErrRetrievalUnavailable
	}
	status := RetrievalStatus{Available: true, ProfileID: profileID}
	if err := s.queryer().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM retrieval_chunks chunk
		JOIN retrieval_parent_projections parent
			ON parent.parent_kind = chunk.parent_kind
			AND parent.parent_source_key = chunk.parent_source_key
			AND parent.status = 'current'`).Scan(&status.ChunkCount); err != nil {
		return RetrievalStatus{}, fmt.Errorf("count retrieval chunks: %w", err)
	}
	if err := s.queryer().QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN e.status = 'ready' AND e.chunk_text_hash = c.chunk_text_hash THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN e.chunk_id IS NULL OR e.chunk_text_hash != c.chunk_text_hash OR e.status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN e.status = 'blocked' AND e.chunk_text_hash = c.chunk_text_hash THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN e.status = 'error' AND e.chunk_text_hash = c.chunk_text_hash THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN `+retrievalEmbeddingDueSQL+` THEN 1 ELSE 0 END), 0)
		FROM retrieval_chunks c
		JOIN retrieval_parent_projections parent
			ON parent.parent_kind = c.parent_kind
			AND parent.parent_source_key = c.parent_source_key
			AND parent.status = 'current'
		LEFT JOIN retrieval_embeddings e ON e.chunk_id = c.chunk_id AND e.profile_id = ?`, now.UTC().Format(time.RFC3339), profileID).Scan(
		&status.ReadyEmbeddings, &status.PendingEmbeddings, &status.BlockedEmbeddings, &status.FailedEmbeddings,
		&status.EmbeddingCandidates,
	); err != nil {
		return RetrievalStatus{}, fmt.Errorf("count retrieval embeddings: %w", err)
	}
	if err := s.queryer().QueryRowContext(ctx, `
		SELECT COALESCE(MAX(CASE WHEN active = 1 THEN generation_id ELSE '' END), ''),
			COALESCE(SUM(CASE WHEN build_status = 'stale' THEN 1 ELSE 0 END), 0)
		FROM retrieval_index_generations WHERE profile_id = ?`, profileID).Scan(
		&status.ActiveGenerationID, &status.StaleGenerations,
	); err != nil {
		return RetrievalStatus{}, fmt.Errorf("load retrieval generation status: %w", err)
	}
	return status, nil
}
