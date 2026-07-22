package xapi

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/vault"
)

func bookmarkRecordToItem(record bookmarkRecord, now time.Time) (model.Item, error) {
	sourceKey, err := bookmarkSourceKey(record.TweetID)
	if err != nil {
		return model.Item{}, err
	}
	publishedAt := normalizeBookmarkTimestamp(record.PostedAt)
	savedAt := normalizeBookmarkTimestamp(record.BookmarkedAt)
	syncedAt := normalizeBookmarkTimestamp(record.SyncedAt)
	if syncedAt == "" {
		syncedAt = now.UTC().Format(time.RFC3339)
	}

	linksJSONBytes, err := json.Marshal(record.Links)
	if err != nil {
		return model.Item{}, fmt.Errorf("marshal links for bookmark %s: %w", record.TweetID, err)
	}
	rawJSONBytes, err := json.Marshal(record)
	if err != nil {
		return model.Item{}, fmt.Errorf("marshal raw bookmark %s: %w", record.TweetID, err)
	}

	primaryDomain, domains, githubURLs := deriveBookmarkDomains(record.Links)
	notePath := vault.NoteRelativePath("x", chooseBookmarkYear(savedAt, publishedAt, syncedAt), record.TweetID)
	title := deriveBookmarkTitle(record)
	item := model.Item{
		SourceKey:     sourceKey,
		SourceType:    "x_bookmark",
		ExternalID:    record.TweetID,
		CanonicalURL:  record.URL,
		Title:         title,
		AuthorHandle:  record.AuthorHandle,
		AuthorName:    record.AuthorName,
		PublishedAt:   publishedAt,
		SavedAt:       savedAt,
		SyncedAt:      syncedAt,
		Language:      record.Language,
		Text:          record.Text,
		PrimaryDomain: primaryDomain,
		LinksJSON:     string(linksJSONBytes),
		Domains:       strings.Join(domains, ","),
		GitHubURLs:    strings.Join(githubURLs, ","),
		LikeCount:     record.LikeCount,
		RepostCount:   record.RepostCount,
		ReplyCount:    record.ReplyCount,
		QuoteCount:    record.QuoteCount,
		BookmarkCount: record.BookmarkCount,
		NotePath:      notePath,
		RawJSON:       string(rawJSONBytes),
		ImportedAt:    now.UTC(),
		UpdatedAt:     now.UTC(),
		LastSeenAt:    now.UTC(),
	}
	item.ContentHash = itemhash.Compute(item)
	return item, nil
}

func bookmarkSourceKey(tweetID string) (string, error) {
	tweetID = strings.TrimSpace(tweetID)
	if tweetID == "" || len(tweetID) > 32 {
		return "", fmt.Errorf("invalid x bookmark identity")
	}
	for _, char := range tweetID {
		if char < '0' || char > '9' {
			return "", fmt.Errorf("invalid x bookmark identity")
		}
	}
	return "x:" + tweetID, nil
}

func deriveBookmarkDomains(links []string) (string, []string, []string) {
	domains := make([]string, 0, len(links))
	githubURLs := make([]string, 0)
	seenDomains := map[string]struct{}{}
	for _, link := range links {
		parsed, err := url.Parse(link)
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if _, ok := seenDomains[host]; !ok {
			domains = append(domains, host)
			seenDomains[host] = struct{}{}
		}
		if host == "github.com" || strings.HasSuffix(host, ".github.com") {
			githubURLs = append(githubURLs, link)
		}
	}
	primary := ""
	if len(domains) > 0 {
		primary = domains[0]
	}
	return primary, domains, githubURLs
}

func deriveBookmarkTitle(record bookmarkRecord) string {
	if value := firstLine(strings.TrimSpace(record.Text)); value != "" {
		return trimBookmarkRunes(value, 96)
	}
	if record.AuthorHandle != "" {
		return "Bookmark from @" + record.AuthorHandle
	}
	return "Bookmark " + record.TweetID
}

func chooseBookmarkYear(values ...string) string {
	for _, value := range values {
		if value == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, value); err == nil {
			return t.Format("2006")
		}
	}
	return "unknown"
}

func normalizeBookmarkTimestamp(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		time.RubyDate,
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return value
}

func firstLine(value string) string {
	if value == "" {
		return ""
	}
	if idx := strings.IndexByte(value, '\n'); idx >= 0 {
		return strings.TrimSpace(value[:idx])
	}
	return strings.TrimSpace(value)
}

func trimBookmarkRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}
