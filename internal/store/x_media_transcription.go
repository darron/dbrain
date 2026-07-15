package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (s *Store) ListItemsForXMediaTranscription(ctx context.Context, limit int, force bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = 100
	}

	pendingWhere, pendingArgs := xMediaTranscriptionPendingWhere(time.Now().UTC())

	query := `
		SELECT ` + itemSelectColumns + `
		FROM items
		WHERE ` + xItemSourceTypeWhere + `
			AND external_id != ''
			AND ` + xMediaTranscriptionRunnableMediaExistsWhere
	if !force {
		query += ` AND ` + pendingWhere
	}
	query += `
		ORDER BY last_seen_at DESC, id DESC
		LIMIT ?`

	args := append([]any{}, pendingArgs...)
	if force {
		args = nil
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list x media transcription items: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		if err := scanItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan x media transcription item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x media transcription items: %w", err)
	}
	if err := s.applyItemEnrichmentMirrorToItems(ctx, items); err != nil {
		return nil, err
	}

	return items, nil
}

type XMediaTranscriptionState struct {
	Status, Error, RawJSON, Model, Tool, ToolVersion, InputHash string
	InputSettings                                               []XMediaTranscriptionInputSettings
	CompletedAt                                                 time.Time
}

type XMediaTranscriptionInputSettings struct {
	Backend    string `json:"backend"`
	Model      string `json:"model"`
	Language   string `json:"language"`
	VADEnabled bool   `json:"vad_enabled"`
}

func (s *Store) SaveXMediaTranscriptionState(ctx context.Context, itemID int64, status string, errorText string, at time.Time) error {
	return s.SaveXMediaTranscription(ctx, itemID, XMediaTranscriptionState{Status: status, Error: errorText, CompletedAt: at})
}

