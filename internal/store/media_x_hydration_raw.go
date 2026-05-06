package store

import "strings"

func extractXHydrationMediaFromRaw(raw map[string]any) []xHydrationMedia {
	if len(raw) == 0 {
		return nil
	}
	if media := extractGraphQLMediaFromRaw(raw); len(media) > 0 {
		return media
	}
	if media := extractSyndicationMediaFromRaw(raw); len(media) > 0 {
		return media
	}
	return nil
}

func extractGraphQLMediaFromRaw(payload map[string]any) []xHydrationMedia {
	result := digMap(payload, "data", "tweetResult", "result")
	if len(result) == 0 {
		return nil
	}
	tweet := result
	if nested := mapAny(result["tweet"]); len(nested) > 0 {
		tweet = nested
	}
	legacy := mapAny(tweet["legacy"])
	if len(legacy) == 0 {
		return nil
	}
	mediaEntities := listAny(digMap(legacy, "extended_entities")["media"])
	if len(mediaEntities) == 0 {
		mediaEntities = listAny(digMap(legacy, "entities")["media"])
	}
	return buildXHydrationMediaFromRaw(mediaEntities)
}

func extractSyndicationMediaFromRaw(payload map[string]any) []xHydrationMedia {
	return buildXHydrationMediaFromRaw(listAny(payload["mediaDetails"]))
}

func buildXHydrationMediaFromRaw(media []map[string]any) []xHydrationMedia {
	if len(media) == 0 {
		return nil
	}
	out := make([]xHydrationMedia, 0, len(media))
	for _, entry := range media {
		out = append(out, xHydrationMedia{
			Type:        stringAny(entry["type"]),
			URL:         selectRawMediaURL(entry),
			ExpandedURL: stringAny(entry["expanded_url"]),
			Width:       intAny(digMap(entry, "original_info")["width"]),
			Height:      intAny(digMap(entry, "original_info")["height"]),
		})
	}
	return out
}

func selectRawMediaURL(media map[string]any) string {
	mediaType := stringAny(media["type"])
	if mediaType == "video" || mediaType == "animated_gif" {
		if variant := bestRawVideoVariantURL(media); variant != "" {
			return variant
		}
	}
	return firstNonEmpty(stringAny(media["media_url_https"]), stringAny(media["media_url"]))
}

func bestRawVideoVariantURL(media map[string]any) string {
	var bestURL string
	bestBitrate := -1
	for _, variant := range listAny(digMap(media, "video_info")["variants"]) {
		url := stringAny(variant["url"])
		contentType := strings.ToLower(stringAny(variant["content_type"]))
		if url == "" {
			continue
		}
		if contentType != "" && !strings.Contains(contentType, "mp4") {
			continue
		}
		bitrate := intAny(variant["bitrate"])
		if bitrate > bestBitrate {
			bestBitrate = bitrate
			bestURL = url
		}
	}
	return bestURL
}

func looksPlayableVideoURL(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "video.twimg.com/") ||
		strings.HasSuffix(value, ".mp4") ||
		strings.Contains(value, ".mp4?")
}

func sameMediaKind(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func digMap(m map[string]any, keys ...string) map[string]any {
	current := m
	for _, key := range keys {
		next := mapAny(current[key])
		if len(next) == 0 {
			return nil
		}
		current = next
	}
	return current
}

func mapAny(value any) map[string]any {
	if value == nil {
		return nil
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func listAny(value any) []map[string]any {
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

func stringAny(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func intAny(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}
