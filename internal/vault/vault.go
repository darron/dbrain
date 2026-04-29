package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/xpost"
)

const defaultMediaProxyBaseURL = "http://127.0.0.1:8742"

type RenderOptions struct {
	MediaProxyBaseURL string
}

func NoteRelativePath(sourceKind, year, externalID string) string {
	if year == "" {
		year = "unknown"
	}
	if externalID == "" {
		externalID = "unknown"
	}
	return filepath.ToSlash(filepath.Join("items", sourceKind, year, externalID+".md"))
}

func StatNote(cfg config.Config, relPath string) (os.FileInfo, error) {
	return os.Stat(filepath.Join(cfg.VaultDir, relPath))
}

func WriteItem(cfg config.Config, item model.Item) error {
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create note dir: %w", err)
	}

	body, err := RenderItemWithOptions(item, renderOptionsForConfig(cfg))
	if err != nil {
		return err
	}

	existing, err := os.ReadFile(fullPath)
	if err == nil && string(existing) == body {
		return nil
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write note: %w", err)
	}
	return nil
}

func RenderItem(item model.Item) (string, error) {
	return RenderItemWithOptions(item, RenderOptions{})
}

func RenderItemWithOptions(item model.Item, opts RenderOptions) (string, error) {
	links, err := decodeStringArray(item.LinksJSON)
	if err != nil {
		return "", fmt.Errorf("decode links for %s: %w", item.SourceKey, err)
	}
	snapshot, _, _ := xpost.DecodeSnapshot(item.XPostJSON)

	sourceFamily := itemSourceFamily(item.SourceType)
	tags := []string{"source/" + sourceFamily}
	if item.PrimaryCategory != "" {
		tags = append(tags, "category/"+item.PrimaryCategory)
	}
	if item.PrimaryDomain != "" {
		tags = append(tags, "domain/"+item.PrimaryDomain)
	}

	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "brain_source_key", item.SourceKey)
	writeYAMLScalar(&b, "source_type", item.SourceType)
	writeYAMLScalar(&b, "external_id", item.ExternalID)
	writeYAMLScalar(&b, "canonical_url", item.CanonicalURL)
	writeYAMLScalar(&b, "title", item.Title)
	writeYAMLScalar(&b, "author_handle", item.AuthorHandle)
	writeYAMLScalar(&b, "author_name", item.AuthorName)
	writeYAMLScalar(&b, "published_at", item.PublishedAt)
	writeYAMLScalar(&b, "saved_at", item.SavedAt)
	writeYAMLScalar(&b, "synced_at", item.SyncedAt)
	writeYAMLScalar(&b, "primary_category", item.PrimaryCategory)
	writeYAMLScalar(&b, "primary_domain", item.PrimaryDomain)
	if isXItem(item) {
		writeYAMLScalar(&b, "x_post_status", item.XPostStatus)
		writeYAMLScalar(&b, "x_post_fetched_at", formatTime(item.XPostFetchedAt))
		writeYAMLScalar(&b, "x_post_lang", item.XPostLang)
		writeYAMLScalar(&b, "summary_status", item.SummaryStatus)
		writeYAMLScalar(&b, "summary_model", item.SummaryModel)
		writeYAMLScalar(&b, "summary_tool", item.SummaryTool)
		writeYAMLScalar(&b, "ocr_status", item.OCRStatus)
		writeYAMLScalar(&b, "ocr_model", item.OCRModel)
		writeYAMLScalar(&b, "ocr_tool", item.OCRTool)
	}
	writeYAMLArray(&b, "tags", tags)
	writeYAMLArray(&b, "links", links)
	b.WriteString("---\n\n")

	b.WriteString("# ")
	b.WriteString(item.Title)
	b.WriteString("\n\n")

	b.WriteString("## Source\n\n")
	b.WriteString("- Source key: `")
	b.WriteString(item.SourceKey)
	b.WriteString("`\n")
	b.WriteString("- URL: ")
	b.WriteString(item.CanonicalURL)
	b.WriteString("\n")
	if item.AuthorHandle != "" || item.AuthorName != "" {
		b.WriteString("- Author: ")
		if item.AuthorName != "" {
			b.WriteString(item.AuthorName)
			if item.AuthorHandle != "" {
				b.WriteString(" ")
			}
		}
		if item.AuthorHandle != "" {
			b.WriteString("(@")
			b.WriteString(item.AuthorHandle)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if item.PublishedAt != "" {
		b.WriteString("- Published: ")
		b.WriteString(item.PublishedAt)
		b.WriteString("\n")
	}
	if item.SavedAt != "" {
		b.WriteString("- ")
		b.WriteString(itemSavedLabel(item))
		b.WriteString(": ")
		b.WriteString(item.SavedAt)
		b.WriteString("\n")
	}
	if item.PrimaryCategory != "" {
		b.WriteString("- ")
		b.WriteString(itemCategoryLabel(item))
		b.WriteString(": `")
		b.WriteString(item.PrimaryCategory)
		b.WriteString("`\n")
	}
	if item.PrimaryDomain != "" {
		b.WriteString("- Domain: `")
		b.WriteString(item.PrimaryDomain)
		b.WriteString("`\n")
	}
	if isXItem(item) && item.XPostStatus != "" {
		b.WriteString("- X API status: `")
		b.WriteString(item.XPostStatus)
		b.WriteString("`\n")
	}
	if isXItem(item) && !item.XPostFetchedAt.IsZero() {
		b.WriteString("- X API fetched: ")
		b.WriteString(formatTime(item.XPostFetchedAt))
		b.WriteString("\n")
	}
	if isXItem(item) && item.XPostLang != "" {
		b.WriteString("- X API language: `")
		b.WriteString(item.XPostLang)
		b.WriteString("`\n")
	}

	b.WriteString("\n## Summary\n\n")
	switch {
	case strings.TrimSpace(item.SummaryText) != "":
		b.WriteString(strings.TrimSpace(item.SummaryText))
		b.WriteString("\n")
	case isXItem(item) && item.XPostText != "":
		b.WriteString("Canonical X post text is cached below. No item summary stored yet.\n")
	case item.ArticleText != "":
		b.WriteString("Imported source text is available below. No item summary stored yet.\n")
	case isGitHubItem(item):
		b.WriteString("GitHub star imported. Canonical repo and homepage enrichment are stored on linked source notes.\n")
	case isYouTubeItem(item):
		b.WriteString("YouTube signal imported. Canonical video extraction and summarization are stored on linked source notes.\n")
	default:
		b.WriteString("Bookmark imported. No expanded source text is stored yet.\n")
	}

	if text := strings.TrimSpace(item.XPostText); text != "" {
		b.WriteString("\n## Canonical X Post\n\n")
		b.WriteString(text)
		b.WriteString("\n")
	}
	if isXItem(item) && snapshot != nil && snapshot.QuotedPost != nil {
		writeQuotedPostSection(&b, snapshot.QuotedPost)
	}

	if text := strings.TrimSpace(item.Text); text != "" && !sameNormalizedText(text, item.XPostText) {
		b.WriteString("\n## ")
		b.WriteString(itemTextHeading(item))
		b.WriteString("\n\n")
		b.WriteString(text)
		b.WriteString("\n")
	}

	if item.ArticleTitle != "" || item.ArticleText != "" {
		b.WriteString("\n## Cached Source Extract\n\n")
		if item.ArticleTitle != "" {
			b.WriteString("### Title\n\n")
			b.WriteString(item.ArticleTitle)
			b.WriteString("\n\n")
		}
		if item.ArticleText != "" {
			b.WriteString("### Text\n\n")
			b.WriteString(strings.TrimSpace(item.ArticleText))
			b.WriteString("\n")
		}
	}

	if text := strings.TrimSpace(item.OCRText); text != "" {
		b.WriteString("\n## OCR / Vision Extract\n\n")
		b.WriteString(text)
		b.WriteString("\n")
	}

	if len(item.Media) > 0 {
		b.WriteString("\n## Media\n\n")
		for _, media := range item.Media {
			b.WriteString("### ")
			b.WriteString(mediaHeading(media))
			b.WriteString("\n\n")
			proxyURL := archivedMediaProxyURL(opts, media)
			switch {
			case media.DownloadStatus == "downloaded" && strings.TrimSpace(media.LocalPath) != "" && media.LocalPrunedAt.IsZero():
				b.WriteString("![[")
				b.WriteString(media.LocalPath)
				b.WriteString("]]\n\n")
			case strings.TrimSpace(media.ArchiveURL) != "" && strings.TrimSpace(media.ArchiveStatus) == "archived":
				if media.MediaType == "photo" {
					writeArchivedImageEmbed(&b, media.ArchiveURL)
				} else if mediaIsVideoLike(media) {
					writeArchivedVideoEmbed(&b, media.ArchiveURL)
				} else {
					b.WriteString("[Archived media](")
					b.WriteString(media.ArchiveURL)
					b.WriteString(")\n\n")
				}
			case proxyURL != "" && strings.TrimSpace(media.ArchiveStatus) == "archived":
				if media.MediaType == "photo" {
					writeArchivedImageEmbed(&b, proxyURL)
				} else if mediaIsVideoLike(media) {
					writeArchivedVideoEmbed(&b, proxyURL)
				} else {
					b.WriteString("[Archived media stream](")
					b.WriteString(proxyURL)
					b.WriteString(")\n\n")
				}
			case strings.TrimSpace(media.ArchiveStatus) == "archived":
				b.WriteString("Archived remotely. No anonymous media URL is configured.\n\n")
			}
			b.WriteString("- Status: `")
			b.WriteString(firstNonEmptyString(media.DownloadStatus, "pending"))
			b.WriteString("`\n")
			if media.RemoteURL != "" {
				b.WriteString("- Remote URL: ")
				b.WriteString(media.RemoteURL)
				b.WriteString("\n")
			}
			if media.ExpandedURL != "" {
				b.WriteString("- Post Media URL: ")
				b.WriteString(media.ExpandedURL)
				b.WriteString("\n")
			}
			if media.Width > 0 && media.Height > 0 {
				b.WriteString("- Dimensions: `")
				_, _ = fmt.Fprintf(&b, "%dx%d", media.Width, media.Height)
				b.WriteString("`\n")
			}
			if media.LocalPath != "" {
				b.WriteString("- Local Path: `")
				b.WriteString(media.LocalPath)
				b.WriteString("`\n")
			}
			if media.ArchiveStatus != "" {
				b.WriteString("- Archive Status: `")
				b.WriteString(media.ArchiveStatus)
				b.WriteString("`\n")
			}
			if media.ArchiveProvider != "" {
				b.WriteString("- Archive Provider: `")
				b.WriteString(media.ArchiveProvider)
				b.WriteString("`\n")
			}
			if media.ArchiveBucket != "" {
				b.WriteString("- Archive Bucket: `")
				b.WriteString(media.ArchiveBucket)
				b.WriteString("`\n")
			}
			if media.ArchiveKey != "" {
				b.WriteString("- Archive Key: `")
				b.WriteString(media.ArchiveKey)
				b.WriteString("`\n")
			}
			if media.ArchiveURL != "" {
				b.WriteString("- Archive URL: ")
				b.WriteString(media.ArchiveURL)
				b.WriteString("\n")
			}
			if !media.LocalPrunedAt.IsZero() {
				b.WriteString("- Local Pruned: ")
				b.WriteString(formatTime(media.LocalPrunedAt))
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	if len(links) > 0 {
		b.WriteString("\n## Outbound Links\n\n")
		for _, link := range links {
			b.WriteString("- ")
			b.WriteString(link)
			b.WriteString("\n")
		}
	}

	metadata := itemMetadataLines(item)
	if len(metadata) > 0 {
		b.WriteString("\n## Metadata\n\n")
		for _, line := range metadata {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	return b.String(), nil
}

func renderOptionsForConfig(cfg config.Config) RenderOptions {
	baseURL := strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_MEDIA_PROXY_BASE_URL", "DBRAIN_WEB_BASE_URL"))
	switch strings.ToLower(baseURL) {
	case "off", "none", "disabled":
		return RenderOptions{}
	}
	if baseURL == "" {
		baseURL = defaultMediaProxyBaseURL
	}
	return RenderOptions{MediaProxyBaseURL: baseURL}
}

func writeQuotedPostSection(b *strings.Builder, snapshot *xpost.Snapshot) {
	if snapshot == nil {
		return
	}

	b.WriteString("\n## Quoted X Post\n\n")
	if notePath := xpost.NotePath(snapshot); notePath != "" {
		b.WriteString("- Linked item: [[")
		b.WriteString(notePath)
		b.WriteString("]]\n")
	}
	if url := strings.TrimSpace(snapshot.URL); url != "" {
		b.WriteString("- URL: ")
		b.WriteString(url)
		b.WriteString("\n")
	}
	if snapshot.AuthorHandle != "" || snapshot.AuthorName != "" {
		b.WriteString("- Author: ")
		if snapshot.AuthorName != "" {
			b.WriteString(snapshot.AuthorName)
			if snapshot.AuthorHandle != "" {
				b.WriteString(" ")
			}
		}
		if snapshot.AuthorHandle != "" {
			b.WriteString("(@")
			b.WriteString(snapshot.AuthorHandle)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if postedAt := strings.TrimSpace(snapshot.PostedAt); postedAt != "" {
		b.WriteString("- Published: ")
		b.WriteString(postedAt)
		b.WriteString("\n")
	}
	if len(snapshot.Links) > 0 {
		b.WriteString("- Links:\n")
		for _, link := range snapshot.Links {
			link = strings.TrimSpace(link)
			if link == "" {
				continue
			}
			b.WriteString("  - ")
			b.WriteString(link)
			b.WriteString("\n")
		}
	}
	if text := strings.TrimSpace(snapshot.Text); text != "" {
		b.WriteString("\n")
		b.WriteString(text)
		b.WriteString("\n")
	}
}

func archivedMediaProxyURL(opts RenderOptions, media model.ItemMediaRef) string {
	baseURL := strings.TrimSpace(opts.MediaProxyBaseURL)
	if baseURL == "" || media.MediaAssetID <= 0 {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/media/asset/" + strconv.FormatInt(media.MediaAssetID, 10)
}

func mediaIsVideoLike(media model.ItemMediaRef) bool {
	switch strings.TrimSpace(media.MediaType) {
	case "video", "animated_gif":
		return true
	default:
		return false
	}
}

func writeArchivedVideoEmbed(b *strings.Builder, url string) {
	b.WriteString("<video controls preload=\"metadata\" src=\"")
	b.WriteString(url)
	b.WriteString("\"></video>\n\n")
	b.WriteString("[Open archived media](")
	b.WriteString(url)
	b.WriteString(")\n\n")
}

func writeArchivedImageEmbed(b *strings.Builder, url string) {
	b.WriteString("![](")
	b.WriteString(url)
	b.WriteString(")\n\n")
	b.WriteString("[Open archived media](")
	b.WriteString(url)
	b.WriteString(")\n\n")
}

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

func isYouTubeItem(item model.Item) bool {
	return itemSourceFamily(item.SourceType) == "youtube"
}

func isGitHubItem(item model.Item) bool {
	return itemSourceFamily(item.SourceType) == "github"
}

func itemSavedLabel(item model.Item) string {
	if isXItem(item) {
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

func mediaHeading(media model.ItemMediaRef) string {
	label := strings.ReplaceAll(strings.TrimSpace(media.MediaType), "_", " ")
	if label == "" {
		label = "media"
	}
	if len(label) == 1 {
		label = strings.ToUpper(label)
	} else {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	return label + fmt.Sprintf(" %d", media.Ordinal+1)
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
	if isXItem(item) {
		return "Bookmark Text"
	}
	if isYouTubeItem(item) {
		return "Imported Description"
	}
	if isGitHubItem(item) {
		return "Repository Description"
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

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func sameNormalizedText(a, b string) bool {
	return normalizeBodyText(a) == normalizeBodyText(b)
}

func normalizeBodyText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.TrimSpace(value)
}

func decodeStringArray(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}

	var raw []interface{}
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return nil, err
	}

	result := make([]string, 0, len(raw))
	for _, entry := range raw {
		if asString, ok := entry.(string); ok && strings.TrimSpace(asString) != "" {
			result = append(result, asString)
		}
	}
	return result, nil
}

func writeYAMLScalar(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(yamlQuote(value))
	b.WriteString("\n")
}

func writeYAMLArray(b *strings.Builder, key string, values []string) {
	if len(values) == 0 {
		b.WriteString(key)
		b.WriteString(": []\n")
		return
	}
	b.WriteString(key)
	b.WriteString(":\n")
	for _, value := range values {
		b.WriteString("  - ")
		b.WriteString(yamlQuote(value))
		b.WriteString("\n")
	}
}

func yamlQuote(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}
