package mastodonapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/safehttp"
)

const maxBookmarksPerPage = 40

// Client is an origin-scoped authenticated Mastodon API client. The bearer
// token is held only in memory and is never part of a status projection.
type Client struct {
	Origin      string
	AccessToken string
	HTTPClient  *http.Client
}

type BookmarksPage struct {
	URL        string
	Statuses   []StatusRecord
	NextURL    string
	PageDigest string
}

type RetryableError struct {
	StatusCode int
	RetryAfter time.Duration
	Message    string
}

func (e *RetryableError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("Mastodon API retryable error status=%d retry_after=%s", e.StatusCode, e.RetryAfter)
	}
	return fmt.Sprintf("Mastodon API retryable error status=%d: %s", e.StatusCode, e.Message)
}

func NewClient(origin, accessToken string, httpClient *http.Client) (*Client, error) {
	canonical, err := canonicalOrigin(origin)
	if err != nil {
		return nil, fmt.Errorf("validate Mastodon origin: %w", err)
	}
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("mastodon access token is empty")
	}
	if httpClient == nil {
		httpClient = safehttp.NewClient(safehttp.Policy{
			Timeout:                         30 * time.Second,
			AllowedOrigins:                  []string{canonical},
			RejectCredentialQueryOnRedirect: true,
		})
	}
	return &Client{Origin: canonical, AccessToken: accessToken, HTTPClient: httpClient}, nil
}

func (c *Client) VerifyCredentials(ctx context.Context) (VerifiedAccount, error) {
	if c == nil {
		return VerifiedAccount{}, fmt.Errorf("mastodon client is nil")
	}
	return VerifyCredentials(ctx, c.HTTPClient, c.Origin, c.AccessToken)
}

func (c *Client) FetchBookmarksPage(ctx context.Context, cursor string, limit int) (BookmarksPage, error) {
	if c == nil {
		return BookmarksPage{}, fmt.Errorf("mastodon client is nil")
	}
	if limit <= 0 || limit > maxBookmarksPerPage {
		limit = maxBookmarksPerPage
	}
	pageURL := strings.TrimRight(c.Origin, "/") + "/api/v1/bookmarks"
	if strings.TrimSpace(cursor) != "" {
		if err := validateBookmarksCursor(cursor, c.Origin); err != nil {
			return BookmarksPage{}, &StaleCursorError{Reason: err.Error()}
		}
		pageURL = cursor
	} else {
		query := url.Values{}
		query.Set("limit", strconv.Itoa(limit))
		pageURL += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return BookmarksPage{}, fmt.Errorf("create Mastodon bookmarks request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.AccessToken)
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return BookmarksPage{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusBadGateway || response.StatusCode == http.StatusServiceUnavailable || response.StatusCode == http.StatusGatewayTimeout {
		return BookmarksPage{}, &RetryableError{StatusCode: response.StatusCode, RetryAfter: boundedRetryAfter(response.Header.Get("Retry-After"))}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if strings.TrimSpace(cursor) != "" && (response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone) {
			return BookmarksPage{}, &StaleCursorError{StatusCode: response.StatusCode}
		}
		return BookmarksPage{}, fmt.Errorf("mastodon bookmarks: HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxMastodonAPIResponseBytes {
		return BookmarksPage{}, fmt.Errorf("mastodon bookmarks response exceeds %d bytes", maxMastodonAPIResponseBytes)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxMastodonAPIResponseBytes+1))
	if err != nil {
		return BookmarksPage{}, fmt.Errorf("read Mastodon bookmarks response: %w", err)
	}
	if int64(len(body)) > maxMastodonAPIResponseBytes {
		return BookmarksPage{}, fmt.Errorf("mastodon bookmarks response exceeds %d bytes", maxMastodonAPIResponseBytes)
	}
	trimmedBody := bytes.TrimSpace(body)
	if len(trimmedBody) == 0 || trimmedBody[0] != '[' {
		return BookmarksPage{}, fmt.Errorf("decode Mastodon bookmarks response: expected a JSON array")
	}
	var statuses []StatusRecord
	if err := json.Unmarshal(body, &statuses); err != nil {
		return BookmarksPage{}, fmt.Errorf("decode Mastodon bookmarks response: %w", err)
	}
	next, err := ParseNextBookmarksCursor(response.Header.Get("Link"), c.Origin)
	if err != nil {
		return BookmarksPage{}, err
	}
	digest := sha256.Sum256(body)
	return BookmarksPage{URL: pageURL, Statuses: statuses, NextURL: next, PageDigest: hex.EncodeToString(digest[:])}, nil
}

func clientWithTimeout(client *Client, timeout time.Duration) *Client {
	if client == nil || client.HTTPClient == nil || timeout <= 0 || client.HTTPClient.Timeout == timeout {
		return client
	}
	clone := *client
	httpClient := *client.HTTPClient
	httpClient.Timeout = timeout
	clone.HTTPClient = &httpClient
	return &clone
}

func boundedRetryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds <= 0 {
			return 0
		}
		if seconds > int((5 * time.Minute).Seconds()) {
			seconds = int((5 * time.Minute).Seconds())
		}
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(raw); err == nil {
		wait := time.Until(retryAt)
		if wait <= 0 {
			return 0
		}
		if wait > 5*time.Minute {
			wait = 5 * time.Minute
		}
		return wait
	}
	return 0
}
