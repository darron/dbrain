package xapi

import (
	"github.com/darron/dbrain/internal/xpost"
)

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

func tweetURLEntitySets(tweet, legacy map[string]any) []any {
	sets := []any{dig(legacy, "entities")["urls"]}
	noteResult := dig(tweet, "note_tweet", "note_tweet_results", "result")
	if len(noteResult) > 0 {
		sets = append(sets, dig(noteResult, "entity_set")["urls"], dig(noteResult, "entities")["urls"])
	}
	return sets
}
