package xapi

import (
	"log/slog"
	"time"
)

const (
	bookmarksQueryID   = "Z9GWmP0kP2dajyckAaDUBw"
	bookmarksOperation = "Bookmarks"
)

var bookmarkTimelineFeatures = map[string]bool{
	"graphql_timeline_v2_bookmark_timeline":                                   true,
	"rweb_tipjar_consumption_enabled":                                         true,
	"responsive_web_graphql_exclude_directive_enabled":                        true,
	"verified_phone_label_enabled":                                            false,
	"creator_subscriptions_tweet_preview_api_enabled":                         true,
	"responsive_web_graphql_timeline_navigation_enabled":                      true,
	"responsive_web_graphql_skip_user_profile_image_extensions_enabled":       false,
	"communities_web_enable_tweet_community_results_fetch":                    true,
	"c9s_tweet_anatomy_moderator_badge_enabled":                               true,
	"articles_preview_enabled":                                                true,
	"responsive_web_edit_tweet_api_enabled":                                   true,
	"tweetypie_unmention_optimization_enabled":                                true,
	"responsive_web_uc_gql_enabled":                                           true,
	"vibe_api_enabled":                                                        true,
	"responsive_web_text_conversations_enabled":                               false,
	"freedom_of_speech_not_reach_fetch_enabled":                               true,
	"longform_notetweets_consumption_enabled":                                 true,
	"longform_notetweets_rich_text_read_enabled":                              true,
	"longform_notetweets_inline_media_enabled":                                true,
	"responsive_web_enhance_cards_enabled":                                    false,
	"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled": true,
	"responsive_web_media_download_video_enabled":                             false,
}

var bookmarkGraphQLBaseURL = "https://x.com/i/api/graphql"

type BookmarkOptions struct {
	Limit          int
	Force          bool
	PageSize       int
	MaxPages       int
	StalePageLimit int
	Browser        string
	Profile        string
	CT0            string
	AuthToken      string
	Timeout        time.Duration
	Logger         *slog.Logger
}

type BookmarkStats struct {
	PagesFetched  int    `json:"pages_fetched"`
	Processed     int    `json:"processed"`
	Created       int    `json:"created"`
	Updated       int    `json:"updated"`
	Unchanged     int    `json:"unchanged"`
	Rendered      int    `json:"rendered"`
	StalePages    int    `json:"stale_pages"`
	StoppedReason string `json:"stopped_reason"`
}

type bookmarkPage struct {
	Records    []bookmarkRecord
	NextCursor string
}

type bookmarkRecord struct {
	ID            string   `json:"id"`
	TweetID       string   `json:"tweet_id"`
	URL           string   `json:"url"`
	Text          string   `json:"text"`
	AuthorHandle  string   `json:"author_handle"`
	AuthorName    string   `json:"author_name"`
	PostedAt      string   `json:"posted_at,omitempty"`
	BookmarkedAt  string   `json:"bookmarked_at,omitempty"`
	SyncedAt      string   `json:"synced_at,omitempty"`
	Language      string   `json:"language,omitempty"`
	LikeCount     int      `json:"like_count,omitempty"`
	RepostCount   int      `json:"repost_count,omitempty"`
	ReplyCount    int      `json:"reply_count,omitempty"`
	QuoteCount    int      `json:"quote_count,omitempty"`
	BookmarkCount int      `json:"bookmark_count,omitempty"`
	Links         []string `json:"links,omitempty"`
	SortIndex     string   `json:"sort_index,omitempty"`
	IngestedVia   string   `json:"ingested_via,omitempty"`
}
