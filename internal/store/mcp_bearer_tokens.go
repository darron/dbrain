package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const mcpBearerTokenPrefix = "dbrain_mcp_"

type MCPBearerToken struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	TokenFingerprint string `json:"token_fingerprint"`
	RevokedAt        string `json:"revoked_at,omitempty"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type MCPBearerTokenCreateResult struct {
	Token  string         `json:"token"`
	Record MCPBearerToken `json:"record"`
}

func GenerateMCPBearerToken() (string, error) {
	var randomBytes [32]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return "", fmt.Errorf("generate mcp bearer token: %w", err)
	}
	return mcpBearerTokenPrefix + base64.RawURLEncoding.EncodeToString(randomBytes[:]), nil
}

func HashMCPBearerToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func (s *Store) CreateMCPBearerToken(ctx context.Context, name string) (MCPBearerTokenCreateResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return MCPBearerTokenCreateResult{}, fmt.Errorf("mcp bearer token name is required")
	}

	token, err := GenerateMCPBearerToken()
	if err != nil {
		return MCPBearerTokenCreateResult{}, err
	}
	hash := HashMCPBearerToken(token)
	now := time.Now().UTC().Format(time.RFC3339)

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO mcp_bearer_tokens (
			name, token_hash, token_fingerprint, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?)`,
		name,
		hash,
		tokenFingerprint(hash),
		now,
		now,
	)
	if err != nil {
		return MCPBearerTokenCreateResult{}, fmt.Errorf("create mcp bearer token: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return MCPBearerTokenCreateResult{}, fmt.Errorf("read created mcp bearer token id: %w", err)
	}

	record, found, err := s.GetMCPBearerTokenByID(ctx, id)
	if err != nil {
		return MCPBearerTokenCreateResult{}, err
	}
	if !found {
		return MCPBearerTokenCreateResult{}, fmt.Errorf("created mcp bearer token was not readable after insert")
	}
	return MCPBearerTokenCreateResult{Token: token, Record: record}, nil
}

func (s *Store) GetMCPBearerTokenByID(ctx context.Context, id int64) (MCPBearerToken, bool, error) {
	if id <= 0 {
		return MCPBearerToken{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+mcpBearerTokenColumns+`
		FROM mcp_bearer_tokens
		WHERE id = ?
		LIMIT 1`,
		id,
	)
	record, err := scanMCPBearerToken(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MCPBearerToken{}, false, nil
		}
		return MCPBearerToken{}, false, fmt.Errorf("load mcp bearer token by id: %w", err)
	}
	return record, true, nil
}

