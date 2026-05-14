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

func (s *Store) ValidateMCPBearerToken(ctx context.Context, token string) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return false, nil
	}
	hash := HashMCPBearerToken(token)
	var id int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id
		FROM mcp_bearer_tokens
		WHERE token_hash = ? AND revoked_at = ''
		LIMIT 1`,
		hash,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("validate mcp bearer token: %w", err)
	}
	return true, nil
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
