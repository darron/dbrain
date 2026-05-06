package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (c *Client) fetchViaGraphQL(ctx context.Context, tweetID string) (model.XHydration, error) {
	endpoint := buildTweetResultByRestIDURL(tweetID)
	for attempt := range 4 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return model.XHydration{}, fmt.Errorf("create graphql request for %s: %w", tweetID, err)
		}
		for key, value := range buildHeaders(c.csrfToken, c.cookieHeader) {
			req.Header.Set(key, value)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if sleepErr := sleepWithContext(ctx, time.Duration(2*(attempt+1))*time.Second); sleepErr != nil {
				return model.XHydration{}, sleepErr
			}
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return model.XHydration{}, fmt.Errorf("read graphql response for %s: %w", tweetID, readErr)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			debugLog(c.logger, "x graphql rate limited",
				"tweet_id", tweetID,
				"attempt", attempt+1,
			)
			return model.XHydration{
				FetchedAt: time.Now().UTC(),
				Status:    "rate_limited",
				Error:     "X GraphQL rate limited; falling back to syndication",
			}, nil
		}
		if resp.StatusCode >= 500 {
			debugLog(c.logger, "x graphql server error, retrying",
				"tweet_id", tweetID,
				"attempt", attempt+1,
				"status_code", resp.StatusCode,
			)
			if sleepErr := sleepWithContext(ctx, time.Duration(5*(attempt+1))*time.Second); sleepErr != nil {
				return model.XHydration{}, sleepErr
			}
			continue
		}

		fetchedAt := time.Now().UTC()
		if resp.StatusCode == http.StatusNotFound {
			return model.XHydration{FetchedAt: fetchedAt, Status: "not_found", Error: "post not found via X GraphQL"}, nil
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return model.XHydration{FetchedAt: fetchedAt, Status: "forbidden", Error: fmt.Sprintf("X GraphQL denied the request (status=%d)", resp.StatusCode)}, nil
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return model.XHydration{FetchedAt: fetchedAt, Status: "error", Error: fmt.Sprintf("X GraphQL returned status=%d body=%s", resp.StatusCode, trimForError(string(body), 300))}, nil
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			return model.XHydration{FetchedAt: fetchedAt, Status: "error", Error: fmt.Sprintf("invalid X GraphQL JSON: %v", err)}, nil
		}

		result := dig(payload, "data", "tweetResult", "result")
		typename, _ := result["__typename"].(string)
		if len(result) == 0 || typename == "TweetTombstone" || typename == "TweetUnavailable" {
			return model.XHydration{FetchedAt: fetchedAt, Status: "not_found", Error: "post unavailable via X GraphQL"}, nil
		}

		snapshot := parseGraphQLSnapshot(tweetID, payload)
		if snapshot == nil || strings.TrimSpace(snapshot.Text) == "" {
			envelope := map[string]any{
				"source":     "graphql",
				"fetched_at": fetchedAt.Format(time.RFC3339),
				"raw":        payload,
			}
			apiJSON, _ := json.Marshal(envelope)
			return model.XHydration{
				Language:  "",
				APIJSON:   string(apiJSON),
				FetchedAt: fetchedAt,
				Status:    "empty",
				Error:     "tweet exists but has no text content",
			}, nil
		}

		return buildSnapshotHydration("ok_graphql", snapshot, payload, fetchedAt)
	}

	return model.XHydration{FetchedAt: time.Now().UTC(), Status: "rate_limited", Error: "X GraphQL rate limited after retries"}, nil
}
