package bskyapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const bookmarkEndpoint = "/xrpc/app.bsky.bookmark.getBookmarks"

var errBookmarkUnauthorized = errors.New("bluesky bookmarks request was unauthorized")

type bookmarkHTTPError struct {
	status int
}

func (e bookmarkHTTPError) Error() string {
	return fmt.Sprintf("Bluesky bookmarks request returned HTTP %d", e.status)
}

type bookmarkClient struct {
	baseURL    *url.URL
	httpClient *http.Client
	accessJWT  string
	refreshJWT string
}

type bookmarkPage struct {
	Bookmarks []bookmarkView `json:"bookmarks"`
	Cursor    string         `json:"cursor"`
}

type bookmarkView struct {
	CreatedAt string          `json:"createdAt"`
	Subject   bookmarkSubject `json:"subject"`
	Item      json.RawMessage `json:"item"`
}

type bookmarkSubject struct {
	URI string `json:"uri"`
	CID string `json:"cid"`
}

func newBookmarkClient(credentials sessionCredentials, httpClient *http.Client) (*bookmarkClient, error) {
	base := strings.TrimSpace(credentials.PDSURL)
	if base == "" {
		base = strings.TrimSpace(credentials.Service)
	}
	if base == "" {
		return nil, errors.New("bluesky session has no API service URL")
	}
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || parsed.Hostname() == "" {
		return nil, errors.New("invalid Bluesky API service URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid Bluesky API service URL")
	}
	if parsed.Scheme != "https" && !isLocalHTTPURL(parsed) {
		return nil, errors.New("bluesky API service URL must use HTTPS")
	}
	if strings.TrimSpace(credentials.AccessJWT) == "" && strings.TrimSpace(credentials.RefreshJWT) == "" {
		return nil, errors.New("bluesky session has no usable access or refresh token")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &bookmarkClient{
		baseURL:    parsed,
		httpClient: httpClient,
		accessJWT:  strings.TrimSpace(credentials.AccessJWT),
		refreshJWT: strings.TrimSpace(credentials.RefreshJWT),
	}, nil
}

func (c *bookmarkClient) fetchBookmarksPage(ctx context.Context, cursor string, limit int) (bookmarkPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}

	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + bookmarkEndpoint
	query := endpoint.Query()
	query.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	endpoint.RawQuery = query.Encode()

	refreshed := false
	if strings.TrimSpace(c.accessJWT) == "" && c.refreshJWT != "" {
		if err := c.refreshSession(ctx); err != nil {
			return bookmarkPage{}, err
		}
		refreshed = true
	}
	for attempt := 0; attempt < 3; attempt++ {
		page, err := c.fetchBookmarksPageOnce(ctx, endpoint)
		if !errors.Is(err, errBookmarkUnauthorized) {
			if err == nil {
				return page, nil
			}
			var statusErr bookmarkHTTPError
			if !errors.As(err, &statusErr) || (!isTransientStatus(statusErr.status) || attempt == 2) {
				return bookmarkPage{}, err
			}
			if err := waitForRetry(ctx, attempt); err != nil {
				return bookmarkPage{}, err
			}
			continue
		}

		if refreshed || c.refreshJWT == "" {
			return bookmarkPage{}, err
		}
		if err := c.refreshSession(ctx); err != nil {
			return bookmarkPage{}, err
		}
		refreshed = true
	}
	return bookmarkPage{}, errors.New("bluesky bookmarks request retry limit reached")
}

func (c *bookmarkClient) fetchBookmarksPageOnce(ctx context.Context, endpoint url.URL) (bookmarkPage, error) {
	if strings.TrimSpace(c.accessJWT) == "" {
		return bookmarkPage{}, errors.New("bluesky session has no access token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return bookmarkPage{}, fmt.Errorf("build Bluesky bookmarks request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.accessJWT)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return bookmarkPage{}, fmt.Errorf("request Bluesky bookmarks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return bookmarkPage{}, errBookmarkUnauthorized
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var apiError struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &apiError) == nil && strings.EqualFold(apiError.Error, "ExpiredToken") {
			return bookmarkPage{}, errBookmarkUnauthorized
		}
		return bookmarkPage{}, bookmarkHTTPError{status: resp.StatusCode}
	}

	var page bookmarkPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return bookmarkPage{}, fmt.Errorf("decode Bluesky bookmarks response: %w", err)
	}
	return page, nil
}

func (c *bookmarkClient) refreshSession(ctx context.Context) error {
	if strings.TrimSpace(c.refreshJWT) == "" {
		return errors.New("bluesky session has no refresh token")
	}
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/xrpc/com.atproto.server.refreshSession"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("build Bluesky session refresh request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.refreshJWT)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("refresh Bluesky session: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("refresh Bluesky session returned HTTP %d", resp.StatusCode)
	}
	var session struct {
		AccessJWT  string `json:"accessJwt"`
		RefreshJWT string `json:"refreshJwt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return fmt.Errorf("decode refreshed Bluesky session: %w", err)
	}
	if strings.TrimSpace(session.AccessJWT) == "" {
		return errors.New("refreshed Bluesky session has no access token")
	}
	c.accessJWT = strings.TrimSpace(session.AccessJWT)
	if strings.TrimSpace(session.RefreshJWT) != "" {
		c.refreshJWT = strings.TrimSpace(session.RefreshJWT)
	}
	return nil
}

func isTransientStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func waitForRetry(ctx context.Context, attempt int) error {
	delay := 50 * time.Millisecond
	if attempt > 0 {
		delay = 100 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isLocalHTTPURL(value *url.URL) bool {
	if value.Scheme != "http" {
		return false
	}
	switch strings.ToLower(value.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
