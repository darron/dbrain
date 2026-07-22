package xapi

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
	"github.com/darron/dbrain/internal/safehttp"
)

const (
	bookmarkAuditOrigin           = "https://x.com"
	bookmarkAuditPageSize         = 100
	bookmarkAuditMaxBodyBytes     = 8 << 20
	bookmarkAuditMaxRequestTime   = 45 * time.Second
	bookmarkAuditMaxCursorBytes   = 4 << 10
	bookmarkAuditConnectTimeout   = 5 * time.Second
	bookmarkAuditTLSHeaderTimeout = 10 * time.Second
)

type bookmarkAuditCookieResolver func(context.Context, Options) (string, string, error)

// bookmarkAuditHTTPInjections keep tests underneath the immutable x.com
// safehttp policy. They cannot broaden origin, redirect, proxy, or time limits.
type bookmarkAuditHTTPInjections struct {
	LookupNetIP     func(context.Context, string, string) ([]netip.Addr, error)
	DialContext     func(context.Context, string, string) (net.Conn, error)
	TLSClientConfig *tls.Config
}

type bookmarkAuditInventory struct {
	opts           BookmarkOptions
	resolveCookies bookmarkAuditCookieResolver
	httpClient     *http.Client
}

// NewBookmarkAuditInventory constructs a read-only full X bookmark inventory.
// Browser cookie access is deferred until Inventory is explicitly invoked.
func NewBookmarkAuditInventory(opts BookmarkOptions) audit.UpstreamInventory {
	return newBookmarkAuditInventory(opts, resolveCookies, bookmarkAuditHTTPInjections{})
}

func newBookmarkAuditInventory(opts BookmarkOptions, resolver bookmarkAuditCookieResolver, injected bookmarkAuditHTTPInjections) audit.UpstreamInventory {
	timeout := opts.Timeout
	if timeout <= 0 || timeout > bookmarkAuditMaxRequestTime {
		timeout = bookmarkAuditMaxRequestTime
	}
	opts.Timeout = timeout
	tlsConfig := injected.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	client := safehttp.NewClient(safehttp.Policy{
		Timeout:               timeout,
		DisableRedirects:      true,
		AllowedOrigins:        []string{bookmarkAuditOrigin},
		LookupNetIP:           injected.LookupNetIP,
		DialContext:           injected.DialContext,
		TLSClientConfig:       tlsConfig,
		ConnectTimeout:        bookmarkAuditConnectTimeout,
		TLSHandshakeTimeout:   bookmarkAuditConnectTimeout,
		ResponseHeaderTimeout: bookmarkAuditTLSHeaderTimeout,
	})
	return &bookmarkAuditInventory{opts: opts, resolveCookies: resolver, httpClient: client}
}

