package feedimport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	ext "github.com/mmcdole/gofeed/extensions"

	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

func buildFeedEntry(feed store.Feed, parsed *gofeed.Feed, item *gofeed.Item, observedAt time.Time) (store.FeedEntry, bool) {
	link := strings.TrimSpace(item.Link)
	normalizedLink := ""
	var sourceCandidate *model.SourceCandidate
	if candidate, ok := linkextract.NormalizeCandidate(link); ok {
		normalizedLink = candidate.NormalizedURL
		sourceCandidate = &candidate
	}
	author := feedItemAuthor(item)
	publishedAt := feedItemTime(item.Published, item.PublishedParsed)
	entryUpdatedAt := feedItemTime(item.Updated, item.UpdatedParsed)
	contentMarkdown := extractMarkdown(item.Extensions, item.Custom)
	contentText := textFromHTML(item.Content)
	summaryText := textFromHTML(item.Description)
	identity := feedItemIdentity(item, contentMarkdown, contentText, summaryText)
	if identity == "" {
		return store.FeedEntry{}, false
	}
	entryKey := "feed-entry:" + shortHash(feed.FeedKey+"|"+identity)
	enclosuresJSON := mustJSON(item.Enclosures, "[]")
	extensionsJSON := mustJSON(item.Extensions, "{}")
	rawJSON := mustJSON(item, "{}")
	contentHash := feedEntryContentHash(item, contentMarkdown)

	nowText := observedAt.UTC().Format(time.RFC3339)
	year := yearFor(firstNonEmpty(publishedAt, entryUpdatedAt, nowText))
	title := strings.TrimSpace(item.Title)
	canonicalURL := firstNonEmpty(normalizedLink, link, feed.SiteURL, feed.NormalizedURL)
	text := feedItemText(parsed, item, contentMarkdown, contentText)
	linksJSON := feedItemLinks(item, canonicalURL)
	noteID := slugify(firstNonEmpty(title, identity)) + "-" + strings.TrimPrefix(entryKey, "feed-entry:")
	modelItem := model.Item{
		SourceKey:    entryKey,
		SourceType:   "feed_entry",
		ExternalID:   identity,
		CanonicalURL: canonicalURL,
		Title:        title,
		AuthorName:   author,
		PublishedAt:  publishedAt,
		SavedAt:      nowText,
		SyncedAt:     nowText,
		Language:     strings.TrimSpace(parsed.Language),
		Text:         text,
		LinksJSON:    linksJSON,
		ContentHash:  "",
		NotePath:     vault.NoteRelativePath("feed", year, noteID),
		RawJSON:      rawJSON,
		UpdatedAt:    observedAt.UTC(),
		LastSeenAt:   observedAt.UTC(),
		UserTags:     feed.UserTags,
	}
	modelItem.ContentHash = itemhash.Compute(modelItem)

	return store.FeedEntry{
		FeedID:          feed.ID,
		EntryKey:        entryKey,
		IdentityKey:     identity,
		GUID:            strings.TrimSpace(item.GUID),
		GUIDIsPermalink: strings.EqualFold(strings.TrimSpace(item.GUID), link) && link != "",
		Link:            link,
		NormalizedLink:  normalizedLink,
		Title:           title,
		Author:          author,
		PublishedAt:     publishedAt,
		EntryUpdatedAt:  entryUpdatedAt,
		SummaryHTML:     strings.TrimSpace(item.Description),
		SummaryText:     summaryText,
		ContentHTML:     strings.TrimSpace(item.Content),
		ContentMarkdown: contentMarkdown,
		ContentText:     contentText,
		EnclosuresJSON:  enclosuresJSON,
		ExtensionsJSON:  extensionsJSON,
		RawJSON:         rawJSON,
		ContentHash:     contentHash,
		Item:            modelItem,
		SourceCandidate: sourceCandidate,
		ObservedAt:      observedAt.UTC(),
	}, true
}

func feedItemIdentity(item *gofeed.Item, contentMarkdown string, contentText string, summaryText string) string {
	if guid := strings.TrimSpace(item.GUID); guid != "" {
		return "guid:" + guid
	}
	if link := strings.TrimSpace(item.Link); link != "" {
		if candidate, ok := linkextract.NormalizeCandidate(link); ok {
			return "link:" + candidate.NormalizedURL
		}
		return "link:" + link
	}
	fallbackText := normalizedFallbackText(item, contentMarkdown, contentText, summaryText)
	if fallbackText == "" {
		return ""
	}
	fallback := strings.Join([]string{
		strings.TrimSpace(item.Title),
		strings.TrimSpace(item.Published),
		truncateUTF8Bytes(fallbackText, 2048),
	}, "\x00")
	return "fallback:" + shortHash(fallback)
}

func normalizedFallbackText(item *gofeed.Item, contentMarkdown string, contentText string, summaryText string) string {
	if value := strings.Join(strings.Fields(contentMarkdown), " "); value != "" {
		return value
	}
	if value := strings.Join(strings.Fields(contentText), " "); value != "" {
		return value
	}
	if value := strings.Join(strings.Fields(summaryText), " "); value != "" {
		return value
	}
	if value := strings.Join(strings.Fields(item.Title), " "); value != "" {
		return value
	}
	metadata := stableEnclosures(item.Enclosures)
	if len(metadata) == 0 {
		return ""
	}
	return mustJSON(metadata, "")
}

