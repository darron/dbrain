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

	"github.com/darron/dbrain/internal/model"
)

func (c *Client) fetchViaSyndication(ctx context.Context, tweetID string) (model.XHydration, error) {
	endpoint := syndicationURL + "?id=" + url.QueryEscape(tweetID) + "&token=x"
	for attempt := range 4 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return model.XHydration{}, fmt.Errorf("create syndication request for %s: %w", tweetID, err)
		}
		req.Header.Set("user-agent", chromeUA)

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
			return model.XHydration{}, fmt.Errorf("read syndication response for %s: %w", tweetID, readErr)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			debugLog(c.logger, "x syndication rate limited",
				"tweet_id", tweetID,
				"attempt", attempt+1,
			)
			return model.XHydration{
				FetchedAt: time.Now().UTC(),
				Status:    "rate_limited",
				Error:     "X syndication rate limited",
			}, nil
		}
		if resp.StatusCode >= 500 {
			debugLog(c.logger, "x syndication server error, retrying",
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
			return model.XHydration{FetchedAt: fetchedAt, Status: "not_found", Error: "post not found via syndication"}, nil
		}
		if resp.StatusCode == http.StatusForbidden {
			return model.XHydration{FetchedAt: fetchedAt, Status: "forbidden", Error: "syndication denied the request"}, nil
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return model.XHydration{FetchedAt: fetchedAt, Status: "error", Error: fmt.Sprintf("syndication returned status=%d body=%s", resp.StatusCode, trimForError(string(body), 300))}, nil
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			return model.XHydration{FetchedAt: fetchedAt, Status: "error", Error: fmt.Sprintf("invalid syndication JSON: %v", err)}, nil
		}

		snapshot := parseSyndicationSnapshot(tweetID, payload)
		if snapshot == nil || strings.TrimSpace(snapshot.Text) == "" {
			envelope := map[string]any{
				"source":     "syndication",
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

		return buildSnapshotHydration("ok_syndication", snapshot, payload, fetchedAt)
	}

	return model.XHydration{FetchedAt: time.Now().UTC(), Status: "rate_limited", Error: "syndication rate limited after retries"}, nil
}
