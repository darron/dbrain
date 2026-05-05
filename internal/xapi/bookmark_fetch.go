package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (c *Client) FetchBookmarksPage(ctx context.Context, cursor string, count int) (bookmarkPage, error) {
	endpoint := buildBookmarksURL(cursor, count)
	for attempt := range 4 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return bookmarkPage{}, fmt.Errorf("create bookmarks request: %w", err)
		}
		for key, value := range buildHeaders(c.csrfToken, c.cookieHeader) {
			req.Header.Set(key, value)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if sleepErr := sleepWithContext(ctx, time.Duration(2*(attempt+1))*time.Second); sleepErr != nil {
				return bookmarkPage{}, sleepErr
			}
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return bookmarkPage{}, fmt.Errorf("read bookmarks response: %w", readErr)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			if sleepErr := sleepWithContext(ctx, time.Duration(15*(attempt+1))*time.Second); sleepErr != nil {
				return bookmarkPage{}, sleepErr
			}
			continue
		}
		if resp.StatusCode >= 500 {
			if sleepErr := sleepWithContext(ctx, time.Duration(5*(attempt+1))*time.Second); sleepErr != nil {
				return bookmarkPage{}, sleepErr
			}
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return bookmarkPage{}, fmt.Errorf("x bookmarks denied the request (status=%d)", resp.StatusCode)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return bookmarkPage{}, fmt.Errorf("x bookmarks returned status=%d body=%s", resp.StatusCode, trimForError(string(body), 300))
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			return bookmarkPage{}, fmt.Errorf("invalid bookmarks JSON: %w", err)
		}
		return parseBookmarksResponse(payload, time.Now().UTC()), nil
	}

	return bookmarkPage{}, fmt.Errorf("x bookmarks rate limited after retries")
}

func buildBookmarksURL(cursor string, count int) string {
	variables := map[string]any{
		"count": count,
	}
	if strings.TrimSpace(cursor) != "" {
		variables["cursor"] = cursor
	}
	params := url.Values{}
	vars, _ := json.Marshal(variables)
	features, _ := json.Marshal(bookmarkTimelineFeatures)
	params.Set("variables", string(vars))
	params.Set("features", string(features))
	return bookmarkGraphQLBaseURL + "/" + bookmarksQueryID + "/" + bookmarksOperation + "?" + params.Encode()
}