func (s *Store) SaveXMediaTranscription(ctx context.Context, itemID int64, state XMediaTranscriptionState) error {
	_, err := withBusyRetry(ctx, func() (struct{}, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return struct{}{}, fmt.Errorf("begin x media transcription state tx: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		atText := ""
		if !state.CompletedAt.IsZero() {
			atText = state.CompletedAt.UTC().Format(time.RFC3339)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE items
			SET x_media_transcript_status = ?,
				x_media_transcript_error = ?,
				x_media_transcript_at = ?,
				updated_at = ?
			WHERE id = ?`,
			strings.TrimSpace(state.Status),
			strings.TrimSpace(state.Error),
			atText,
			time.Now().UTC().Format(time.RFC3339),
			itemID,
		); err != nil {
			return struct{}{}, fmt.Errorf("save x media transcription state %d: %w", itemID, err)
		}

		var articleTitle string
		var articleText string
		if err := tx.QueryRowContext(ctx, `SELECT article_title, article_text FROM items WHERE id = ?`, itemID).Scan(&articleTitle, &articleText); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return struct{}{}, fmt.Errorf("item not found for x media transcription state: %d", itemID)
			}
			return struct{}{}, fmt.Errorf("load x media transcript text %d: %w", itemID, err)
		}
		text := ""
		if strings.TrimSpace(articleTitle) == model.XMediaTranscriptArticleTitle {
			text = articleText
		}
		if strings.TrimSpace(state.InputHash) == "" &&
			strings.TrimSpace(state.Status) == model.XMediaTranscriptStatusOK &&
			len(state.InputSettings) > 0 {
			contentHashes, err := xMediaTranscriptionContentHashes(ctx, tx, itemID)
			if err != nil {
				return struct{}{}, err
			}
			state.InputHash, err = xMediaTranscriptionInputHash(contentHashes, state.InputSettings)
			if err != nil {
				return struct{}{}, fmt.Errorf("compute x media transcript input hash %d: %w", itemID, err)
			}
		}
		if err := s.upsertItemEnrichmentTx(ctx, tx, model.ItemEnrichment{
			ItemID:      itemID,
			Role:        model.ItemEnrichmentRoleXMediaTranscript,
			Status:      strings.TrimSpace(state.Status),
			Text:        text,
			RawJSON:     state.RawJSON,
			Error:       strings.TrimSpace(state.Error),
			Model:       state.Model,
			Tool:        state.Tool,
			ToolVersion: state.ToolVersion,
			InputHash:   state.InputHash,
			CompletedAt: state.CompletedAt,
		}); err != nil {
			return struct{}{}, err
		}
		if err := tx.Commit(); err != nil {
			return struct{}{}, fmt.Errorf("commit x media transcription state %d: %w", itemID, err)
		}
		return struct{}{}, nil
	})
	return err
}

type xMediaTranscriptionQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *Store) XMediaTranscriptionInputHash(ctx context.Context, itemID int64, settings []XMediaTranscriptionInputSettings) (string, error) {
	contentHashes, err := xMediaTranscriptionContentHashes(ctx, s.db, itemID)
	if err != nil {
		return "", err
	}
	inputHash, err := xMediaTranscriptionInputHash(contentHashes, settings)
	if err != nil {
		return "", fmt.Errorf("compute x media transcript input hash %d: %w", itemID, err)
	}
	return inputHash, nil
}

func xMediaTranscriptionContentHashes(ctx context.Context, queryer xMediaTranscriptionQueryer, itemID int64) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT a.content_hash
		FROM item_media_links l
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE l.item_id = ?
			AND a.download_status = ?
			AND a.local_path != ''
			AND a.local_pruned_at = ''
			AND a.media_type IN ('video', 'animated_gif')`,
		itemID, model.MediaDownloadStatusDownloaded,
	)
	if err != nil {
		return nil, fmt.Errorf("list x media transcript input hashes %d: %w", itemID, err)
	}
	defer func() { _ = rows.Close() }()
	var hashes []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, fmt.Errorf("scan x media transcript input hash %d: %w", itemID, err)
		}
		hash = strings.TrimSpace(hash)
		if hash == "" {
			return nil, fmt.Errorf("x media transcript input %d has no durable content hash", itemID)
		}
		hashes = append(hashes, hash)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate x media transcript input hashes %d: %w", itemID, err)
	}
	return hashes, nil
}

func xMediaTranscriptionInputHash(contentHashes []string, settings []XMediaTranscriptionInputSettings) (string, error) {
	hashes := append([]string(nil), contentHashes...)
	for i := range hashes {
		hashes[i] = strings.TrimSpace(hashes[i])
		if hashes[i] == "" {
			return "", fmt.Errorf("media content hash is required")
		}
	}
	if len(hashes) == 0 {
		return "", fmt.Errorf("at least one media content hash is required")
	}
	resolvedSettings := append([]XMediaTranscriptionInputSettings(nil), settings...)
	if len(resolvedSettings) == 0 {
		return "", fmt.Errorf("at least one resolved transcription setting is required")
	}
	for i := range resolvedSettings {
		resolvedSettings[i].Backend = strings.TrimSpace(resolvedSettings[i].Backend)
		resolvedSettings[i].Model = strings.TrimSpace(resolvedSettings[i].Model)
		resolvedSettings[i].Language = strings.TrimSpace(resolvedSettings[i].Language)
		if resolvedSettings[i].Backend == "" || resolvedSettings[i].Model == "" || resolvedSettings[i].Language == "" {
			return "", fmt.Errorf("resolved backend, model, and language are required")
		}
	}
	sort.Strings(hashes)
	sort.Slice(resolvedSettings, func(i, j int) bool {
		left, _ := json.Marshal(resolvedSettings[i])
		right, _ := json.Marshal(resolvedSettings[j])
		return string(left) < string(right)
	})
	payload, err := json.Marshal(struct {
		Version            string                             `json:"version"`
		MediaContentHashes []string                           `json:"media_content_hashes"`
		Settings           []XMediaTranscriptionInputSettings `json:"settings"`
	}{
		Version: "dbrain.x_media_transcript.input.v1", MediaContentHashes: hashes, Settings: resolvedSettings,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
