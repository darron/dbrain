package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
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

func RunBookmarks(ctx context.Context, cfg config.Config, st *store.Store, opts BookmarkOptions) (BookmarkStats, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.PageSize <= 0 {
		opts.PageSize = 20
	}
	if opts.PageSize > 100 {
		opts.PageSize = 100
	}
	if opts.StalePageLimit <= 0 {
		opts.StalePageLimit = 2
	}

	client, err := newClient(ctx, Options{
		Browser:   opts.Browser,
		Profile:   opts.Profile,
		CT0:       opts.CT0,
		AuthToken: opts.AuthToken,
		Timeout:   opts.Timeout,
		Logger:    opts.Logger,
	})
	if err != nil {
		return BookmarkStats{}, err
	}

	stats := BookmarkStats{}
	cursor := ""
	stalePages := 0
	now := time.Now().UTC()

pageLoop:
	for {
		if opts.MaxPages > 0 && stats.PagesFetched >= opts.MaxPages {
			stats.StoppedReason = "max pages reached"
			break
		}
		if opts.Limit > 0 && stats.Processed >= opts.Limit {
			stats.StoppedReason = "limit reached"
			break
		}

		page, err := client.FetchBookmarksPage(ctx, cursor, opts.PageSize)
		if err != nil {
			return stats, err
		}
		stats.PagesFetched++

		if len(page.Records) == 0 {
			stats.StoppedReason = "end of bookmarks"
			break
		}

		pageCreated := 0
		for _, record := range page.Records {
			if opts.Limit > 0 && stats.Processed >= opts.Limit {
				stats.StoppedReason = "limit reached"
				break pageLoop
			}

			item, err := bookmarkRecordToItem(record, now)
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
				pageCreated++
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
					return stats, fmt.Errorf("render imported bookmark note %s: %w", item.SourceKey, err)
				}
				stats.Rendered++
			}
		}

		if !opts.Force {
			if pageCreated == 0 {
				stalePages++
				stats.StalePages = stalePages
				if stalePages >= opts.StalePageLimit {
					stats.StoppedReason = "overlap with existing bookmarks"
					break
				}
			} else {
				stalePages = 0
				stats.StalePages = 0
			}
		}

		if page.NextCursor == "" {
			stats.StoppedReason = "end of bookmarks"
			break
		}
		cursor = page.NextCursor
	}

	if stats.StoppedReason == "" {
		stats.StoppedReason = "completed"
	}
	return stats, nil
}

func (c *Client) FetchBookmarksPage(ctx context.Context, cursor string, count int) (bookmarkPage, error) {
	endpoint := buildBookmarksURL(cursor, count)
	for attempt := range 4 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return bookmarkPage{}, fmt.Errorf("create bookmarks request: %w", err)
		}
		for key, value := range buildHeaders(c.csrfToken, c.cookieHeader) {
			req.Header.Set(key, value)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if sleepErr := sleepWithContext(ctx, time.Duration(2*(attempt+1))*time.Second); sleepErr != nil {
				return bookmarkPage{}, sleepErr
			}
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return bookmarkPage{}, fmt.Errorf("read bookmarks response: %w", readErr)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			if sleepErr := sleepWithContext(ctx, time.Duration(15*(attempt+1))*time.Second); sleepErr != nil {
				return bookmarkPage{}, sleepErr
			}
			continue
		}
		if resp.StatusCode >= 500 {
			if sleepErr := sleepWithContext(ctx, time.Duration(5*(attempt+1))*time.Second); sleepErr != nil {
				return bookmarkPage{}, sleepErr
			}
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return bookmarkPage{}, fmt.Errorf("x bookmarks denied the request (status=%d)", resp.StatusCode)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return bookmarkPage{}, fmt.Errorf("x bookmarks returned status=%d body=%s", resp.StatusCode, trimForError(string(body), 300))
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			return bookmarkPage{}, fmt.Errorf("invalid bookmarks JSON: %w", err)
		}
		return parseBookmarksResponse(payload, time.Now().UTC()), nil
	}

	return bookmarkPage{}, fmt.Errorf("x bookmarks rate limited after retries")
}

func buildBookmarksURL(cursor string, count int) string {
	variables := map[string]any{
		"count": count,
	}
	if strings.TrimSpace(cursor) != "" {
		variables["cursor"] = cursor
	}
	params := url.Values{}
	vars, _ := json.Marshal(variables)
	features, _ := json.Marshal(bookmarkTimelineFeatures)
	params.Set("variables", string(vars))
	params.Set("features", string(features))
	return bookmarkGraphQLBaseURL + "/" + bookmarksQueryID + "/" + bookmarksOperation + "?" + params.Encode()
}

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

func bookmarkRecordToItem(record bookmarkRecord, now time.Time) (model.Item, error) {
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
		SourceKey:     "x:" + record.TweetID,
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