func (s *Store) ListMCPBearerTokens(ctx context.Context, includeRevoked bool) ([]MCPBearerToken, error) {
	query := `
		SELECT ` + mcpBearerTokenColumns + `
		FROM mcp_bearer_tokens`
	if !includeRevoked {
		query += `
		WHERE revoked_at = ''`
	}
	query += `
		ORDER BY CASE WHEN revoked_at = '' THEN 0 ELSE 1 END, updated_at DESC, id DESC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list mcp bearer tokens: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	tokens := []MCPBearerToken{}
	for rows.Next() {
		record, err := scanMCPBearerToken(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mcp bearer token: %w", err)
		}
		tokens = append(tokens, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mcp bearer tokens: %w", err)
	}
	return tokens, nil
}

func (s *Store) RevokeMCPBearerToken(ctx context.Context, ref string) (MCPBearerToken, bool, error) {
	record, found, err := s.resolveMCPBearerTokenRef(ctx, ref)
	if err != nil {
		return MCPBearerToken{}, false, err
	}
	if !found {
		return MCPBearerToken{}, false, nil
	}
	if record.RevokedAt != "" {
		return record, false, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx, `
		UPDATE mcp_bearer_tokens
		SET revoked_at = ?, updated_at = ?
		WHERE id = ? AND revoked_at = ''`,
		now,
		now,
		record.ID,
	)
	if err != nil {
		return MCPBearerToken{}, false, fmt.Errorf("revoke mcp bearer token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return MCPBearerToken{}, false, fmt.Errorf("check revoked mcp bearer token: %w", err)
	}
	record, found, err = s.GetMCPBearerTokenByID(ctx, record.ID)
	if err != nil {
		return MCPBearerToken{}, false, err
	}
	if !found {
		return MCPBearerToken{}, false, fmt.Errorf("revoked mcp bearer token was not readable after update")
	}
	return record, rows > 0, nil
}

func (s *Store) resolveMCPBearerTokenRef(ctx context.Context, ref string) (MCPBearerToken, bool, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return MCPBearerToken{}, false, fmt.Errorf("mcp bearer token id, name, or fingerprint is required")
	}
	if id, ok := parsePositiveInt64(ref); ok {
		if record, found, err := s.GetMCPBearerTokenByID(ctx, id); err != nil || found {
			return record, found, err
		}
	}
	if record, found, err := s.getMCPBearerTokenByFingerprint(ctx, ref); err != nil || found {
		return record, found, err
	}
	return s.getMCPBearerTokenByName(ctx, ref)
}

func (s *Store) getMCPBearerTokenByFingerprint(ctx context.Context, fingerprint string) (MCPBearerToken, bool, error) {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return MCPBearerToken{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+mcpBearerTokenColumns+`
		FROM mcp_bearer_tokens
		WHERE token_fingerprint = ?
		LIMIT 1`,
		fingerprint,
	)
	record, err := scanMCPBearerToken(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MCPBearerToken{}, false, nil
		}
		return MCPBearerToken{}, false, fmt.Errorf("load mcp bearer token by fingerprint: %w", err)
	}
	return record, true, nil
}

func (s *Store) getMCPBearerTokenByName(ctx context.Context, name string) (MCPBearerToken, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return MCPBearerToken{}, false, nil
	}
	if record, found, err := s.getMCPBearerTokenByNameAndRevocation(ctx, name, false); err != nil || found {
		return record, found, err
	}
	return s.getMCPBearerTokenByNameAndRevocation(ctx, name, true)
}

func (s *Store) getMCPBearerTokenByNameAndRevocation(ctx context.Context, name string, includeRevoked bool) (MCPBearerToken, bool, error) {
	query := `
		SELECT ` + mcpBearerTokenColumns + `
		FROM mcp_bearer_tokens
		WHERE name = ?`
	if !includeRevoked {
		query += `
			AND revoked_at = ''`
	}
	query += `
		ORDER BY CASE WHEN revoked_at = '' THEN 0 ELSE 1 END, updated_at DESC, id DESC
		LIMIT 2`
	rows, err := s.db.QueryContext(ctx, query,
		name,
	)
	if err != nil {
		return MCPBearerToken{}, false, fmt.Errorf("load mcp bearer token by name: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var matches []MCPBearerToken
	for rows.Next() {
		record, err := scanMCPBearerToken(rows)
		if err != nil {
			return MCPBearerToken{}, false, fmt.Errorf("scan mcp bearer token by name: %w", err)
		}
		matches = append(matches, record)
	}
	if err := rows.Err(); err != nil {
		return MCPBearerToken{}, false, fmt.Errorf("iterate mcp bearer token by name: %w", err)
	}
	if len(matches) == 0 {
		return MCPBearerToken{}, false, nil
	}
	if len(matches) > 1 {
		return MCPBearerToken{}, false, fmt.Errorf("multiple mcp bearer tokens named %q; use id or fingerprint", name)
	}
	return matches[0], true, nil
}

func (s *Store) ValidateMCPBearerToken(ctx context.Context, token string) (bool, error) {
	_, found, err := s.GetMCPBearerTokenByRawToken(ctx, token)
	return found, err
}

func (s *Store) GetMCPBearerTokenByRawToken(ctx context.Context, token string) (MCPBearerToken, bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return MCPBearerToken{}, false, nil
	}
	hash := HashMCPBearerToken(token)
	row := s.db.QueryRowContext(ctx, `
		SELECT `+mcpBearerTokenColumns+`
		FROM mcp_bearer_tokens
		WHERE token_hash = ? AND revoked_at = ''
		LIMIT 1`,
		hash,
	)
	record, err := scanMCPBearerToken(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MCPBearerToken{}, false, nil
		}
		return MCPBearerToken{}, false, fmt.Errorf("load mcp bearer token by raw token: %w", err)
	}
	return record, true, nil
}

func (s *Store) ActiveMCPBearerTokenCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM mcp_bearer_tokens
		WHERE revoked_at = ''`,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active mcp bearer tokens: %w", err)
	}
	return count, nil
}

const mcpBearerTokenColumns = `
	id, name, token_fingerprint, revoked_at, created_at, updated_at`

type mcpBearerTokenScanner interface {
	Scan(dest ...any) error
}

func scanMCPBearerToken(row mcpBearerTokenScanner) (MCPBearerToken, error) {
	var record MCPBearerToken
	if err := row.Scan(
		&record.ID,
		&record.Name,
		&record.TokenFingerprint,
		&record.RevokedAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return MCPBearerToken{}, err
	}
	return record, nil
}

func tokenFingerprint(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func parsePositiveInt64(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0
}
