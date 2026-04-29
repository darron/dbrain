package itemhash

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/darron/dbrain/internal/model"
)

func Compute(item model.Item) string {
	likeCount := item.LikeCount
	repostCount := item.RepostCount
	replyCount := item.ReplyCount
	quoteCount := item.QuoteCount
	bookmarkCount := item.BookmarkCount
	if item.SourceType == "x_bookmark" {
		likeCount = 0
		repostCount = 0
		replyCount = 0
		quoteCount = 0
		bookmarkCount = 0
	}

	payload := struct {
		SourceType      string `json:"source_type"`
		SourceKey       string `json:"source_key"`
		CanonicalURL    string `json:"canonical_url"`
		Title           string `json:"title"`
		AuthorHandle    string `json:"author_handle"`
		AuthorName      string `json:"author_name"`
		PublishedAt     string `json:"published_at"`
		SavedAt         string `json:"saved_at"`
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
		SourceType:      item.SourceType,
		SourceKey:       item.SourceKey,
		CanonicalURL:    item.CanonicalURL,
		Title:           item.Title,
		AuthorHandle:    item.AuthorHandle,
		AuthorName:      item.AuthorName,
		PublishedAt:     item.PublishedAt,
		SavedAt:         item.SavedAt,
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
		LikeCount:       likeCount,
		RepostCount:     repostCount,
		ReplyCount:      replyCount,
		QuoteCount:      quoteCount,
		BookmarkCount:   bookmarkCount,
	}
	bytes, _ := json.Marshal(payload)
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}
