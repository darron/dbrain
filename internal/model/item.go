package model

import "time"

type Item struct {
	ID                  int64          `json:"id"`
	SourceKey           string         `json:"source_key"`
	SourceType          string         `json:"source_type"`
	ExternalID          string         `json:"external_id"`
	CanonicalURL        string         `json:"canonical_url"`
	Title               string         `json:"title"`
	AuthorHandle        string         `json:"author_handle"`
	AuthorName          string         `json:"author_name"`
	PublishedAt         string         `json:"published_at"`
	SavedAt             string         `json:"saved_at"`
	SyncedAt            string         `json:"synced_at"`
	Language            string         `json:"language"`
	Text                string         `json:"text"`
	ArticleTitle        string         `json:"article_title"`
	ArticleText         string         `json:"article_text"`
	PrimaryCategory     string         `json:"primary_category"`
	PrimaryDomain       string         `json:"primary_domain"`
	LinksJSON           string         `json:"links_json"`
	Categories          string         `json:"categories"`
	Domains             string         `json:"domains"`
	GitHubURLs          string         `json:"github_urls"`
	FolderNames         string         `json:"folder_names"`
	LikeCount           int            `json:"like_count"`
	RepostCount         int            `json:"repost_count"`
	ReplyCount          int            `json:"reply_count"`
	QuoteCount          int            `json:"quote_count"`
	BookmarkCount       int            `json:"bookmark_count"`
	ContentHash         string         `json:"content_hash"`
	NotePath            string         `json:"note_path"`
	RawJSON             string         `json:"raw_json"`
	ImportedAt          time.Time      `json:"imported_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	LastSeenAt          time.Time      `json:"last_seen_at"`
	XPostText           string         `json:"x_post_text"`
	XPostLang           string         `json:"x_post_lang"`
	XPostJSON           string         `json:"x_post_json"`
	XPostFetchedAt      time.Time      `json:"x_post_fetched_at"`
	XPostStatus         string         `json:"x_post_status"`
	XPostError          string         `json:"x_post_error"`
	LinkExtractSyncedAt time.Time      `json:"link_extract_synced_at"`
	Media               []ItemMediaRef `json:"media,omitempty"`
}

type SearchResult struct {
	SourceKey     string `json:"source_key"`
	ExternalID    string `json:"external_id"`
	Title         string `json:"title"`
	AuthorHandle  string `json:"author_handle"`
	AuthorName    string `json:"author_name"`
	CanonicalURL  string `json:"canonical_url"`
	PrimaryDomain string `json:"primary_domain"`
	NotePath      string `json:"note_path"`
	Snippet       string `json:"snippet"`
}

type UpsertStatus string

const (
	UpsertCreated   UpsertStatus = "created"
	UpsertUpdated   UpsertStatus = "updated"
	UpsertUnchanged UpsertStatus = "unchanged"
)

type UpsertResult struct {
	Status   UpsertStatus
	ItemID   int64
	NotePath string
}

type XHydration struct {
	FullText  string    `json:"full_text"`
	Language  string    `json:"language"`
	APIJSON   string    `json:"api_json"`
	FetchedAt time.Time `json:"fetched_at"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
}
