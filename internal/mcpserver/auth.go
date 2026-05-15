package mcpserver

import (
	"context"
	"fmt"
	"io"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/store"
)

type BearerTokenValidator func(context.Context, string) (bool, error)

type BearerTokenAuthenticator func(context.Context, string) (BearerTokenIdentity, bool, error)

type BearerTokenIdentity struct {
	Name        string
	Fingerprint string
}

func AuthEnabled(cfg config.Config) bool {
	return runtimeenv.FirstBoolDefault(cfg.RootDir, false, "DBRAIN_MCP_AUTH_ENABLED")
}

func ApplyRuntimeAuthOptions(cfg config.Config, st *store.Store, opts *HTTPOptions) error {
	if opts == nil || !AuthEnabled(cfg) {
		return nil
	}
	if st == nil {
		return fmt.Errorf("mcp bearer auth requires a store")
	}
	if _, err := st.ActiveMCPBearerTokenCount(context.Background()); err != nil {
		return fmt.Errorf("mcp bearer auth is enabled but the token table is unavailable; create a token with dbrain auth mcp token add NAME: %w", err)
	}
	opts.RequireBearerAuth = true
	opts.BearerTokenAuthenticator = storeBearerTokenAuthenticator(st)
	return nil
}

func storeBearerTokenAuthenticator(st *store.Store) BearerTokenAuthenticator {
	return func(ctx context.Context, token string) (BearerTokenIdentity, bool, error) {
		record, found, err := st.GetMCPBearerTokenByRawToken(ctx, token)
		if err != nil || !found {
			return BearerTokenIdentity{}, found, err
		}
		return BearerTokenIdentity{
			Name:        record.Name,
			Fingerprint: record.TokenFingerprint,
		}, true, nil
	}
}

func WriteOpenAuthWarning(out io.Writer, surface string) {
	if out == nil {
		return
	}
	if surface == "" {
		surface = "MCP"
	}
	_, _ = fmt.Fprintf(out, "WARNING MCP AUTH DISABLED: %s is serving MCP without dbrain bearer-token auth. This is acceptable only on private localhost or a trusted tailnet. If exposed with Tailscale Funnel, a public reverse proxy, or a non-private listener, public clients can read local brain content over MCP. Enable mcp.auth.enabled=true and create a token with: dbrain auth mcp token add NAME. Otherwise disable MCP on public surfaces.\n", surface)
}
