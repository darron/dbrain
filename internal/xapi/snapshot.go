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

func parseGraphQLSnapshot(tweetID string, payload map[string]any) *xpost.Snapshot {
	result := dig(payload, "data", "tweetResult", "result")
	return parseGraphQLSnapshotNode(result, tweetID)
}

func parseGraphQLSnapshotNode(result map[string]any, fallbackID string) *xpost.Snapshot {
	if len(result) == 0 {
		return nil
	}
	tweet := result
	if nested := mapValue(result["tweet"]); len(nested) > 0 {
		tweet = nested
	}
	legacy := mapValue(tweet["legacy"])
	if len(legacy) == 0 {
		return nil
	}

	noteText := stringValue(dig(tweet, "note_tweet", "note_tweet_results", "result")["text"])
	text := firstNonEmpty(noteText, stringValue(legacy["full_text"]), stringValue(legacy["text"]))
	handle := firstNonEmpty(
		stringValue(dig(tweet, "core", "user_results", "result", "core")["screen_name"]),
		stringValue(dig(tweet, "core", "user_results", "result", "legacy")["screen_name"]),
	)
	name := firstNonEmpty(
		stringValue(dig(tweet, "core", "user_results", "result", "core")["name"]),
		stringValue(dig(tweet, "core", "user_results", "result", "legacy")["name"]),
	)
	profileImage := firstNonEmpty(
		stringValue(dig(tweet, "core", "user_results", "result", "avatar")["image_url"]),
		stringValue(dig(tweet, "core", "user_results", "result", "legacy")["profile_image_url_https"]),
	)
	resolvedID := firstNonEmpty(stringValue(legacy["id_str"]), stringValue(tweet["rest_id"]), fallbackID)

	mediaEntities := listValue(dig(legacy, "extended_entities")["media"])
	if len(mediaEntities) == 0 {
		mediaEntities = listValue(dig(legacy, "entities")["media"])
	}

	snapshot := &xpost.Snapshot{
		ID:                    resolvedID,
		Text:                  text,
		Language:              stringValue(legacy["lang"]),
		AuthorHandle:          handle,
		AuthorName:            name,
		AuthorProfileImageURL: profileImage,
		PostedAt:              xpost.NormalizeTimestamp(stringValue(legacy["created_at"])),
		URL:                   "https://x.com/" + firstNonEmpty(handle, "_") + "/status/" + resolvedID,
		Links:                 extractEntityExpandedURLs(mediaEntities, tweetURLEntitySets(tweet, legacy)...),
		Raw:                   tweet,
	}
	for _, media := range mediaEntities {
		mediaURL := selectMediaURL(media)
		snapshot.Media = append(snapshot.Media, mediaURL)
		snapshot.MediaObjects = append(snapshot.MediaObjects, xpost.MediaObject{
			Type:        stringValue(media["type"]),
			URL:         mediaURL,
			ExpandedURL: stringValue(media["expanded_url"]),
			Width:       intValue(dig(media, "original_info")["width"]),
			Height:      intValue(dig(media, "original_info")["height"]),
		})
	}
	if quoted := parseGraphQLSnapshotNode(dig(tweet, "quoted_status_result", "result"), stringValue(legacy["quoted_status_id_str"])); quoted != nil {
		snapshot.QuotedPost = quoted
	} else if quotedID := stringValue(legacy["quoted_status_id_str"]); quotedID != "" {
		snapshot.QuotedPost = &xpost.Snapshot{
			ID:  quotedID,
			URL: firstNonEmpty(stringValue(dig(legacy, "quoted_status_permalink")["expanded"]), "https://x.com/i/web/status/"+quotedID),
			Raw: map[string]any{
				"legacy": map[string]any{
					"quoted_status_id_str": quotedID,
				},
			},
		}
	}
	return snapshot
}

func parseSyndicationSnapshot(tweetID string, payload map[string]any) *xpost.Snapshot {
	return parseSyndicationSnapshotNode(payload, tweetID)
}

func parseSyndicationSnapshotNode(payload map[string]any, fallbackID string) *xpost.Snapshot {
	text := stringValue(payload["text"])
	if text == "" {
		return nil
	}
	handle := stringValue(dig(payload, "user")["screen_name"])
	name := stringValue(dig(payload, "user")["name"])
	profileImage := stringValue(dig(payload, "user")["profile_image_url_https"])
	resolvedID := firstNonEmpty(stringValue(payload["id_str"]), fallbackID)
	mediaDetails := listValue(payload["mediaDetails"])

	snapshot := &xpost.Snapshot{
		ID:                    resolvedID,
		Text:                  text,
		Language:              "",
		AuthorHandle:          handle,
		AuthorName:            name,
		AuthorProfileImageURL: profileImage,
		PostedAt:              xpost.NormalizeTimestamp(stringValue(payload["created_at"])),
		URL:                   "https://x.com/" + firstNonEmpty(handle, "_") + "/status/" + resolvedID,
		Links:                 extractEntityExpandedURLs(mediaDetails, dig(payload, "entities")["urls"]),
		Raw:                   payload,
	}
	for _, media := range mediaDetails {
		mediaURL := selectMediaURL(media)
		snapshot.Media = append(snapshot.Media, mediaURL)
		snapshot.MediaObjects = append(snapshot.MediaObjects, xpost.MediaObject{
			Type:   stringValue(media["type"]),
			URL:    mediaURL,
			Width:  intValue(dig(media, "original_info")["width"]),
			Height: intValue(dig(media, "original_info")["height"]),
		})
	}
	quotedPayload := mapValue(payload["quoted_tweet"])
	if len(quotedPayload) == 0 {
		quotedPayload = mapValue(mapValue(payload["parent"])["quoted_tweet"])
	}
	if quoted := parseSyndicationSnapshotNode(quotedPayload, ""); quoted != nil {
		snapshot.QuotedPost = quoted
	}
	return snapshot
}

func tweetURLEntitySets(tweet, legacy map[string]any) []any {
	sets := []any{dig(legacy, "entities")["urls"]}
	noteResult := dig(tweet, "note_tweet", "note_tweet_results", "result")
	if len(noteResult) > 0 {
		sets = append(sets, dig(noteResult, "entity_set")["urls"], dig(noteResult, "entities")["urls"])
	}
	return sets
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
