package store

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const publicChatShareSaltKey = "public_chat_share_salt"

type PublicChatShareInput struct {
	OwnerProvider    string
	OwnerSubject     string
	OwnerUsername    string
	Title            string
	Summary          string
	Categories       []string
	SanitizedContent string
	OriginalURLs     []string
	MetadataJSON     string
}

type PublicChatShare struct {
	Slug             string
	OwnerProvider    string
	OwnerSubject     string
	OwnerUsername    string
	ContentHash      string
	Title            string
	Summary          string
	Categories       []string
	SanitizedContent string
	OriginalURLs     []string
	MetadataJSON     string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (s *Store) ensurePublicChatShareTables() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS public_share_config (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);`); err != nil {
		return fmt.Errorf("ensure public_share_config table: %w", err)
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS public_chat_shares (
		slug TEXT PRIMARY KEY,
		owner_provider TEXT NOT NULL,
		owner_subject TEXT NOT NULL,
		owner_username TEXT NOT NULL DEFAULT '',
		content_hash TEXT NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL,
		categories_json TEXT NOT NULL DEFAULT '[]',
		sanitized_content TEXT NOT NULL,
		original_urls_json TEXT NOT NULL DEFAULT '[]',
		metadata_json TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);`); err != nil {
		return fmt.Errorf("ensure public_chat_shares table: %w", err)
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_public_chat_shares_owner_content
		ON public_chat_shares(owner_provider, owner_subject, content_hash);`); err != nil {
		return fmt.Errorf("ensure public chat share owner content index: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_public_chat_shares_owner_updated
		ON public_chat_shares(owner_provider, owner_subject, updated_at DESC);`); err != nil {
		return fmt.Errorf("ensure public chat share owner updated index: %w", err)
	}
	return nil
}

func (s *Store) SavePublicChatShare(ctx context.Context, input PublicChatShareInput) (PublicChatShare, error) {
	input = normalizePublicChatShareInput(input)
	if input.OwnerProvider == "" || input.OwnerSubject == "" {
		return PublicChatShare{}, fmt.Errorf("share owner is required")
	}
	if input.SanitizedContent == "" {
		return PublicChatShare{}, fmt.Errorf("share content is required")
	}
	if input.Summary == "" {
		return PublicChatShare{}, fmt.Errorf("share summary is required")
	}

	salt, err := s.publicChatShareSalt(ctx)
	if err != nil {
		return PublicChatShare{}, err
	}
	contentHash := publicChatShareContentHash(input.SanitizedContent)
	slug := publicChatShareSlug(salt, input.OwnerProvider, input.OwnerSubject, contentHash)
	categoriesJSON, err := jsonStringArray(input.Categories)
	if err != nil {
		return PublicChatShare{}, fmt.Errorf("encode share categories: %w", err)
	}
	urlsJSON, err := jsonStringArray(input.OriginalURLs)
	if err != nil {
		return PublicChatShare{}, fmt.Errorf("encode share urls: %w", err)
	}
	metadataJSON := strings.TrimSpace(input.MetadataJSON)
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	now := time.Now().UTC().Format(time.RFC3339)

	row := s.db.QueryRowContext(ctx, `
		INSERT INTO public_chat_shares (
			slug, owner_provider, owner_subject, owner_username, content_hash,
			title, summary, categories_json, sanitized_content, original_urls_json,
			metadata_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_provider, owner_subject, content_hash) DO UPDATE SET
			owner_username = excluded.owner_username,
			title = excluded.title,
			summary = excluded.summary,
			categories_json = excluded.categories_json,
			sanitized_content = excluded.sanitized_content,
			original_urls_json = excluded.original_urls_json,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at
		RETURNING `+publicChatShareColumns,
		slug,
		input.OwnerProvider,
		input.OwnerSubject,
		input.OwnerUsername,
		contentHash,
		input.Title,
		input.Summary,
		categoriesJSON,
		input.SanitizedContent,
		urlsJSON,
		metadataJSON,
		now,
		now,
	)
	share, err := scanPublicChatShare(row)
	if err != nil {
		return PublicChatShare{}, fmt.Errorf("save public chat share: %w", err)
	}
	return share, nil
}

func (s *Store) GetPublicChatShareBySlug(ctx context.Context, slug string) (PublicChatShare, bool, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return PublicChatShare{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+publicChatShareColumns+` FROM public_chat_shares WHERE slug = ?`, slug)
	share, err := scanPublicChatShare(row)
	if err != nil {
		if isNoRows(err) {
			return PublicChatShare{}, false, nil
		}
		return PublicChatShare{}, false, fmt.Errorf("get public chat share: %w", err)
	}
	return share, true, nil
}

