package xapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

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
