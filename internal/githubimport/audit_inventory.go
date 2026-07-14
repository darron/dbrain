package githubimport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/version"
)

const (
	githubAuditResponseMaxBytes = 16 << 20
	githubAuditRequestTimeout   = 45 * time.Second
	githubAuditPageSize         = 100
)

type githubAuditTokenResolver func(context.Context, string) (string, error)

// githubAuditHTTPInjections keeps tests beneath the fixed safehttp policy. It
// cannot change the request origin, proxy behavior, redirects, or time bounds.
type githubAuditHTTPInjections struct {
	LookupNetIP     func(context.Context, string, string) ([]netip.Addr, error)
	DialContext     func(context.Context, string, string) (net.Conn, error)
	TLSClientConfig *tls.Config
}

type githubAuditInventory struct {
	rootDir      string
	userAgent    string
	resolveToken githubAuditTokenResolver
	httpClient   *http.Client
}

// NewAuditInventory returns a read-only, fixed-origin GitHub Stars inventory.
// Credential resolution remains lazy so standard audits do not touch secrets.
func NewAuditInventory(rootDir, userAgent string) audit.UpstreamInventory {
	return newGitHubAuditInventory(rootDir, version.UserAgent(userAgent), func(ctx context.Context, root string) (string, error) {
		return runtimeenv.FirstNonEmptySecret(ctx, root, "GITHUB_TOKEN")
	}, githubAuditHTTPInjections{})
}

func newGitHubAuditInventory(rootDir, userAgent string, resolver githubAuditTokenResolver, injected githubAuditHTTPInjections) audit.UpstreamInventory {
	return &githubAuditInventory{
		rootDir:      rootDir,
		userAgent:    strings.TrimSpace(userAgent),
		resolveToken: resolver,
		httpClient:   newGitHubAuditHTTPClient(injected),
	}
}

func newGitHubAuditHTTPClient(injected githubAuditHTTPInjections) *http.Client {
	tlsConfig := injected.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return safehttp.NewClient(safehttp.Policy{
		Timeout:               githubAuditRequestTimeout,
		DisableRedirects:      true,
		AllowedOrigins:        []string{defaultAPIBaseURL},
		LookupNetIP:           injected.LookupNetIP,
		DialContext:           injected.DialContext,
		TLSClientConfig:       tlsConfig,
		ConnectTimeout:        5 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	})
}

func (i *githubAuditInventory) Inventory(ctx context.Context, budget audit.InventoryBudget) (audit.InventoryResult, error) {
	result := audit.InventoryResult{}
	if err := validateGitHubAuditBudget(budget); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if i.resolveToken == nil {
		return result, fmt.Errorf("github audit credential resolver unavailable")
	}
	token, err := i.resolveToken(ctx, i.rootDir)
	if err != nil {
		return result, fmt.Errorf("github audit credential resolution failed")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return result, fmt.Errorf("github audit credential missing")
	}

	var account viewer
	if err := i.getJSON(ctx, token, "/user", "", "application/vnd.github+json", &account); err != nil {
		return result, err
	}
	account.Login = strings.TrimSpace(account.Login)
	if account.Login == "" {
		return result, fmt.Errorf("%w: github viewer login missing", audit.ErrInventoryInvalid)
	}

	seen := make(map[string]struct{}, min(budget.MaxIdentities, 1024))
	for page := 1; ; page++ {
		if page > budget.MaxPages {
			result.IdentityHashes = sortedGitHubAuditHashes(seen)
			return result, fmt.Errorf("%w: github page budget exhausted", audit.ErrInventoryBudget)
		}
		query := url.Values{}
		query.Set("sort", "created")
		query.Set("direction", "desc")
		query.Set("per_page", fmt.Sprint(githubAuditPageSize))
		query.Set("page", fmt.Sprint(page))
		var records []starRecord
		if err := i.getJSON(ctx, token, "/user/starred", query.Encode(), "application/vnd.github.star+json", &records); err != nil {
			result.IdentityHashes = sortedGitHubAuditHashes(seen)
			return result, err
		}
		result.PageCount++
		if records == nil {
			result.IdentityHashes = sortedGitHubAuditHashes(seen)
			return result, fmt.Errorf("%w: github starred response invalid", audit.ErrInventoryInvalid)
		}
		if len(records) == 0 {
			result.IdentityHashes = sortedGitHubAuditHashes(seen)
			result.Complete = true
			return result, nil
		}
		if len(records) > githubAuditPageSize {
			result.IdentityHashes = sortedGitHubAuditHashes(seen)
			return result, fmt.Errorf("%w: github page size invalid", audit.ErrInventoryInvalid)
		}
		for _, record := range records {
			sourceKey, err := githubStarSourceKey(account.Login, record.Repo.FullName)
			if err != nil {
				result.IdentityHashes = sortedGitHubAuditHashes(seen)
				return result, fmt.Errorf("%w: github star identity invalid", audit.ErrInventoryInvalid)
			}
			hash, err := audit.HashUpstreamIdentity(audit.SourceGitHubStars, sourceKey)
			if err != nil {
				result.IdentityHashes = sortedGitHubAuditHashes(seen)
				return result, fmt.Errorf("%w: github star identity invalid", audit.ErrInventoryInvalid)
			}
			if _, exists := seen[hash]; exists {
				continue
			}
			if len(seen) == budget.MaxIdentities {
				result.IdentityHashes = sortedGitHubAuditHashes(seen)
				return result, fmt.Errorf("%w: github identity budget exhausted", audit.ErrInventoryBudget)
			}
			seen[hash] = struct{}{}
		}
		if len(records) < githubAuditPageSize {
			result.IdentityHashes = sortedGitHubAuditHashes(seen)
			result.Complete = true
			return result, nil
		}
	}
}

func validateGitHubAuditBudget(budget audit.InventoryBudget) error {
	if budget.MaxIdentities <= 0 || budget.MaxIdentities > audit.InventoryMaxIdentities || budget.MaxPages <= 0 || budget.MaxPages > audit.InventoryMaxPages {
		return fmt.Errorf("%w: github inventory budget invalid", audit.ErrInventoryInvalid)
	}
	return nil
}

func (i *githubAuditInventory) getJSON(ctx context.Context, token, path, rawQuery, accept string, target any) error {
	endpoint := url.URL{Scheme: "https", Host: "api.github.com", Path: path, RawQuery: rawQuery}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("github audit request construction failed")
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", i.userAgent)
	resp, err := i.httpClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("github audit request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github audit request failed with status %d", resp.StatusCode)
	}
	if resp.ContentLength > githubAuditResponseMaxBytes {
		return fmt.Errorf("github audit response exceeds size limit")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, githubAuditResponseMaxBytes+1))
	if err != nil {
		return fmt.Errorf("github audit response read failed")
	}
	if len(body) > githubAuditResponseMaxBytes {
		return fmt.Errorf("github audit response exceeds size limit")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("github audit response invalid")
	}
	return nil
}

func sortedGitHubAuditHashes(seen map[string]struct{}) []string {
	hashes := make([]string, 0, len(seen))
	for hash := range seen {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	return hashes
}
