package itemhash

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"dbrain/internal/model"
)

func Compute(item model.Item) string {
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