func (i *bookmarkAuditInventory) Inventory(ctx context.Context, budget audit.InventoryBudget) (audit.InventoryResult, error) {
	result := audit.InventoryResult{}
	if err := validateBookmarkAuditBudget(budget); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	csrfToken := strings.TrimSpace(i.opts.CT0)
	authToken := strings.TrimSpace(i.opts.AuthToken)
	if csrfToken == "" {
		if i.resolveCookies == nil {
			return result, fmt.Errorf("x bookmark audit session resolver unavailable")
		}
		resolvedCSRF, resolvedAuth, err := i.resolveCookies(ctx, Options{
			Browser: i.opts.Browser, Profile: i.opts.Profile, Timeout: i.opts.Timeout,
		})
		if err != nil {
			return result, fmt.Errorf("x bookmark audit session resolution failed")
		}
		csrfToken = strings.TrimSpace(resolvedCSRF)
		authToken = strings.TrimSpace(resolvedAuth)
	}
	if csrfToken == "" {
		return result, fmt.Errorf("x bookmark audit session missing")
	}
	cookieHeader := "ct0=" + csrfToken
	if authToken != "" {
		cookieHeader += "; auth_token=" + authToken
	}

	seenHashes := make(map[string]struct{}, min(budget.MaxIdentities, 1024))
	seenCursors := make(map[string]struct{})
	cursor := ""
	for pageNumber := 1; ; pageNumber++ {
		if pageNumber > budget.MaxPages {
			result.IdentityHashes = sortedBookmarkAuditHashes(seenHashes)
			return result, fmt.Errorf("%w: x bookmark page budget exhausted", audit.ErrInventoryBudget)
		}
		page, err := i.fetchAuditPage(ctx, csrfToken, cookieHeader, cursor)
		if err != nil {
			result.IdentityHashes = sortedBookmarkAuditHashes(seenHashes)
			return result, err
		}
		result.PageCount++
		if len(page.Records) > bookmarkAuditPageSize {
			result.IdentityHashes = sortedBookmarkAuditHashes(seenHashes)
			return result, fmt.Errorf("%w: x bookmark page size invalid", audit.ErrInventoryInvalid)
		}
		for _, record := range page.Records {
			sourceKey, err := bookmarkSourceKey(record.TweetID)
			if err != nil {
				result.IdentityHashes = sortedBookmarkAuditHashes(seenHashes)
				return result, fmt.Errorf("%w: x bookmark identity invalid", audit.ErrInventoryInvalid)
			}
			hash, err := audit.HashUpstreamIdentity(audit.SourceXBookmarks, sourceKey)
			if err != nil {
				result.IdentityHashes = sortedBookmarkAuditHashes(seenHashes)
				return result, fmt.Errorf("%w: x bookmark identity invalid", audit.ErrInventoryInvalid)
			}
			if _, exists := seenHashes[hash]; exists {
				continue
			}
			if len(seenHashes) == budget.MaxIdentities {
				result.IdentityHashes = sortedBookmarkAuditHashes(seenHashes)
				return result, fmt.Errorf("%w: x bookmark identity budget exhausted", audit.ErrInventoryBudget)
			}
			seenHashes[hash] = struct{}{}
		}
		if page.NextCursor == "" {
			result.IdentityHashes = sortedBookmarkAuditHashes(seenHashes)
			if len(page.Records) != 0 {
				return result, fmt.Errorf("%w: x bookmark traversal ended without terminal evidence", audit.ErrInventoryIncomplete)
			}
			result.Complete = true
			return result, nil
		}
		if _, exists := seenCursors[page.NextCursor]; exists {
			result.IdentityHashes = sortedBookmarkAuditHashes(seenHashes)
			return result, fmt.Errorf("%w: x bookmark cursor cycle", audit.ErrInventoryInvalid)
		}
		seenCursors[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
}

func validateBookmarkAuditBudget(budget audit.InventoryBudget) error {
	if budget.MaxIdentities <= 0 || budget.MaxIdentities > audit.InventoryMaxIdentities || budget.MaxPages <= 0 || budget.MaxPages > audit.InventoryMaxPages {
		return fmt.Errorf("%w: x bookmark inventory budget invalid", audit.ErrInventoryInvalid)
	}
	return nil
}

func (i *bookmarkAuditInventory) fetchAuditPage(ctx context.Context, csrfToken, cookieHeader, cursor string) (bookmarkPage, error) {
	endpoint, err := bookmarkAuditURL(cursor)
	if err != nil {
		return bookmarkPage{}, fmt.Errorf("x bookmark audit request construction failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return bookmarkPage{}, fmt.Errorf("x bookmark audit request construction failed")
	}
	for key, value := range buildHeaders(csrfToken, cookieHeader) {
		req.Header.Set(key, value)
	}
	resp, err := i.httpClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return bookmarkPage{}, ctxErr
		}
		return bookmarkPage{}, fmt.Errorf("x bookmark audit request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return bookmarkPage{}, fmt.Errorf("x bookmark audit request failed with status %d", resp.StatusCode)
	}
	if resp.ContentLength > bookmarkAuditMaxBodyBytes {
		return bookmarkPage{}, fmt.Errorf("x bookmark audit response exceeds size limit")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, bookmarkAuditMaxBodyBytes+1))
	if err != nil {
		return bookmarkPage{}, fmt.Errorf("x bookmark audit response read failed")
	}
	if len(body) > bookmarkAuditMaxBodyBytes {
		return bookmarkPage{}, fmt.Errorf("x bookmark audit response exceeds size limit")
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return bookmarkPage{}, fmt.Errorf("x bookmark audit response invalid")
	}
	return parseBookmarkAuditPage(payload)
}

func bookmarkAuditURL(cursor string) (string, error) {
	variables := map[string]any{"count": bookmarkAuditPageSize}
	if cursor != "" {
		variables["cursor"] = cursor
	}
	encodedVariables, err := json.Marshal(variables)
	if err != nil {
		return "", err
	}
	encodedFeatures, err := json.Marshal(bookmarkTimelineFeatures)
	if err != nil {
		return "", err
	}
	query := url.Values{}
	query.Set("variables", string(encodedVariables))
	query.Set("features", string(encodedFeatures))
	endpoint := url.URL{
		Scheme: "https", Host: "x.com",
		Path:     "/i/api/graphql/" + bookmarksQueryID + "/" + bookmarksOperation,
		RawQuery: query.Encode(),
	}
	return endpoint.String(), nil
}

func parseBookmarkAuditPage(payload map[string]any) (bookmarkPage, error) {
	if rawErrors, exists := payload["errors"]; exists && rawErrors != nil {
		errorsList, ok := rawErrors.([]any)
		if !ok || len(errorsList) != 0 {
			return bookmarkPage{}, fmt.Errorf("%w: x bookmark response contains errors", audit.ErrInventoryInvalid)
		}
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		return bookmarkPage{}, fmt.Errorf("%w: x bookmark response shape invalid", audit.ErrInventoryInvalid)
	}
	timelineContainer, ok := data["bookmark_timeline_v2"].(map[string]any)
	if !ok {
		return bookmarkPage{}, fmt.Errorf("%w: x bookmark response shape invalid", audit.ErrInventoryInvalid)
	}
	timeline, ok := timelineContainer["timeline"].(map[string]any)
	if !ok {
		return bookmarkPage{}, fmt.Errorf("%w: x bookmark response shape invalid", audit.ErrInventoryInvalid)
	}
	instructions, ok := timeline["instructions"].([]any)
	if !ok {
		return bookmarkPage{}, fmt.Errorf("%w: x bookmark response shape invalid", audit.ErrInventoryInvalid)
	}
	foundEntries := false
	cursorCount := 0
	for _, rawInstruction := range instructions {
		instruction, ok := rawInstruction.(map[string]any)
		if !ok {
			return bookmarkPage{}, fmt.Errorf("%w: x bookmark response shape invalid", audit.ErrInventoryInvalid)
		}
		if stringValue(instruction["type"]) != "TimelineAddEntries" {
			continue
		}
		foundEntries = true
		entries, ok := instruction["entries"].([]any)
		if !ok {
			return bookmarkPage{}, fmt.Errorf("%w: x bookmark response shape invalid", audit.ErrInventoryInvalid)
		}
		for _, rawEntry := range entries {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				return bookmarkPage{}, fmt.Errorf("%w: x bookmark response shape invalid", audit.ErrInventoryInvalid)
			}
			entryID := stringValue(entry["entryId"])
			if strings.HasPrefix(entryID, "cursor-bottom") {
				cursorCount++
				cursor := stringValue(dig(entry, "content")["value"])
				if cursorCount > 1 || !validBookmarkAuditCursor(cursor) {
					return bookmarkPage{}, fmt.Errorf("%w: x bookmark cursor invalid", audit.ErrInventoryInvalid)
				}
				continue
			}
			if strings.HasPrefix(entryID, "tweet-") {
				result := dig(dig(dig(entry, "content"), "itemContent"), "tweet_results", "result")
				if parseBookmarkRecord(result, stringValue(entry["sortIndex"]), time.Time{}) == nil {
					return bookmarkPage{}, fmt.Errorf("%w: x bookmark record invalid", audit.ErrInventoryInvalid)
				}
			}
		}
	}
	if !foundEntries {
		return bookmarkPage{}, fmt.Errorf("%w: x bookmark response shape invalid", audit.ErrInventoryInvalid)
	}
	page := parseBookmarksResponse(payload, time.Time{})
	if cursorCount == 0 && page.NextCursor != "" {
		return bookmarkPage{}, fmt.Errorf("%w: x bookmark cursor invalid", audit.ErrInventoryInvalid)
	}
	return page, nil
}

func validBookmarkAuditCursor(cursor string) bool {
	if cursor == "" || len(cursor) > bookmarkAuditMaxCursorBytes || strings.TrimSpace(cursor) != cursor {
		return false
	}
	for _, char := range cursor {
		if char < 0x21 || char == 0x7f {
			return false
		}
	}
	return true
}

func sortedBookmarkAuditHashes(seen map[string]struct{}) []string {
	hashes := make([]string, 0, len(seen))
	for hash := range seen {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	return hashes
}
