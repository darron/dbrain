package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/xpost"
)

func requiresRemoteFetch(items []model.Item, force bool) bool {
	for _, item := range items {
		if shouldFetchItem(item, force) {
			return true
		}
	}
	return false
}

func needsQuotedSnapshotDirectFetch(item model.Item) bool {
	return item.SourceType == "x_quote" &&
		item.XPostStatus == "ok_graphql" &&
		!strings.Contains(item.XPostJSON, `"tweetResult"`)
}

func shouldFetchItem(item model.Item, force bool) bool {
	if force {
		return true
	}
	if needsQuotedSnapshotDirectFetch(item) {
		return true
	}
	switch item.XPostStatus {
	case "", "api_error", "error", "rate_limited":
		return true
	default:
		return false
	}
}

func hydrateItem(ctx context.Context, client *Client, item model.Item, force bool) (model.XHydration, bool, error) {
	if !shouldFetchItem(item, force) {
		return model.XHydration{
			FullText:  item.XPostText,
			Language:  item.XPostLang,
			APIJSON:   item.XPostJSON,
			FetchedAt: item.XPostFetchedAt,
			Status:    item.XPostStatus,
			Error:     item.XPostError,
		}, false, nil
	}
	if client == nil {
		return model.XHydration{}, false, fmt.Errorf("x client is required to hydrate tweet %s", item.ExternalID)
	}
	hydration, err := client.FetchPost(ctx, item.ExternalID)
	return hydration, true, err
}

func normalizeHydration(hydration model.XHydration, fallbackTweetID string) (model.XHydration, *xpost.Snapshot, bool, error) {
	rawJSON := strings.TrimSpace(hydration.APIJSON)
	if rawJSON == "" {
		return hydration, nil, false, nil
	}

	var envelope struct {
		Source    string          `json:"source"`
		FetchedAt string          `json:"fetched_at"`
		Snapshot  *xpost.Snapshot `json:"snapshot"`
		Raw       map[string]any  `json:"raw"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &envelope); err != nil {
		return hydration, nil, false, fmt.Errorf("decode x hydration envelope for %s: %w", fallbackTweetID, err)
	}

	normalized := envelope.Snapshot
	switch strings.TrimSpace(envelope.Source) {
	case "graphql":
		if rebuilt := parseGraphQLSnapshot(fallbackTweetID, envelope.Raw); rebuilt != nil {
			normalized = rebuilt
		}
	case "syndication":
		if rebuilt := parseSyndicationSnapshot(fallbackTweetID, envelope.Raw); rebuilt != nil {
			normalized = rebuilt
		}
	}
	if normalized == nil {
		return hydration, nil, false, nil
	}

	hydration.FullText = strings.TrimSpace(normalized.Text)
	hydration.Language = strings.TrimSpace(normalized.Language)
	if reflect.DeepEqual(envelope.Snapshot, xpost.ForStorage(normalized)) {
		return hydration, normalized, false, nil
	}

	envelope.Snapshot = xpost.ForStorage(normalized)
	rewritten, err := json.Marshal(envelope)
	if err != nil {
		return hydration, nil, false, fmt.Errorf("marshal normalized x hydration for %s: %w", fallbackTweetID, err)
	}
	hydration.APIJSON = string(rewritten)
	return hydration, normalized, true, nil
}
