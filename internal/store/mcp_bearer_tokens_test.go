package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAndValidateMCPBearerToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "brain.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	result, err := st.CreateMCPBearerToken(ctx, "laptop")
	if err != nil {
		t.Fatalf("CreateMCPBearerToken: %v", err)
	}
	if !strings.HasPrefix(result.Token, mcpBearerTokenPrefix) {
		t.Fatalf("token prefix = %q, want %q", result.Token, mcpBearerTokenPrefix)
	}
	if result.Record.Name != "laptop" || result.Record.TokenFingerprint == "" || result.Record.CreatedAt == "" {
		t.Fatalf("unexpected created record: %#v", result.Record)
	}
	var tokenHash string
	if err := st.db.QueryRowContext(ctx, `SELECT token_hash FROM mcp_bearer_tokens WHERE id = ?`, result.Record.ID).Scan(&tokenHash); err != nil {
		t.Fatalf("read token hash: %v", err)
	}
	if tokenHash == result.Token || strings.Contains(tokenHash, mcpBearerTokenPrefix) {
		t.Fatalf("stored token hash appears to contain the raw bearer token: %q", tokenHash)
	}
	if count, err := st.ActiveMCPBearerTokenCount(ctx); err != nil {
		t.Fatalf("ActiveMCPBearerTokenCount: %v", err)
	} else if count != 1 {
		t.Fatalf("active token count = %d, want 1", count)
	}
	if ok, err := st.ValidateMCPBearerToken(ctx, result.Token); err != nil {
		t.Fatalf("ValidateMCPBearerToken: %v", err)
	} else if !ok {
		t.Fatalf("expected created token to validate")
	}
	if ok, err := st.ValidateMCPBearerToken(ctx, result.Token+"x"); err != nil {
		t.Fatalf("ValidateMCPBearerToken invalid: %v", err)
	} else if ok {
		t.Fatalf("expected modified token to be rejected")
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close writable store: %v", err)
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open read-only store: %v", err)
	}
	defer func() { _ = ro.Close() }()
	if ok, err := ro.ValidateMCPBearerToken(ctx, result.Token); err != nil {
		t.Fatalf("read-only ValidateMCPBearerToken: %v", err)
	} else if !ok {
		t.Fatalf("expected read-only validation to accept created token")
	}
}
