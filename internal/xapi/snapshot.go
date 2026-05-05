package xapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/xpost"
)

func buildSnapshotHydration(status string, snapshot *xpost.Snapshot, payload map[string]any, fetchedAt time.Time) (model.XHydration, error) {
	envelope := map[string]any{
		"source":     strings.TrimPrefix(status, "ok_"),
		"fetched_at": fetchedAt.Format(time.RFC3339),
		"snapshot":   xpost.ForStorage(snapshot),
		"raw":        payload,
	}
	apiJSON, err := json.Marshal(envelope)
	if err != nil {
		return model.XHydration{}, fmt.Errorf("marshal hydrated payload for %s: %w", snapshot.ID, err)
	}

	return model.XHydration{
		FullText:  strings.TrimSpace(snapshot.Text),
		Language:  strings.TrimSpace(snapshot.Language),
		APIJSON:   string(apiJSON),
		FetchedAt: fetchedAt,
		Status:    status,
		Error:     "",
	}, nil
}

func extractEntityExpandedURLs(mediaEntities []map[string]any, urlEntitySets ...any) []string {
	seen := map[string]struct{}{}
	links := make([]string, 0, 4)
	for _, urlEntities := range urlEntitySets {
		for _, entity := range listValue(urlEntities) {
			if expanded := stringValue(entity["expanded_url"]); expanded != "" {
				if _, exists := seen[expanded]; !exists {
					seen[expanded] = struct{}{}
					links = append(links, expanded)
				}
			}
		}
	}
	for _, media := range mediaEntities {
		if expanded := stringValue(media["expanded_url"]); expanded != "" {
			if _, exists := seen[expanded]; !exists {
				seen[expanded] = struct{}{}
				links = append(links, expanded)
			}
		}
	}
	return links
}

func selectMediaURL(media map[string]any) string {
	mediaType := stringValue(media["type"])
	if mediaType == "video" || mediaType == "animated_gif" {
		if variant := bestVideoVariantURL(media); variant != "" {
			return variant
		}
	}
	return firstNonEmpty(stringValue(media["media_url_https"]), stringValue(media["media_url"]))
}

func bestVideoVariantURL(media map[string]any) string {
	var bestURL string
	bestBitrate := -1
	for _, variant := range listValue(dig(media, "video_info")["variants"]) {
		url := stringValue(variant["url"])
		contentType := strings.ToLower(stringValue(variant["content_type"]))
		if url == "" {
			continue
		}
		if contentType != "" && !strings.Contains(contentType, "mp4") {
			continue
		}
		bitrate := intValue(variant["bitrate"])
		if bitrate > bestBitrate {
			bestBitrate = bitrate
			bestURL = url
		}
	}
	return bestURL
}

func dig(m map[string]any, keys ...string) map[string]any {
	current := m
	for _, key := range keys {
		next := mapValue(current[key])
		if len(next) == 0 {
			return nil
		}
		current = next
	}
	return current
}

func mapValue(value any) map[string]any {
	if value == nil {
		return nil
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func listValue(value any) []map[string]any {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if m, ok := entry.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func intValue(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}
