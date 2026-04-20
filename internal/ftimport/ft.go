package ftimport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
	"dbrain/internal/vault"
)

const driverName = "sqlite"

type Options struct {
	SourcePath string
	Limit      int
}

type Stats struct {
	Processed int  `json:"processed"`
	Created   int  `json:"created"`
	Updated   int  `json:"updated"`
	Unchanged int  `json:"unchanged"`
	Rendered  int  `json:"rendered"`
	Skipped   int  `json:"skipped"`
	FTSBacked bool `json:"fts_backed"`
}

type rawBookmark struct {
	ID              string `json:"id"`
	TweetID         string `json:"tweet_id"`
	URL             string `json:"url"`
	Text            string `json:"text"`
	AuthorHandle    string `json:"author_handle"`
	AuthorName      string `json:"author_name"`
	PostedAt        string `json:"posted_at"`
	BookmarkedAt    string `json:"bookmarked_at"`
	SyncedAt        string `json:"synced_at"`
	Language        string `json:"language"`
	LikeCount       int    `json:"like_count"`
	RepostCount     int    `json:"repost_count"`
	ReplyCount      int    `json:"reply_count"`
	QuoteCount      int    `json:"quote_count"`
	BookmarkCount   int    `json:"bookmark_count"`
	LinksJSON       string `json:"links_json"`
	PrimaryCategory string `json:"primary_category"`
	PrimaryDomain   string `json:"primary_domain"`
	ArticleTitle    string `json:"article_title"`
	ArticleText     string `json:"article_text"`
	Categories      string `json:"categories"`
	Domains         string `json:"domains"`
	GitHubURLs      string `json:"github_urls"`
	FolderNames     string `json:"folder_names"`
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	ftdb, err := sql.Open(driverName, opts.SourcePath)
	if err != nil {
		return Stats{}, fmt.Errorf("open ft db %s: %w", opts.SourcePath, err)
	}
	defer func() {
		_ = ftdb.Close()
	}()

	query := `
		SELECT
			id,
			tweet_id,
			url,
			COALESCE(text, ''),
			COALESCE(author_handle, ''),
			COALESCE(author_name, ''),
			COALESCE(posted_at, ''),
			COALESCE(bookmarked_at, ''),
			COALESCE(synced_at, ''),
			COALESCE(language, ''),
			COALESCE(like_count, 0),
			COALESCE(repost_count, 0),
			COALESCE(reply_count, 0),
			COALESCE(quote_count, 0),
			COALESCE(bookmark_count, 0),
			COALESCE(links_json, '[]'),
			COALESCE(primary_category, ''),
			COALESCE(primary_domain, ''),
			COALESCE(article_title, ''),
			COALESCE(article_text, ''),
			COALESCE(categories, ''),
			COALESCE(domains, ''),
			COALESCE(github_urls, ''),
			COALESCE(folder_names, '')
		FROM bookmarks
		ORDER BY COALESCE(synced_at, posted_at, tweet_id) ASC`
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	rows, err := ftdb.QueryContext(ctx, query)
	if err != nil {
		return Stats{}, fmt.Errorf("query ft bookmarks: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	stats := Stats{}
	stats.FTSBacked = st.HasFTS()
	for rows.Next() {
		var raw rawBookmark
		if err := rows.Scan(
			&raw.ID,
			&raw.TweetID,
			&raw.URL,
			&raw.Text,
			&raw.AuthorHandle,
			&raw.AuthorName,
			&raw.PostedAt,
			&raw.BookmarkedAt,
			&raw.SyncedAt,
			&raw.Language,
			&raw.LikeCount,
			&raw.RepostCount,
			&raw.ReplyCount,
			&raw.QuoteCount,
			&raw.BookmarkCount,
			&raw.LinksJSON,
			&raw.PrimaryCategory,
			&raw.PrimaryDomain,
			&raw.ArticleTitle,
			&raw.ArticleText,
			&raw.Categories,
			&raw.Domains,
			&raw.GitHubURLs,
			&raw.FolderNames,
		); err != nil {
			return stats, fmt.Errorf("scan ft bookmark: %w", err)
		}

		item, err := toItem(raw, cfg)
		if err != nil {
			return stats, err
		}

		result, err := st.UpsertItem(ctx, item)
		if err != nil {
			return stats, err
		}

		stats.Processed++
		switch result.Status {
		case model.UpsertCreated:
			stats.Created++
		case model.UpsertUpdated:
			stats.Updated++
		case model.UpsertUnchanged:
			stats.Unchanged++
		}

		shouldRender := result.Status != model.UpsertUnchanged
		if !shouldRender {
			if _, err := vault.StatNote(cfg, item.NotePath); err != nil {
				shouldRender = true
			}
		}

		if shouldRender {
			if err := vault.WriteItem(cfg, item); err != nil {
				return stats, fmt.Errorf("render note %s: %w", item.SourceKey, err)
			}
			stats.Rendered++
		}
	}

	if err := rows.Err(); err != nil {
		return stats, fmt.Errorf("iterate ft bookmarks: %w", err)
	}

	return stats, nil
}

func toItem(raw rawBookmark, _ config.Config) (model.Item, error) {
	now := time.Now().UTC()
	publishedAt := normalizeTimestamp(raw.PostedAt)
	savedAt := normalizeTimestamp(raw.BookmarkedAt)
	syncedAt := normalizeTimestamp(raw.SyncedAt)
	if syncedAt == "" {
		syncedAt = now.Format(time.RFC3339)
	}

	title := deriveTitle(raw)
	sourceKey := "x:" + raw.TweetID
	notePath := vault.NoteRelativePath("x", chooseYear(publishedAt, savedAt, syncedAt), raw.TweetID)

	rawJSONBytes, err := json.Marshal(raw)
	if err != nil {
		return model.Item{}, fmt.Errorf("marshal raw bookmark %s: %w", raw.TweetID, err)
	}

	item := model.Item{
		SourceKey:       sourceKey,
		SourceType:      "x_bookmark",
		ExternalID:      raw.TweetID,
		CanonicalURL:    raw.URL,
		Title:           title,
		AuthorHandle:    raw.AuthorHandle,
		AuthorName:      raw.AuthorName,
		PublishedAt:     publishedAt,
		SavedAt:         savedAt,
		SyncedAt:        syncedAt,
		Language:        raw.Language,
		Text:            raw.Text,
		ArticleTitle:    raw.ArticleTitle,
		ArticleText:     raw.ArticleText,
		PrimaryCategory: raw.PrimaryCategory,
		PrimaryDomain:   raw.PrimaryDomain,
		LinksJSON:       normalizeJSONArray(raw.LinksJSON),
		Categories:      raw.Categories,
		Domains:         raw.Domains,
		GitHubURLs:      raw.GitHubURLs,
		FolderNames:     raw.FolderNames,
		LikeCount:       raw.LikeCount,
		RepostCount:     raw.RepostCount,
		ReplyCount:      raw.ReplyCount,
		QuoteCount:      raw.QuoteCount,
		BookmarkCount:   raw.BookmarkCount,
		NotePath:        notePath,
		RawJSON:         string(rawJSONBytes),
		ImportedAt:      now,
		UpdatedAt:       now,
		LastSeenAt:      now,
	}
	item.ContentHash = computeHash(item)

	return item, nil
}

func deriveTitle(raw rawBookmark) string {
	if value := strings.TrimSpace(raw.ArticleTitle); value != "" {
		return value
	}
	if value := firstLine(strings.TrimSpace(raw.Text)); value != "" {
		return trimRunes(value, 96)
	}
	if raw.AuthorHandle != "" {
		return "Bookmark from @" + raw.AuthorHandle
	}
	return "Bookmark " + raw.TweetID
}

func chooseYear(values ...string) string {
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

func normalizeTimestamp(value string) string {
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

func normalizeJSONArray(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "[]"
	}
	return value
}

func computeHash(item model.Item) string {
	payload := struct {
		SourceKey       string `json:"source_key"`
		CanonicalURL    string `json:"canonical_url"`
		Title           string `json:"title"`
		AuthorHandle    string `json:"author_handle"`
		AuthorName      string `json:"author_name"`
		PublishedAt     string `json:"published_at"`
		SavedAt         string `json:"saved_at"`
		SyncedAt        string `json:"synced_at"`
		Language        string `json:"language"`
		Text            string `json:"text"`
		ArticleTitle    string `json:"article_title"`
		ArticleText     string `json:"article_text"`
		PrimaryCategory string `json:"primary_category"`
		PrimaryDomain   string `json:"primary_domain"`
		LinksJSON       string `json:"links_json"`
		Categories      string `json:"categories"`
		Domains         string `json:"domains"`
		GitHubURLs      string `json:"github_urls"`
		FolderNames     string `json:"folder_names"`
		LikeCount       int    `json:"like_count"`
		RepostCount     int    `json:"repost_count"`
		ReplyCount      int    `json:"reply_count"`
		QuoteCount      int    `json:"quote_count"`
		BookmarkCount   int    `json:"bookmark_count"`
	}{
		SourceKey:       item.SourceKey,
		CanonicalURL:    item.CanonicalURL,
		Title:           item.Title,
		AuthorHandle:    item.AuthorHandle,
		AuthorName:      item.AuthorName,
		PublishedAt:     item.PublishedAt,
		SavedAt:         item.SavedAt,
		SyncedAt:        item.SyncedAt,
		Language:        item.Language,
		Text:            item.Text,
		ArticleTitle:    item.ArticleTitle,
		ArticleText:     item.ArticleText,
		PrimaryCategory: item.PrimaryCategory,
		PrimaryDomain:   item.PrimaryDomain,
		LinksJSON:       item.LinksJSON,
		Categories:      item.Categories,
		Domains:         item.Domains,
		GitHubURLs:      item.GitHubURLs,
		FolderNames:     item.FolderNames,
		LikeCount:       item.LikeCount,
		RepostCount:     item.RepostCount,
		ReplyCount:      item.ReplyCount,
		QuoteCount:      item.QuoteCount,
		BookmarkCount:   item.BookmarkCount,
	}
	bytes, _ := json.Marshal(payload)
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
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

func trimRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}
