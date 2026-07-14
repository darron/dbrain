package feedimport

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/version"
)

const feedAuditOverallTimeout = 5 * time.Minute

type feedAuditInventory struct {
	feeds     []store.Feed
	userAgent string
	fetcher   Fetcher
}

type feedAuditHTTPInjections struct {
	LookupNetIP     func(context.Context, string, string) ([]netip.Addr, error)
	DialContext     func(context.Context, string, string) (net.Conn, error)
	TLSClientConfig *tls.Config
}

// NewAuditInventory constructs a bounded read-only inventory over configured
// feed rows supplied by the caller's existing query-only snapshot. The public
// API deliberately exposes no HTTP client, transport, DNS, redirect, or URL
// override capability.
func NewAuditInventory(feeds []store.Feed, userAgent string, allowPrivateNetwork bool) audit.UpstreamInventory {
	configured := enabledAuditFeeds(feeds)
	return &feedAuditInventory{
		feeds:     configured,
		userAgent: version.UserAgent(userAgent),
		fetcher:   newFeedAuditHTTPFetcher(configured, allowPrivateNetwork, feedAuditHTTPInjections{}),
	}
}

func newAuditInventory(feeds []store.Feed, userAgent string, fetcher Fetcher) audit.UpstreamInventory {
	return &feedAuditInventory{feeds: enabledAuditFeeds(feeds), userAgent: strings.TrimSpace(userAgent), fetcher: fetcher}
}

func enabledAuditFeeds(feeds []store.Feed) []store.Feed {
	capacity := min(len(feeds), audit.InventoryMaxPages+1)
	enabled := make([]store.Feed, 0, capacity)
	for _, feed := range feeds {
		if feed.Enabled {
			enabled = append(enabled, feed)
			if len(enabled) == audit.InventoryMaxPages+1 {
				break
			}
		}
	}
	return enabled
}

func (i *feedAuditInventory) Inventory(ctx context.Context, budget audit.InventoryBudget) (audit.InventoryResult, error) {
	result := audit.InventoryResult{}
	if err := validateFeedAuditBudget(budget); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if i == nil || i.fetcher == nil {
		return result, fmt.Errorf("%w: feed audit inventory unavailable", audit.ErrInventoryInvalid)
	}

	bounded, cancel := context.WithTimeout(ctx, feedAuditOverallTimeout)
	defer cancel()
	seen := make(map[string]struct{}, min(budget.MaxIdentities, 1024))
	for _, feed := range i.feeds {
		if err := bounded.Err(); err != nil {
			result.IdentityHashes = sortedFeedAuditHashes(seen)
			return result, err
		}
		if result.PageCount == budget.MaxPages {
			result.IdentityHashes = sortedFeedAuditHashes(seen)
			return result, fmt.Errorf("%w: feed page budget exhausted", audit.ErrInventoryBudget)
		}
		fetch, err := i.fetcher.Fetch(bounded, feed, Options{
			Force: true, Timeout: DefaultTimeout, MaxBodyBytes: DefaultMaxBodyBytes, UserAgent: i.userAgent,
		})
		if err != nil {
			result.IdentityHashes = sortedFeedAuditHashes(seen)
			if contextErr := bounded.Err(); contextErr != nil {
				return result, contextErr
			}
			return result, fmt.Errorf("feed audit fetch failed")
		}
		result.PageCount++
		if fetch.NotModified || fetch.UnchangedBody || fetch.HTTPStatus < http.StatusOK || fetch.HTTPStatus >= http.StatusMultipleChoices || len(fetch.DecodedBody) == 0 {
			result.IdentityHashes = sortedFeedAuditHashes(seen)
			return result, fmt.Errorf("%w: feed audit response invalid", audit.ErrInventoryInvalid)
		}
		parsed, err := gofeed.NewParser().Parse(bytes.NewReader(fetch.DecodedBody))
		if err != nil || parsed == nil {
			result.IdentityHashes = sortedFeedAuditHashes(seen)
			return result, fmt.Errorf("%w: feed audit parse failed", audit.ErrInventoryInvalid)
		}
		for _, item := range parsed.Items {
			if err := bounded.Err(); err != nil {
				result.IdentityHashes = sortedFeedAuditHashes(seen)
				return result, err
			}
			if item == nil {
				result.IdentityHashes = sortedFeedAuditHashes(seen)
				return result, fmt.Errorf("%w: feed audit item invalid", audit.ErrInventoryInvalid)
			}
			aliases := feedItemIdentityAliases(item, extractMarkdown(item.Extensions, item.Custom), textFromHTML(item.Content), textFromHTML(item.Description))
			if len(aliases) == 0 {
				result.IdentityHashes = sortedFeedAuditHashes(seen)
				return result, fmt.Errorf("%w: feed audit identity invalid", audit.ErrInventoryInvalid)
			}
			for _, identity := range aliases {
				hash, hashErr := audit.HashUpstreamFeedIdentity(feed.FeedKey, identity)
				if hashErr != nil {
					result.IdentityHashes = sortedFeedAuditHashes(seen)
					return result, fmt.Errorf("%w: feed audit identity invalid", audit.ErrInventoryInvalid)
				}
				if _, exists := seen[hash]; exists {
					continue
				}
				if len(seen) == budget.MaxIdentities {
					result.IdentityHashes = sortedFeedAuditHashes(seen)
					return result, fmt.Errorf("%w: feed identity budget exhausted", audit.ErrInventoryBudget)
				}
				seen[hash] = struct{}{}
			}
		}
	}
	result.IdentityHashes = sortedFeedAuditHashes(seen)
	result.Complete = true
	return result, nil
}

func validateFeedAuditBudget(budget audit.InventoryBudget) error {
	if budget.MaxIdentities <= 0 || budget.MaxIdentities > audit.InventoryMaxIdentities || budget.MaxPages <= 0 || budget.MaxPages > audit.InventoryMaxPages {
		return fmt.Errorf("%w: feed audit budget", audit.ErrInventoryInvalid)
	}
	return nil
}

func sortedFeedAuditHashes(seen map[string]struct{}) []string {
	hashes := make([]string, 0, len(seen))
	for hash := range seen {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	return hashes
}

func newFeedAuditHTTPFetcher(feeds []store.Feed, allowPrivateNetwork bool, injected feedAuditHTTPInjections) Fetcher {
	tlsConfig := injected.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	privateOrigins := []string(nil)
	if allowPrivateNetwork {
		privateOrigins = configuredFeedOrigins(feeds)
	}
	client := safehttp.NewClient(safehttp.Policy{
		Timeout:               DefaultTimeout,
		MaxRedirects:          10,
		AllowedPrivateOrigins: privateOrigins,
		DisableCompression:    true,
		LookupNetIP:           injected.LookupNetIP,
		DialContext:           injected.DialContext,
		TLSClientConfig:       tlsConfig,
		ConnectTimeout:        5 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	})
	return HTTPFetcher{client: client}
}

func configuredFeedOrigins(feeds []store.Feed) []string {
	seen := make(map[string]struct{}, len(feeds))
	origins := make([]string, 0, len(feeds))
	for _, feed := range feeds {
		target := firstNonEmpty(feed.ResolvedURL, feed.NormalizedURL, feed.URL)
		parsed, err := url.Parse(strings.TrimSpace(target))
		if err != nil || parsed.User != nil || parsed.Opaque != "" {
			continue
		}
		candidate := (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
		origin, err := safehttp.CanonicalOriginEndpoint(candidate)
		if err != nil {
			continue
		}
		if _, exists := seen[origin]; exists {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	sort.Strings(origins)
	return origins
}
