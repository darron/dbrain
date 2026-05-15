package store

import (
	"context"
	"path/filepath"
	"strconv"
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

func TestListAndRevokeMCPBearerTokens(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)

	laptop, err := st.CreateMCPBearerToken(ctx, "laptop")
	if err != nil {
		t.Fatalf("CreateMCPBearerToken laptop: %v", err)
	}
	phone, err := st.CreateMCPBearerToken(ctx, "phone")
	if err != nil {
		t.Fatalf("CreateMCPBearerToken phone: %v", err)
	}

	tokens, err := st.ListMCPBearerTokens(ctx, false)
	if err != nil {
		t.Fatalf("ListMCPBearerTokens active: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("expected two active tokens, got %#v", tokens)
	}
	for _, record := range tokens {
		if record.TokenFingerprint == laptop.Token || record.TokenFingerprint == phone.Token || strings.Contains(record.TokenFingerprint, mcpBearerTokenPrefix) {
			t.Fatalf("token fingerprint should not expose raw token material: record=%#v", record)
		}
	}

	revoked, changed, err := st.RevokeMCPBearerToken(ctx, phone.Record.TokenFingerprint)
	if err != nil {
		t.Fatalf("RevokeMCPBearerToken: %v", err)
	}
	if !changed || revoked.ID != phone.Record.ID || revoked.RevokedAt == "" {
		t.Fatalf("unexpected revoked token: changed=%v record=%#v", changed, revoked)
	}
	if ok, err := st.ValidateMCPBearerToken(ctx, phone.Token); err != nil {
		t.Fatalf("ValidateMCPBearerToken revoked: %v", err)
	} else if ok {
		t.Fatalf("revoked token should not validate")
	}
	if ok, err := st.ValidateMCPBearerToken(ctx, laptop.Token); err != nil {
		t.Fatalf("ValidateMCPBearerToken active: %v", err)
	} else if !ok {
		t.Fatalf("active token should still validate")
	}

	active, err := st.ListMCPBearerTokens(ctx, false)
	if err != nil {
		t.Fatalf("ListMCPBearerTokens after revoke: %v", err)
	}
	if len(active) != 1 || active[0].ID != laptop.Record.ID {
		t.Fatalf("expected only laptop active, got %#v", active)
	}
	all, err := st.ListMCPBearerTokens(ctx, true)
	if err != nil {
		t.Fatalf("ListMCPBearerTokens all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected active and revoked tokens with includeRevoked, got %#v", all)
	}
	if _, changed, err := st.RevokeMCPBearerToken(ctx, strconv.FormatInt(phone.Record.ID, 10)); err != nil {
		t.Fatalf("RevokeMCPBearerToken already revoked: %v", err)
	} else if changed {
		t.Fatalf("already revoked token should not report changed")
	}
}
