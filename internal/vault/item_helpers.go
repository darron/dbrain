package vault

import (
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func itemSourceFamily(sourceType string) string {
	sourceType = strings.TrimSpace(sourceType)
	switch {
	case sourceType == "":
		return "item"
	case sourceType == "x_bookmark" || strings.HasPrefix(sourceType, "x_"):
		return "x"
	case strings.HasPrefix(sourceType, "youtube_"):
		return "youtube"
	default:
		if idx := strings.IndexByte(sourceType, '_'); idx > 0 {
			return sourceType[:idx]
		}
		return sourceType
	}
}

func isXItem(item model.Item) bool {
	return itemSourceFamily(item.SourceType) == "x"
}

func isMastodonItem(item model.Item) bool {
	return itemSourceFamily(item.SourceType) == "mastodon"
}

func isYouTubeItem(item model.Item) bool {
	return itemSourceFamily(item.SourceType) == "youtube"
}

func isGitHubItem(item model.Item) bool {
	return itemSourceFamily(item.SourceType) == "github"
}

func isAppleNoteItem(item model.Item) bool {
	return item.SourceType == "apple_note"
}

func itemSavedLabel(item model.Item) string {
	if isXItem(item) || isMastodonItem(item) {
		return "Bookmarked"
	}
	if isGitHubItem(item) {
		return "Starred"
	}
	return "Saved"
}

func itemCategoryLabel(item model.Item) string {
	if isYouTubeItem(item) || isGitHubItem(item) {
		return "Signal"
	}
	return "Category"
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func itemTextHeading(item model.Item) string {
	if isXItem(item) || isMastodonItem(item) {
		return "Bookmark Text"
	}
	if isYouTubeItem(item) {
		return "Imported Description"
	}
	if isGitHubItem(item) {
		return "Repository Description"
	}
	if isAppleNoteItem(item) {
		return "Apple Note Body"
	}
	return "Imported Text"
}

func itemMetadataLines(item model.Item) []string {
	lines := make([]string, 0, 8)
	if item.LikeCount > 0 {
		lines = append(lines, fmt.Sprintf("Likes: %d", item.LikeCount))
	}
	if item.RepostCount > 0 {
		lines = append(lines, fmt.Sprintf("Reposts: %d", item.RepostCount))
	}
	if item.ReplyCount > 0 {
		lines = append(lines, fmt.Sprintf("Replies: %d", item.ReplyCount))
	}
	if item.QuoteCount > 0 {
		lines = append(lines, fmt.Sprintf("Quotes: %d", item.QuoteCount))
	}
	if item.BookmarkCount > 0 {
		lines = append(lines, fmt.Sprintf("Bookmarks: %d", item.BookmarkCount))
	}
	if item.FolderNames != "" {
		lines = append(lines, "Folder names: "+item.FolderNames)
	}
	if item.GitHubURLs != "" {
		lines = append(lines, "GitHub URLs: "+item.GitHubURLs)
	}
	if isXItem(item) && item.XPostError != "" {
		lines = append(lines, "X API error: "+item.XPostError)
	}
	return lines
}
