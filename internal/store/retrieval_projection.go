package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrievalchunk"
)

func (s *Store) ListRetrievalParents(ctx context.Context, afterSourceKey string, limit int) ([]retrievalchunk.Parent, error) {
	summaryText := itemSummaryTextExpr()
	ocrText := itemOCRTextExpr()
	transcriptText := itemXMediaTranscriptTextExpr()
	query := `
		SELECT parent_kind, source_key, content_hash, title, source_type,
			author_name, author_handle, domain, text, x_post_text, ocr_text,
			article_title, article_text, transcript_text, extracted_text, summary_text
		FROM (
			SELECT 'item' AS parent_kind, source_key, content_hash, title, source_type,
				author_name, author_handle, '' AS domain, text, x_post_text, ` + ocrText + ` AS ocr_text,
				article_title, article_text, ` + transcriptText + ` AS transcript_text,
				'' AS extracted_text, ` + summaryText + ` AS summary_text
			FROM items WHERE note_path != '' AND source_key > ?
			UNION ALL
			SELECT 'source' AS parent_kind, source_key, content_hash, title, source_type,
				'' AS author_name, '' AS author_handle, domain, '' AS text, '' AS x_post_text,
				'' AS ocr_text, '' AS article_title, '' AS article_text, '' AS transcript_text,
				extracted_text, summary_text
			FROM sources WHERE note_path != '' AND source_key > ?
		)
		ORDER BY source_key, parent_kind`
	args := []any{afterSourceKey, afterSourceKey}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
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
		if err := rows.Scan(&kind, &sourceKey, &contentHash, &title, &sourceType,
			&authorName, &authorHandle, &domain, &text, &xPostText, &ocrText,
			&articleTitle, &articleText, &transcriptText, &extractedText, &summaryText); err != nil {
			return nil, fmt.Errorf("scan retrieval parent: %w", err)
		}
		if kind == "item" {
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
	Available          bool   `json:"available"`
	ProfileID          string `json:"profile_id"`
	ChunkCount         int    `json:"chunk_count"`
	ReadyEmbeddings    int    `json:"ready_embeddings"`
	PendingEmbeddings  int    `json:"pending_embeddings"`
	BlockedEmbeddings  int    `json:"blocked_embeddings"`
	FailedEmbeddings   int    `json:"failed_embeddings"`
	ActiveGenerationID string `json:"active_generation_id"`
	StaleGenerations   int    `json:"stale_generations"`
}

func (s *Store) RetrievalStatus(ctx context.Context, profileID string) (RetrievalStatus, error) {
	available, err := s.RetrievalAvailable(ctx)
	if err != nil {
		return RetrievalStatus{}, err
	}
	if !available {
		return RetrievalStatus{ProfileID: profileID}, ErrRetrievalUnavailable
	}
	status := RetrievalStatus{Available: true, ProfileID: profileID}
	if err := s.queryer().QueryRowContext(ctx, `SELECT COUNT(*) FROM retrieval_chunks`).Scan(&status.ChunkCount); err != nil {
		return RetrievalStatus{}, fmt.Errorf("count retrieval chunks: %w", err)
	}
	if err := s.queryer().QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN e.status = 'ready' AND e.chunk_text_hash = c.chunk_text_hash THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN e.chunk_id IS NULL OR e.chunk_text_hash != c.chunk_text_hash OR e.status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN e.status = 'blocked' AND e.chunk_text_hash = c.chunk_text_hash THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN e.status = 'error' AND e.chunk_text_hash = c.chunk_text_hash THEN 1 ELSE 0 END), 0)
		FROM retrieval_chunks c
		LEFT JOIN retrieval_embeddings e ON e.chunk_id = c.chunk_id AND e.profile_id = ?`, profileID).Scan(
		&status.ReadyEmbeddings, &status.PendingEmbeddings, &status.BlockedEmbeddings, &status.FailedEmbeddings,
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
