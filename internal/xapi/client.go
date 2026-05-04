package xapi

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

	"github.com/steipete/sweetcookie"

	"github.com/darron/dbrain/internal/model"
)

func newClient(ctx context.Context, opts Options) (*Client, error) {
	csrfToken := strings.TrimSpace(opts.CT0)
	authToken := strings.TrimSpace(opts.AuthToken)
	if csrfToken == "" {
		ct0, auth, err := resolveCookies(ctx, opts)
		if err != nil {
			return nil, err
		}
		csrfToken = ct0
		authToken = auth
	}
	if csrfToken == "" {
		return nil, fmt.Errorf("could not resolve X ct0 cookie")
	}

	cookieParts := []string{"ct0=" + csrfToken}
	if authToken != "" {
		cookieParts = append(cookieParts, "auth_token="+authToken)
	}

	return &Client{
		httpClient:   &http.Client{Timeout: opts.Timeout},
		csrfToken:    csrfToken,
		cookieHeader: strings.Join(cookieParts, "; "),
		logger:       opts.Logger,
	}, nil
}

func resolveCookies(ctx context.Context, opts Options) (string, string, error) {
	profiles := map[sweetcookie.Browser]string{}
	browsers, err := parseBrowsers(opts.Browser)
	if err != nil {
		return "", "", err
	}
	if opts.Profile != "" {
		if len(browsers) == 0 {
			return "", "", fmt.Errorf("--profile requires --browser so the profile target is unambiguous")
		}
		profiles[browsers[0]] = opts.Profile
	}

	result, err := sweetcookie.Get(ctx, sweetcookie.Options{
		URL:      "https://x.com/",
		Names:    []string{"ct0", "auth_token"},
		Browsers: browsers,
		Profiles: profiles,
		Mode:     sweetcookie.ModeFirst,
		Timeout:  opts.Timeout,
	})
	if err != nil {
		return "", "", fmt.Errorf(
			"load browser cookies: %w\n\nIf macOS shows a Keychain prompt for Go or dbrain, choose Allow or Always Allow\nYou can also bypass browser lookup with --ct0 and --auth-token",
			err,
		)
	}

	var ct0 string
	var authToken string
	for _, cookie := range result.Cookies {
		switch cookie.Name {
		case "ct0":
			if ct0 == "" {
				ct0 = cookie.Value
			}
		case "auth_token":
			if authToken == "" {
				authToken = cookie.Value
			}
		}
	}

	if ct0 == "" {
		msg := "could not find ct0 cookie for x.com in local browser profiles"
		if len(result.Warnings) > 0 {
			msg += ": " + strings.Join(result.Warnings, " | ")
		}
		return "", "", errors.New(msg)
	}

	return ct0, authToken, nil
}

func parseBrowsers(value string) ([]sweetcookie.Browser, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}

	parts := strings.Split(value, ",")
	result := make([]sweetcookie.Browser, 0, len(parts))
	for _, part := range parts {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "chrome":
			result = append(result, sweetcookie.BrowserChrome)
		case "chromium":
			result = append(result, sweetcookie.BrowserChromium)
		case "edge":
			result = append(result, sweetcookie.BrowserEdge)
		case "brave":
			result = append(result, sweetcookie.BrowserBrave)
		case "vivaldi":
			result = append(result, sweetcookie.BrowserVivaldi)
		case "opera":
			result = append(result, sweetcookie.BrowserOpera)
		case "firefox":
			result = append(result, sweetcookie.BrowserFirefox)
		case "safari":
			result = append(result, sweetcookie.BrowserSafari)
		default:
			return nil, fmt.Errorf("unsupported browser %q", strings.TrimSpace(part))
		}
	}
	return result, nil
}

func (c *Client) FetchPost(ctx context.Context, tweetID string) (model.XHydration, error) {
	graphql, err := c.fetchViaGraphQL(ctx, tweetID)
	if err != nil {
		return model.XHydration{}, err
	}

	switch graphql.Status {
	case "ok_graphql", "not_found", "empty":
		return graphql, nil
	case "forbidden", "rate_limited", "error":
		debugLog(c.logger, "falling back to x syndication",
			"tweet_id", tweetID,
			"graphql_status", graphql.Status,
			"graphql_error", graphql.Error,
		)
		return c.fetchViaSyndication(ctx, tweetID)
	default:
		return graphql, nil
	}
}

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