func truncateUTF8Bytes(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	var out strings.Builder
	for _, r := range value {
		next := string(r)
		if out.Len()+len(next) > maxBytes {
			break
		}
		out.WriteString(next)
	}
	return out.String()
}

func feedEntryContentHash(item *gofeed.Item, markdown string) string {
	payload := map[string]any{
		"title":            strings.TrimSpace(item.Title),
		"description":      strings.TrimSpace(item.Description),
		"content":          strings.TrimSpace(item.Content),
		"content_markdown": markdown,
		"link":             strings.TrimSpace(item.Link),
		"links":            sortedStrings(item.Links),
		"published":        strings.TrimSpace(item.Published),
		"updated":          strings.TrimSpace(item.Updated),
		"author":           feedItemAuthor(item),
		"categories":       sortedStrings(item.Categories),
		"enclosures":       stableEnclosures(item.Enclosures),
		"extensions":       whitelistedExtensions(item.Extensions),
	}
	bytes, _ := json.Marshal(payload)
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}

func whitelistedExtensions(input ext.Extensions) map[string]map[string][]ext.Extension {
	out := map[string]map[string][]ext.Extension{}
	for namespace, names := range ExtensionContentHashWhitelist {
		values, ok := input[namespace]
		if !ok {
			continue
		}
		for name := range names {
			matches := values[name]
			if len(matches) == 0 {
				continue
			}
			if out[namespace] == nil {
				out[namespace] = map[string][]ext.Extension{}
			}
			out[namespace][name] = matches
		}
	}
	return out
}

func extractMarkdown(extensions ext.Extensions, custom map[string]string) string {
	for _, key := range []string{"markdown", "content_markdown", "content:markdown"} {
		if value := strings.TrimSpace(custom[key]); value != "" {
			return value
		}
	}
	for namespace, names := range ExtensionContentHashWhitelist {
		if namespace != "markdown" && namespace != "content" {
			continue
		}
		for name := range names {
			for _, match := range extensions[namespace][name] {
				if strings.Contains(strings.ToLower(match.Attrs["type"]), "markdown") {
					if value := strings.TrimSpace(match.Value); value != "" {
						return value
					}
				}
			}
		}
	}
	return ""
}

func feedItemAuthor(item *gofeed.Item) string {
	if len(item.Authors) > 0 && item.Authors[0] != nil {
		return strings.TrimSpace(item.Authors[0].Name)
	}
	if item.Author != nil {
		return strings.TrimSpace(item.Author.Name)
	}
	return ""
}

func feedItemTime(raw string, parsed *time.Time) string {
	if parsed != nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return strings.TrimSpace(raw)
}

func yearFor(value string) string {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC().Format("2006")
	}
	if len(value) >= 4 {
		prefix := value[:4]
		if prefix >= "1900" && prefix <= "2999" {
			return prefix
		}
	}
	return time.Now().UTC().Format("2006")
}

func textFromHTML(value string) string {
	value = htmlTagPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	return strings.Join(strings.Fields(value), " ")
}

func feedItemText(feed *gofeed.Feed, item *gofeed.Item, markdown string, text string) string {
	parts := []string{}
	if feed.Title != "" {
		parts = append(parts, "Feed: "+strings.TrimSpace(feed.Title))
	}
	if feed.Link != "" {
		parts = append(parts, "Feed URL: "+strings.TrimSpace(feed.Link))
	}
	if item.Title != "" {
		parts = append(parts, strings.TrimSpace(item.Title))
	}
	if markdown != "" {
		parts = append(parts, markdown)
	} else if text != "" {
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}

func feedItemLinks(item *gofeed.Item, canonicalURL string) string {
	links := []string{}
	for _, value := range append([]string{canonicalURL, item.Link}, item.Links...) {
		value = strings.TrimSpace(value)
		if value != "" {
			links = append(links, value)
		}
	}
	for _, enclosure := range item.Enclosures {
		if enclosure != nil && strings.TrimSpace(enclosure.URL) != "" {
			links = append(links, strings.TrimSpace(enclosure.URL))
		}
	}
	return mustJSON(sortedStrings(uniqueStrings(links)), "[]")
}

func stableEnclosures(enclosures []*gofeed.Enclosure) []map[string]string {
	out := make([]map[string]string, 0, len(enclosures))
	for _, enclosure := range enclosures {
		if enclosure == nil {
			continue
		}
		out = append(out, map[string]string{
			"url":    strings.TrimSpace(enclosure.URL),
			"type":   strings.TrimSpace(enclosure.Type),
			"length": strings.TrimSpace(enclosure.Length),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["url"] < out[j]["url"]
	})
	return out
}

func sortedStrings(values []string) []string {
	out := uniqueStrings(values)
	sort.Strings(out)
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func mustJSON(value any, fallback string) string {
	bytes, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return string(bytes)
}