func (s *Store) ListPublicChatSharesByOwner(ctx context.Context, ownerProvider string, ownerSubject string, limit int) ([]PublicChatShare, error) {
	ownerProvider = normalizeOwnerProvider(ownerProvider)
	ownerSubject = strings.TrimSpace(ownerSubject)
	if ownerProvider == "" || ownerSubject == "" {
		return nil, fmt.Errorf("share owner is required")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+publicChatShareColumns+`
		FROM public_chat_shares
		WHERE owner_provider = ? AND owner_subject = ?
		ORDER BY updated_at DESC, created_at DESC, slug DESC
		LIMIT ?`,
		ownerProvider,
		ownerSubject,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list public chat shares: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	shares := []PublicChatShare{}
	for rows.Next() {
		share, err := scanPublicChatShare(rows)
		if err != nil {
			return nil, fmt.Errorf("scan public chat share: %w", err)
		}
		shares = append(shares, share)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate public chat shares: %w", err)
	}
	return shares, nil
}

func (s *Store) DeletePublicChatShareByOwner(ctx context.Context, slug string, ownerProvider string, ownerSubject string) error {
	slug = strings.TrimSpace(slug)
	ownerProvider = normalizeOwnerProvider(ownerProvider)
	ownerSubject = strings.TrimSpace(ownerSubject)
	if ownerProvider == "" || ownerSubject == "" {
		return fmt.Errorf("share owner is required")
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM public_chat_shares
		WHERE slug = ? AND owner_provider = ? AND owner_subject = ?`,
		slug,
		ownerProvider,
		ownerSubject,
	); err != nil {
		return fmt.Errorf("delete public chat share: %w", err)
	}
	return nil
}

func (s *Store) publicChatShareSalt(ctx context.Context) ([]byte, error) {
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM public_share_config WHERE key = ?`, publicChatShareSaltKey).Scan(&encoded)
	if err == nil {
		return decodePublicChatShareSalt(encoded)
	}
	if !isNoRows(err) {
		return nil, fmt.Errorf("load public share salt: %w", err)
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate public share salt: %w", err)
	}
	encoded = base64.RawURLEncoding.EncodeToString(secret)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO public_share_config (key, value, created_at, updated_at)
		VALUES (?, ?, ?, ?)`,
		publicChatShareSaltKey,
		encoded,
		now,
		now,
	); err != nil {
		return nil, fmt.Errorf("store public share salt: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM public_share_config WHERE key = ?`, publicChatShareSaltKey).Scan(&encoded); err != nil {
		return nil, fmt.Errorf("reload public share salt: %w", err)
	}
	return decodePublicChatShareSalt(encoded)
}

func decodePublicChatShareSalt(encoded string) ([]byte, error) {
	secret, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(secret) < 32 {
		return nil, fmt.Errorf("public share salt is invalid")
	}
	return secret, nil
}

func normalizePublicChatShareInput(input PublicChatShareInput) PublicChatShareInput {
	input.OwnerProvider = normalizeOwnerProvider(input.OwnerProvider)
	input.OwnerSubject = strings.TrimSpace(input.OwnerSubject)
	input.OwnerUsername = strings.TrimSpace(input.OwnerUsername)
	input.Title = singleLine(input.Title)
	input.Summary = singleLine(input.Summary)
	input.SanitizedContent = strings.TrimSpace(strings.ReplaceAll(input.SanitizedContent, "\r\n", "\n"))
	input.Categories = cleanStringSlice(input.Categories)
	input.OriginalURLs = cleanStringSlice(input.OriginalURLs)
	return input
}

func normalizeOwnerProvider(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func cleanStringSlice(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func jsonStringArray(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	buf, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func publicChatShareContentHash(content string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return hex.EncodeToString(sum[:])
}

func publicChatShareSlug(secret []byte, ownerProvider string, ownerSubject string, contentHash string) string {
	mac := hmac.New(sha256.New, secret)
	for _, part := range []string{ownerProvider, ownerSubject, contentHash} {
		_, _ = mac.Write([]byte(part))
		_, _ = mac.Write([]byte{0})
	}
	sum := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

func singleLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

const publicChatShareColumns = `
	slug, owner_provider, owner_subject, owner_username, content_hash,
	title, summary, categories_json, sanitized_content, original_urls_json,
	metadata_json, created_at, updated_at`

type publicChatShareScanner interface {
	Scan(dest ...any) error
}

func scanPublicChatShare(scanner publicChatShareScanner) (PublicChatShare, error) {
	var share PublicChatShare
	var categoriesJSON, urlsJSON string
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&share.Slug,
		&share.OwnerProvider,
		&share.OwnerSubject,
		&share.OwnerUsername,
		&share.ContentHash,
		&share.Title,
		&share.Summary,
		&categoriesJSON,
		&share.SanitizedContent,
		&urlsJSON,
		&share.MetadataJSON,
		&createdAt,
		&updatedAt,
	); err != nil {
		return PublicChatShare{}, err
	}
	share.Categories = decodeStringArray(categoriesJSON)
	share.OriginalURLs = decodeStringArray(urlsJSON)
	share.CreatedAt = parseStoredTime(createdAt)
	share.UpdatedAt = parseStoredTime(updatedAt)
	return share, nil
}

func decodeStringArray(raw string) []string {
	var values []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &values); err != nil {
		return []string{}
	}
	return cleanStringSlice(values)
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
