package xapi

import (
	"strings"
	"time"
)

func parseBookmarksResponse(payload map[string]any, fetchedAt time.Time) bookmarkPage {
	instructions := listValue(dig(dig(payload, "data"), "bookmark_timeline_v2", "timeline")["instructions"])
	entries := make([]map[string]any, 0)
	for _, instruction := range instructions {
		if stringValue(instruction["type"]) != "TimelineAddEntries" {
			continue
		}
		entries = append(entries, listValue(instruction["entries"])...)
	}

	page := bookmarkPage{Records: make([]bookmarkRecord, 0, len(entries))}
	for _, entry := range entries {
		entryID := stringValue(entry["entryId"])
		if strings.HasPrefix(entryID, "cursor-bottom") {
			page.NextCursor = stringValue(dig(entry, "content")["value"])
			continue
		}

		result := dig(dig(dig(entry, "content"), "itemContent"), "tweet_results", "result")
		if len(result) == 0 {
			continue
		}
		record := parseBookmarkRecord(result, stringValue(entry["sortIndex"]), fetchedAt)
		if record == nil {
			continue
		}
		page.Records = append(page.Records, *record)
	}

	return page
}

func parseBookmarkRecord(result map[string]any, sortIndex string, fetchedAt time.Time) *bookmarkRecord {
	tweet := result
	if nested := mapValue(result["tweet"]); len(nested) > 0 {
		tweet = nested
	}
	legacy := mapValue(tweet["legacy"])
	if len(legacy) == 0 {
		return nil
	}

	tweetID := firstNonEmpty(stringValue(legacy["id_str"]), stringValue(tweet["rest_id"]))
	if tweetID == "" {
		return nil
	}

	noteText := stringValue(dig(dig(tweet, "note_tweet", "note_tweet_results"), "result")["text"])
	text := firstNonEmpty(noteText, stringValue(legacy["full_text"]), stringValue(legacy["text"]))
	handle := firstNonEmpty(
		stringValue(dig(dig(dig(tweet, "core"), "user_results"), "result", "core")["screen_name"]),
		stringValue(dig(dig(dig(tweet, "core"), "user_results"), "result", "legacy")["screen_name"]),
	)
	name := firstNonEmpty(
		stringValue(dig(dig(dig(tweet, "core"), "user_results"), "result", "core")["name"]),
		stringValue(dig(dig(dig(tweet, "core"), "user_results"), "result", "legacy")["name"]),
	)

	seenLinks := map[string]struct{}{}
	links := make([]string, 0)
	for _, urlEntities := range tweetURLEntitySets(tweet, legacy) {
		for _, entity := range listValue(urlEntities) {
			shortURL := stringValue(entity["url"])
			displayURL := stringValue(entity["display_url"])
			if shortURL != "" && displayURL != "" {
				text = strings.ReplaceAll(text, shortURL, displayURL)
			}
			if expanded := stringValue(entity["expanded_url"]); expanded != "" {
				if _, exists := seenLinks[expanded]; exists {
					continue
				}
				seenLinks[expanded] = struct{}{}
				links = append(links, expanded)
			}
		}
	}

	return &bookmarkRecord{
		ID:            tweetID,
		TweetID:       tweetID,
		URL:           "https://x.com/" + firstNonEmpty(handle, "_") + "/status/" + tweetID,
		Text:          strings.TrimSpace(text),
		AuthorHandle:  handle,
		AuthorName:    name,
		PostedAt:      stringValue(legacy["created_at"]),
		BookmarkedAt:  "",
		SyncedAt:      fetchedAt.UTC().Format(time.RFC3339),
		Language:      stringValue(legacy["lang"]),
		LikeCount:     intValue(legacy["favorite_count"]),
		RepostCount:   intValue(legacy["retweet_count"]),
		ReplyCount:    intValue(legacy["reply_count"]),
		QuoteCount:    intValue(legacy["quote_count"]),
		BookmarkCount: intValue(legacy["bookmark_count"]),
		Links:         links,
		SortIndex:     sortIndex,
		IngestedVia:   "graphql",
	}
}
