package xapi

import (
	"github.com/darron/dbrain/internal/xpost"
)

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
