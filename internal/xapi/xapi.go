package xapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/steipete/sweetcookie"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/mediadownload"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
	"github.com/darron/dbrain/internal/xpost"
)

const (
	chromeUA                     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
	xPublicBearer                = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"
	tweetResultByRestIDQueryID   = "fHLDP3qFEjnTqhWBVvsREg"
	tweetResultByRestIDOperation = "TweetResultByRestId"
	syndicationURL               = "https://cdn.syndication.twimg.com/tweet-result"
)

var tweetResultFeatures = map[string]bool{
	"responsive_web_graphql_exclude_directive_enabled":                        true,
	"verified_phone_label_enabled":                                            true,
	"creator_subscriptions_tweet_preview_api_enabled":                         true,
	"responsive_web_graphql_timeline_navigation_enabled":                      true,
	"responsive_web_graphql_skip_user_profile_image_extensions_enabled":       true,
	"premium_content_api_read_enabled":                                        true,
	"communities_web_enable_tweet_community_results_fetch":                    true,
	"c9s_tweet_anatomy_moderator_badge_enabled":                               true,
	"responsive_web_grok_analyze_button_fetch_trends_enabled":                 true,
	"responsive_web_grok_analyze_post_followups_enabled":                      true,
	"responsive_web_jetfuel_frame":                                            true,
	"responsive_web_grok_share_attachment_enabled":                            true,
	"responsive_web_grok_annotations_enabled":                                 true,
	"articles_preview_enabled":                                                true,
	"responsive_web_edit_tweet_api_enabled":                                   true,
	"graphql_is_translatable_rweb_tweet_is_translatable_enabled":              true,
	"view_counts_everywhere_api_enabled":                                      true,
	"longform_notetweets_consumption_enabled":                                 true,
	"responsive_web_twitter_article_tweet_consumption_enabled":                true,
	"tweet_awards_web_tipping_enabled":                                        false,
	"creator_subscriptions_quote_tweet_preview_enabled":                       false,
	"content_disclosure_indicator_enabled":                                    true,
	"content_disclosure_ai_generated_indicator_enabled":                       true,
	"responsive_web_grok_show_grok_translated_post":                           true,
	"responsive_web_grok_analysis_button_from_backend":                        true,
	"post_ctas_fetch_enabled":                                                 true,
	"rweb_cashtags_enabled":                                                   true,
	"freedom_of_speech_not_reach_fetch_enabled":                               true,
	"standardized_nudges_misinfo":                                             true,
	"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled": true,
	"longform_notetweets_rich_text_read_enabled":                              true,
	"longform_notetweets_inline_media_enabled":                                true,
	"profile_label_improvements_pcf_label_in_post_enabled":                    true,
	"responsive_web_profile_redirect_enabled":                                 true,
	"rweb_tipjar_consumption_enabled":                                         true,
	"responsive_web_grok_image_annotation_enabled":                            true,
	"responsive_web_grok_imagine_annotation_enabled":                          true,
	"responsive_web_grok_community_note_auto_translation_is_enabled":          true,
	"responsive_web_enhance_cards_enabled":                                    true,
}

var tweetResultFieldToggles = map[string]bool{
	"withArticleRichContentState": true,
	"withArticlePlainText":        true,
	"withArticleSummaryText":      true,
	"withArticleVoiceOver":        true,
	"withGrokAnalyze":             false,
	"withDisallowedReplyControls": true,
	"withPayments":                false,
	"withAuxiliaryUserLabels":     false,
}

type Options struct {
	Limit       int
	Force       bool
	QuoteOnly   bool
	Concurrency int
	Browser     string
	Profile     string
	CT0         string
	AuthToken   string
	Timeout     time.Duration
	Logger      *slog.Logger
}

type Stats struct {
	Candidates      int `json:"candidates"`
	Requested       int `json:"requested"`
	Hydrated        int `json:"hydrated"`
	Missing         int `json:"missing"`
	APIErrors       int `json:"api_errors"`
	Rendered        int `json:"rendered"`
	Unchanged       int `json:"unchanged"`
	MediaCandidates int `json:"media_candidates"`
	MediaRequested  int `json:"media_requested"`
	MediaDownloaded int `json:"media_downloaded"`
	MediaGone       int `json:"media_gone"`
	MediaErrors     int `json:"media_errors"`
}

type Client struct {
	httpClient   *http.Client
	csrfToken    string
	cookieHeader string
	logger       *slog.Logger
}

type fetchResult struct {
	item      model.Item
	hydration model.XHydration
	requested bool
	err       error
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.Concurrency > 16 {
		opts.Concurrency = 16
	}

	var (
		items []model.Item
		err   error
	)
	if opts.QuoteOnly {
		items, err = st.ListItemsForXQuoteHydration(ctx, opts.Limit, opts.Force)
	} else {
		items, err = st.ListItemsForXHydration(ctx, opts.Limit, opts.Force)
	}
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{Candidates: len(items)}
	debugLog(opts.Logger, "x hydration candidates loaded",
		"candidates", len(items),
		"concurrency", opts.Concurrency,
		"force", opts.Force,
		"limit", opts.Limit,
	)
	if len(items) == 0 {
		return stats, nil
	}

	var client *Client
	if requiresRemoteFetch(items, opts.Force) {
		client, err = newClient(ctx, opts)
		if err != nil {
			return Stats{}, err
		}
	}

	jobs := make(chan model.Item)
	results := make(chan fetchResult, opts.Concurrency)

	var wg sync.WaitGroup
	for range opts.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				debugLog(opts.Logger, "hydrating x post",
					"source_key", item.SourceKey,
					"tweet_id", item.ExternalID,
				)
				hydration, requested, fetchErr := hydrateItem(ctx, client, item, opts.Force)
				results <- fetchResult{item: item, hydration: hydration, requested: requested, err: fetchErr}
			}
		}()
	}

	go func() {
		for _, item := range items {
			jobs <- item
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	processed := 0
	for result := range results {
		if result.err != nil {
			return stats, result.err
		}
		processed++

		normalizedHydration, snapshot, hydrationNormalized, err := normalizeHydration(result.hydration, result.item.ExternalID)
		if err != nil {
			return stats, err
		}
		result.hydration = normalizedHydration

		if result.requested {
			stats.Requested++
		}
		changed, saveErr := st.SaveXHydration(ctx, result.item.ID, result.hydration)
		if saveErr != nil {
			return stats, saveErr
		}

		mediaStats, mediaErr := mediadownload.RunForItem(ctx, cfg, st, result.item.ID, mediadownload.Options{
			Force:   opts.Force,
			Timeout: opts.Timeout,
			Logger:  opts.Logger,
		})
		if mediaErr != nil {
			return stats, mediaErr
		}
		stats.MediaCandidates += mediaStats.Candidates
		stats.MediaRequested += mediaStats.Requested
		stats.MediaDownloaded += mediaStats.Downloaded
		stats.MediaGone += mediaStats.Gone
		stats.MediaErrors += mediaStats.Errors

		quoteStats, quoteChanged, quoteRendered, quoteErr := syncQuotedPosts(ctx, cfg, st, result.item, result.hydration, snapshot, opts)
		if quoteErr != nil {
			return stats, quoteErr
		}
		stats.MediaCandidates += quoteStats.Candidates
		stats.MediaRequested += quoteStats.Requested
		stats.MediaDownloaded += quoteStats.Downloaded
		stats.MediaGone += quoteStats.Gone
		stats.MediaErrors += quoteStats.Errors
		stats.Rendered += quoteRendered

		switch result.hydration.Status {
		case "ok_graphql", "ok_syndication":
			stats.Hydrated++
		case "not_found":
			stats.Missing++
		default:
			stats.APIErrors++
		}

		mediaChanged := mediaStats.Changed > 0
		if changed || mediaChanged || quoteChanged || hydrationNormalized {
			refreshed, err := st.GetItem(ctx, result.item.SourceKey)
			if err != nil {
				return stats, err
			}
			if err := vault.WriteItem(cfg, refreshed); err != nil {
				return stats, fmt.Errorf("render hydrated note %s: %w", result.item.SourceKey, err)
			}
			stats.Rendered++
		} else {
			stats.Unchanged++
		}

		debugLog(opts.Logger, "x hydration result",
			"source_key", result.item.SourceKey,
			"tweet_id", result.item.ExternalID,
			"status", result.hydration.Status,
			"changed", changed,
			"media_changed", mediaChanged,
			"media_requested", mediaStats.Requested,
			"media_downloaded", mediaStats.Downloaded,
		)
		if opts.Logger != nil && (processed%25 == 0 || result.hydration.Status != "ok_graphql" || mediaStats.Requested > 0) {
			opts.Logger.Info("x hydration progress",
				"processed", processed,
				"requested", stats.Requested,
				"candidates", stats.Candidates,
				"hydrated", stats.Hydrated,
				"missing", stats.Missing,
				"api_errors", stats.APIErrors,
				"rendered", stats.Rendered,
				"unchanged", stats.Unchanged,
				"media_candidates", stats.MediaCandidates,
				"media_requested", stats.MediaRequested,
				"media_downloaded", stats.MediaDownloaded,
				"media_gone", stats.MediaGone,
				"media_errors", stats.MediaErrors,
				"remaining", stats.Candidates-processed,
			)
		}
	}

	return stats, nil
}

func requiresRemoteFetch(items []model.Item, force bool) bool {
	for _, item := range items {
		if shouldFetchItem(item, force) {
			return true
		}
	}
	return false
}

func needsQuotedSnapshotDirectFetch(item model.Item) bool {
	return item.SourceType == "x_quote" &&
		item.XPostStatus == "ok_graphql" &&
		!strings.Contains(item.XPostJSON, `"tweetResult"`)
}

func shouldFetchItem(item model.Item, force bool) bool {
	if force {
		return true
	}
	if needsQuotedSnapshotDirectFetch(item) {
		return true
	}
	switch item.XPostStatus {
	case "", "api_error", "error", "rate_limited":
		return true
	default:
		return false
	}
}

func hydrateItem(ctx context.Context, client *Client, item model.Item, force bool) (model.XHydration, bool, error) {
	if !shouldFetchItem(item, force) {
		return model.XHydration{
			FullText:  item.XPostText,
			Language:  item.XPostLang,
			APIJSON:   item.XPostJSON,
			FetchedAt: item.XPostFetchedAt,
			Status:    item.XPostStatus,
			Error:     item.XPostError,
		}, false, nil
	}
	if client == nil {
		return model.XHydration{}, false, fmt.Errorf("x client is required to hydrate tweet %s", item.ExternalID)
	}
	hydration, err := client.FetchPost(ctx, item.ExternalID)
	return hydration, true, err
}

func normalizeHydration(hydration model.XHydration, fallbackTweetID string) (model.XHydration, *xpost.Snapshot, bool, error) {
	rawJSON := strings.TrimSpace(hydration.APIJSON)
	if rawJSON == "" {
		return hydration, nil, false, nil
	}

	var envelope struct {
		Source    string          `json:"source"`
		FetchedAt string          `json:"fetched_at"`
		Snapshot  *xpost.Snapshot `json:"snapshot"`
		Raw       map[string]any  `json:"raw"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &envelope); err != nil {
		return hydration, nil, false, fmt.Errorf("decode x hydration envelope for %s: %w", fallbackTweetID, err)
	}

	normalized := envelope.Snapshot
	switch strings.TrimSpace(envelope.Source) {
	case "graphql":
		if rebuilt := parseGraphQLSnapshot(fallbackTweetID, envelope.Raw); rebuilt != nil {
			normalized = rebuilt
		}
	case "syndication":
		if rebuilt := parseSyndicationSnapshot(fallbackTweetID, envelope.Raw); rebuilt != nil {
			normalized = rebuilt
		}
	}
	if normalized == nil {
		return hydration, nil, false, nil
	}

	hydration.FullText = strings.TrimSpace(normalized.Text)
	hydration.Language = strings.TrimSpace(normalized.Language)
	if reflect.DeepEqual(envelope.Snapshot, xpost.ForStorage(normalized)) {
		return hydration, normalized, false, nil
	}

	envelope.Snapshot = xpost.ForStorage(normalized)
	rewritten, err := json.Marshal(envelope)
	if err != nil {
		return hydration, nil, false, fmt.Errorf("marshal normalized x hydration for %s: %w", fallbackTweetID, err)
	}
	hydration.APIJSON = string(rewritten)
	return hydration, normalized, true, nil
}

func syncQuotedPosts(ctx context.Context, cfg config.Config, st *store.Store, parent model.Item, hydration model.XHydration, snapshot *xpost.Snapshot, opts Options) (mediadownload.Stats, bool, int, error) {
	if snapshot == nil || snapshot.QuotedPost == nil {
		changed, err := st.ReplaceItemChildLinks(ctx, parent.ID, "quoted_post", nil)
		return mediadownload.Stats{}, changed, 0, err
	}

	visited := map[string]struct{}{
		strings.TrimSpace(parent.ExternalID): {},
	}
	childID, mediaStats, childRendered, err := upsertQuotedPostTree(ctx, cfg, st, snapshot.QuotedPost, hydration, opts, visited)
	if err != nil {
		return mediadownload.Stats{}, false, 0, err
	}
	if childID <= 0 {
		changed, err := st.ReplaceItemChildLinks(ctx, parent.ID, "quoted_post", nil)
		return mediadownload.Stats{}, changed, childRendered, err
	}
	linkChanged, err := st.ReplaceItemChildLinks(ctx, parent.ID, "quoted_post", []int64{childID})
	if err != nil {
		return mediadownload.Stats{}, false, 0, err
	}
	return mediaStats, linkChanged, childRendered, nil
}

func upsertQuotedPostTree(ctx context.Context, cfg config.Config, st *store.Store, snapshot *xpost.Snapshot, hydration model.XHydration, opts Options, visited map[string]struct{}) (int64, mediadownload.Stats, int, error) {
	if snapshot == nil {
		return 0, mediadownload.Stats{}, 0, nil
	}
	snapshotID := strings.TrimSpace(snapshot.ID)
	if snapshotID == "" {
		return 0, mediadownload.Stats{}, 0, nil
	}
	if _, exists := visited[snapshotID]; exists {
		return 0, mediadownload.Stats{}, 0, nil
	}
	visited[snapshotID] = struct{}{}

	item, err := quotedSnapshotToItem(snapshot, hydration.FetchedAt)
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}
	upsertResult, err := st.UpsertItem(ctx, item)
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}

	childHydration, err := buildSnapshotHydration(hydration.Status, snapshot, snapshot.Raw, hydration.FetchedAt)
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}
	hydrationChanged, err := st.SaveXHydration(ctx, upsertResult.ItemID, childHydration)
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}

	effectiveSnapshot := snapshot
	refreshedItem, err := st.GetItem(ctx, item.SourceKey)
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}
	if storedSnapshot, err := snapshotFromHydrationJSON(refreshedItem.ExternalID, refreshedItem.XPostJSON); err != nil {
		return 0, mediadownload.Stats{}, 0, err
	} else if storedSnapshot != nil {
		effectiveSnapshot = storedSnapshot
	}

	mediaStats, err := mediadownload.RunForItem(ctx, cfg, st, upsertResult.ItemID, mediadownload.Options{
		Force:   opts.Force,
		Timeout: opts.Timeout,
		Logger:  opts.Logger,
	})
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}

	var childIDs []int64
	rendered := 0
	if effectiveSnapshot.QuotedPost != nil {
		childID, nestedMediaStats, nestedRendered, err := upsertQuotedPostTree(ctx, cfg, st, effectiveSnapshot.QuotedPost, hydration, opts, visited)
		if err != nil {
			return 0, mediadownload.Stats{}, 0, err
		}
		mediaStats.Candidates += nestedMediaStats.Candidates
		mediaStats.Requested += nestedMediaStats.Requested
		mediaStats.Downloaded += nestedMediaStats.Downloaded
		mediaStats.Gone += nestedMediaStats.Gone
		mediaStats.Errors += nestedMediaStats.Errors
		mediaStats.Changed += nestedMediaStats.Changed
		rendered += nestedRendered
		if childID > 0 {
			childIDs = append(childIDs, childID)
		}
	}

	linkChanged, err := st.ReplaceItemChildLinks(ctx, upsertResult.ItemID, "quoted_post", childIDs)
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}

	if upsertResult.Status != model.UpsertUnchanged || hydrationChanged || mediaStats.Changed > 0 || linkChanged {
		refreshed, err := st.GetItem(ctx, item.SourceKey)
		if err != nil {
			return 0, mediadownload.Stats{}, 0, err
		}
		if err := vault.WriteItem(cfg, refreshed); err != nil {
			return 0, mediadownload.Stats{}, 0, fmt.Errorf("render quoted x note %s: %w", refreshed.SourceKey, err)
		}
		rendered++
	}

	return upsertResult.ItemID, mediaStats, rendered, nil
}

func snapshotFromHydrationJSON(fallbackTweetID, apiJSON string) (*xpost.Snapshot, error) {
	if strings.TrimSpace(apiJSON) == "" {
		return nil, nil
	}
	_, snapshot, _, err := normalizeHydration(model.XHydration{APIJSON: apiJSON}, fallbackTweetID)
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func quotedSnapshotToItem(snapshot *xpost.Snapshot, fetchedAt time.Time) (model.Item, error) {
	record := bookmarkRecord{
		ID:           strings.TrimSpace(snapshot.ID),
		TweetID:      strings.TrimSpace(snapshot.ID),
		URL:          strings.TrimSpace(snapshot.URL),
		Text:         strings.TrimSpace(snapshot.Text),
		AuthorHandle: strings.TrimSpace(snapshot.AuthorHandle),
		AuthorName:   strings.TrimSpace(snapshot.AuthorName),
		PostedAt:     xpost.NormalizeTimestamp(snapshot.PostedAt),
		BookmarkedAt: "",
		SyncedAt:     fetchedAt.UTC().Format(time.RFC3339),
		Language:     strings.TrimSpace(snapshot.Language),
		Links:        append([]string(nil), snapshot.Links...),
		IngestedVia:  "quoted-post",
	}
	item, err := bookmarkRecordToItem(record, fetchedAt.UTC())
	if err != nil {
		return model.Item{}, err
	}
	item.SourceType = "x_quote"
	item.SavedAt = ""
	item.RawJSON = string(mustJSON(snapshot))
	item.ContentHash = itemhash.Compute(item)
	return item, nil
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func newClient(ctx context.Context, opts Options) (*Client, error) {
	csrfToken := strings.TrimSpace(opts.CT0)
	authToken := strings.TrimSpace(opts.AuthToken)
	if csrfToken == "" {
		ct0, auth, err := resolveCookies(ctx, opts)
		if err != nil {
			return nil, err
		}
		csrfToken = ct0
		authToken = auth
	}
	if csrfToken == "" {
		return nil, fmt.Errorf("could not resolve X ct0 cookie")
	}

	cookieParts := []string{"ct0=" + csrfToken}
	if authToken != "" {
		cookieParts = append(cookieParts, "auth_token="+authToken)
	}

	return &Client{
		httpClient:   &http.Client{Timeout: opts.Timeout},
		csrfToken:    csrfToken,
		cookieHeader: strings.Join(cookieParts, "; "),
		logger:       opts.Logger,
	}, nil
}

func resolveCookies(ctx context.Context, opts Options) (string, string, error) {
	profiles := map[sweetcookie.Browser]string{}
	browsers, err := parseBrowsers(opts.Browser)
	if err != nil {
		return "", "", err
	}
	if opts.Profile != "" {
		if len(browsers) == 0 {
			return "", "", fmt.Errorf("--profile requires --browser so the profile target is unambiguous")
		}
		profiles[browsers[0]] = opts.Profile
	}

	result, err := sweetcookie.Get(ctx, sweetcookie.Options{
		URL:      "https://x.com/",
		Names:    []string{"ct0", "auth_token"},
		Browsers: browsers,
		Profiles: profiles,
		Mode:     sweetcookie.ModeFirst,
		Timeout:  opts.Timeout,
	})
	if err != nil {
		return "", "", fmt.Errorf(
			"load browser cookies: %w\n\nIf macOS shows a Keychain prompt for Go or dbrain, choose Allow or Always Allow\nYou can also bypass browser lookup with --ct0 and --auth-token",
			err,
		)
	}

	var ct0 string
	var authToken string
	for _, cookie := range result.Cookies {
		switch cookie.Name {
		case "ct0":
			if ct0 == "" {
				ct0 = cookie.Value
			}
		case "auth_token":
			if authToken == "" {
				authToken = cookie.Value
			}
		}
	}

	if ct0 == "" {
		msg := "could not find ct0 cookie for x.com in local browser profiles"
		if len(result.Warnings) > 0 {
			msg += ": " + strings.Join(result.Warnings, " | ")
		}
		return "", "", errors.New(msg)
	}

	return ct0, authToken, nil
}

func parseBrowsers(value string) ([]sweetcookie.Browser, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}

	parts := strings.Split(value, ",")
	result := make([]sweetcookie.Browser, 0, len(parts))
	for _, part := range parts {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "chrome":
			result = append(result, sweetcookie.BrowserChrome)
		case "chromium":
			result = append(result, sweetcookie.BrowserChromium)
		case "edge":
			result = append(result, sweetcookie.BrowserEdge)
		case "brave":
			result = append(result, sweetcookie.BrowserBrave)
		case "vivaldi":
			result = append(result, sweetcookie.BrowserVivaldi)
		case "opera":
			result = append(result, sweetcookie.BrowserOpera)
		case "firefox":
			result = append(result, sweetcookie.BrowserFirefox)
		case "safari":
			result = append(result, sweetcookie.BrowserSafari)
		default:
			return nil, fmt.Errorf("unsupported browser %q", strings.TrimSpace(part))
		}
	}
	return result, nil
}

func (c *Client) FetchPost(ctx context.Context, tweetID string) (model.XHydration, error) {
	graphql, err := c.fetchViaGraphQL(ctx, tweetID)
	if err != nil {
		return model.XHydration{}, err
	}

	switch graphql.Status {
	case "ok_graphql", "not_found", "empty":
		return graphql, nil
	case "forbidden", "rate_limited", "error":
		debugLog(c.logger, "falling back to x syndication",
			"tweet_id", tweetID,
			"graphql_status", graphql.Status,
			"graphql_error", graphql.Error,
		)
		return c.fetchViaSyndication(ctx, tweetID)
	default:
		return graphql, nil
	}
}

func (c *Client) fetchViaGraphQL(ctx context.Context, tweetID string) (model.XHydration, error) {
	endpoint := buildTweetResultByRestIDURL(tweetID)
	for attempt := range 4 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return model.XHydration{}, fmt.Errorf("create graphql request for %s: %w", tweetID, err)
		}
		for key, value := range buildHeaders(c.csrfToken, c.cookieHeader) {
			req.Header.Set(key, value)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if sleepErr := sleepWithContext(ctx, time.Duration(2*(attempt+1))*time.Second); sleepErr != nil {
				return model.XHydration{}, sleepErr
			}
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return model.XHydration{}, fmt.Errorf("read graphql response for %s: %w", tweetID, readErr)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			debugLog(c.logger, "x graphql rate limited",
				"tweet_id", tweetID,
				"attempt", attempt+1,
			)
			return model.XHydration{
				FetchedAt: time.Now().UTC(),
				Status:    "rate_limited",
				Error:     "X GraphQL rate limited; falling back to syndication",
			}, nil
		}
		if resp.StatusCode >= 500 {
			debugLog(c.logger, "x graphql server error, retrying",
				"tweet_id", tweetID,
				"attempt", attempt+1,
				"status_code", resp.StatusCode,
			)
			if sleepErr := sleepWithContext(ctx, time.Duration(5*(attempt+1))*time.Second); sleepErr != nil {
				return model.XHydration{}, sleepErr
			}
			continue
		}

		fetchedAt := time.Now().UTC()
		if resp.StatusCode == http.StatusNotFound {
			return model.XHydration{FetchedAt: fetchedAt, Status: "not_found", Error: "post not found via X GraphQL"}, nil
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return model.XHydration{FetchedAt: fetchedAt, Status: "forbidden", Error: fmt.Sprintf("X GraphQL denied the request (status=%d)", resp.StatusCode)}, nil
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return model.XHydration{FetchedAt: fetchedAt, Status: "error", Error: fmt.Sprintf("X GraphQL returned status=%d body=%s", resp.StatusCode, trimForError(string(body), 300))}, nil
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			return model.XHydration{FetchedAt: fetchedAt, Status: "error", Error: fmt.Sprintf("invalid X GraphQL JSON: %v", err)}, nil
		}

		result := dig(payload, "data", "tweetResult", "result")
		typename, _ := result["__typename"].(string)
		if len(result) == 0 || typename == "TweetTombstone" || typename == "TweetUnavailable" {
			return model.XHydration{FetchedAt: fetchedAt, Status: "not_found", Error: "post unavailable via X GraphQL"}, nil
		}

		snapshot := parseGraphQLSnapshot(tweetID, payload)
		if snapshot == nil || strings.TrimSpace(snapshot.Text) == "" {
			envelope := map[string]any{
				"source":     "graphql",
				"fetched_at": fetchedAt.Format(time.RFC3339),
				"raw":        payload,
			}
			apiJSON, _ := json.Marshal(envelope)
			return model.XHydration{
				Language:  "",
				APIJSON:   string(apiJSON),
				FetchedAt: fetchedAt,
				Status:    "empty",
				Error:     "tweet exists but has no text content",
			}, nil
		}

		return buildSnapshotHydration("ok_graphql", snapshot, payload, fetchedAt)
	}

	return model.XHydration{FetchedAt: time.Now().UTC(), Status: "rate_limited", Error: "X GraphQL rate limited after retries"}, nil
}

func (c *Client) fetchViaSyndication(ctx context.Context, tweetID string) (model.XHydration, error) {
	endpoint := syndicationURL + "?id=" + url.QueryEscape(tweetID) + "&token=x"
	for attempt := range 4 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return model.XHydration{}, fmt.Errorf("create syndication request for %s: %w", tweetID, err)
		}
		req.Header.Set("user-agent", chromeUA)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if sleepErr := sleepWithContext(ctx, time.Duration(2*(attempt+1))*time.Second); sleepErr != nil {
				return model.XHydration{}, sleepErr
			}
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return model.XHydration{}, fmt.Errorf("read syndication response for %s: %w", tweetID, readErr)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			debugLog(c.logger, "x syndication rate limited",
				"tweet_id", tweetID,
				"attempt", attempt+1,
			)
			return model.XHydration{
				FetchedAt: time.Now().UTC(),
				Status:    "rate_limited",
				Error:     "X syndication rate limited",
			}, nil
		}
		if resp.StatusCode >= 500 {
			debugLog(c.logger, "x syndication server error, retrying",
				"tweet_id", tweetID,
				"attempt", attempt+1,
				"status_code", resp.StatusCode,
			)
			if sleepErr := sleepWithContext(ctx, time.Duration(5*(attempt+1))*time.Second); sleepErr != nil {
				return model.XHydration{}, sleepErr
			}
			continue
		}

		fetchedAt := time.Now().UTC()
		if resp.StatusCode == http.StatusNotFound {
			return model.XHydration{FetchedAt: fetchedAt, Status: "not_found", Error: "post not found via syndication"}, nil
		}
		if resp.StatusCode == http.StatusForbidden {
			return model.XHydration{FetchedAt: fetchedAt, Status: "forbidden", Error: "syndication denied the request"}, nil
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return model.XHydration{FetchedAt: fetchedAt, Status: "error", Error: fmt.Sprintf("syndication returned status=%d body=%s", resp.StatusCode, trimForError(string(body), 300))}, nil
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			return model.XHydration{FetchedAt: fetchedAt, Status: "error", Error: fmt.Sprintf("invalid syndication JSON: %v", err)}, nil
		}

		snapshot := parseSyndicationSnapshot(tweetID, payload)
		if snapshot == nil || strings.TrimSpace(snapshot.Text) == "" {
			envelope := map[string]any{
				"source":     "syndication",
				"fetched_at": fetchedAt.Format(time.RFC3339),
				"raw":        payload,
			}
			apiJSON, _ := json.Marshal(envelope)
			return model.XHydration{
				Language:  "",
				APIJSON:   string(apiJSON),
				FetchedAt: fetchedAt,
				Status:    "empty",
				Error:     "tweet exists but has no text content",
			}, nil
		}

		return buildSnapshotHydration("ok_syndication", snapshot, payload, fetchedAt)
	}

	return model.XHydration{FetchedAt: time.Now().UTC(), Status: "rate_limited", Error: "syndication rate limited after retries"}, nil
}

func buildSnapshotHydration(status string, snapshot *xpost.Snapshot, payload map[string]any, fetchedAt time.Time) (model.XHydration, error) {
	envelope := map[string]any{
		"source":     strings.TrimPrefix(status, "ok_"),
		"fetched_at": fetchedAt.Format(time.RFC3339),
		"snapshot":   xpost.ForStorage(snapshot),
		"raw":        payload,
	}
	apiJSON, err := json.Marshal(envelope)
	if err != nil {
		return model.XHydration{}, fmt.Errorf("marshal hydrated payload for %s: %w", snapshot.ID, err)
	}

	return model.XHydration{
		FullText:  strings.TrimSpace(snapshot.Text),
		Language:  strings.TrimSpace(snapshot.Language),
		APIJSON:   string(apiJSON),
		FetchedAt: fetchedAt,
		Status:    status,
		Error:     "",
	}, nil
}

func buildTweetResultByRestIDURL(tweetID string) string {
	variables := map[string]any{
		"tweetId":                tweetID,
		"withCommunity":          true,
		"includePromotedContent": true,
		"withVoice":              true,
	}
	params := url.Values{}
	vars, _ := json.Marshal(variables)
	features, _ := json.Marshal(tweetResultFeatures)
	fieldToggles, _ := json.Marshal(tweetResultFieldToggles)
	params.Set("variables", string(vars))
	params.Set("features", string(features))
	params.Set("fieldToggles", string(fieldToggles))
	return "https://x.com/i/api/graphql/" + tweetResultByRestIDQueryID + "/" + tweetResultByRestIDOperation + "?" + params.Encode()
}

func buildHeaders(csrfToken, cookieHeader string) map[string]string {
	return map[string]string{
		"authorization":         "Bearer " + xPublicBearer,
		"x-csrf-token":          csrfToken,
		"x-twitter-auth-type":   "OAuth2Session",
		"x-twitter-active-user": "yes",
		"content-type":          "application/json",
		"user-agent":            chromeUA,
		"cookie":                cookieHeader,
	}
}

func parseGraphQLSnapshot(tweetID string, payload map[string]any) *xpost.Snapshot {
	result := dig(payload, "data", "tweetResult", "result")
	return parseGraphQLSnapshotNode(result, tweetID)
}

func parseGraphQLSnapshotNode(result map[string]any, fallbackID string) *xpost.Snapshot {
	if len(result) == 0 {
		return nil
	}
	tweet := result
	if nested := mapValue(result["tweet"]); len(nested) > 0 {
		tweet = nested
	}
	legacy := mapValue(tweet["legacy"])
	if len(legacy) == 0 {
		return nil
	}

	noteText := stringValue(dig(tweet, "note_tweet", "note_tweet_results", "result")["text"])
	text := firstNonEmpty(noteText, stringValue(legacy["full_text"]), stringValue(legacy["text"]))
	handle := firstNonEmpty(
		stringValue(dig(tweet, "core", "user_results", "result", "core")["screen_name"]),
		stringValue(dig(tweet, "core", "user_results", "result", "legacy")["screen_name"]),
	)
	name := firstNonEmpty(
		stringValue(dig(tweet, "core", "user_results", "result", "core")["name"]),
		stringValue(dig(tweet, "core", "user_results", "result", "legacy")["name"]),
	)
	profileImage := firstNonEmpty(
		stringValue(dig(tweet, "core", "user_results", "result", "avatar")["image_url"]),
		stringValue(dig(tweet, "core", "user_results", "result", "legacy")["profile_image_url_https"]),
	)
	resolvedID := firstNonEmpty(stringValue(legacy["id_str"]), stringValue(tweet["rest_id"]), fallbackID)

	mediaEntities := listValue(dig(legacy, "extended_entities")["media"])
	if len(mediaEntities) == 0 {
		mediaEntities = listValue(dig(legacy, "entities")["media"])
	}

	snapshot := &xpost.Snapshot{
		ID:                    resolvedID,
		Text:                  text,
		Language:              stringValue(legacy["lang"]),
		AuthorHandle:          handle,
		AuthorName:            name,
		AuthorProfileImageURL: profileImage,
		PostedAt:              xpost.NormalizeTimestamp(stringValue(legacy["created_at"])),
		URL:                   "https://x.com/" + firstNonEmpty(handle, "_") + "/status/" + resolvedID,
		Links:                 extractEntityExpandedURLs(mediaEntities, tweetURLEntitySets(tweet, legacy)...),
		Raw:                   tweet,
	}
	for _, media := range mediaEntities {
		mediaURL := selectMediaURL(media)
		snapshot.Media = append(snapshot.Media, mediaURL)
		snapshot.MediaObjects = append(snapshot.MediaObjects, xpost.MediaObject{
			Type:        stringValue(media["type"]),
			URL:         mediaURL,
			ExpandedURL: stringValue(media["expanded_url"]),
			Width:       intValue(dig(media, "original_info")["width"]),
			Height:      intValue(dig(media, "original_info")["height"]),
		})
	}
	if quoted := parseGraphQLSnapshotNode(dig(tweet, "quoted_status_result", "result"), stringValue(legacy["quoted_status_id_str"])); quoted != nil {
		snapshot.QuotedPost = quoted
	} else if quotedID := stringValue(legacy["quoted_status_id_str"]); quotedID != "" {
		snapshot.QuotedPost = &xpost.Snapshot{
			ID:  quotedID,
			URL: firstNonEmpty(stringValue(dig(legacy, "quoted_status_permalink")["expanded"]), "https://x.com/i/web/status/"+quotedID),
			Raw: map[string]any{
				"legacy": map[string]any{
					"quoted_status_id_str": quotedID,
				},
			},
		}
	}
	return snapshot
}

func parseSyndicationSnapshot(tweetID string, payload map[string]any) *xpost.Snapshot {
	return parseSyndicationSnapshotNode(payload, tweetID)
}

func parseSyndicationSnapshotNode(payload map[string]any, fallbackID string) *xpost.Snapshot {
	text := stringValue(payload["text"])
	if text == "" {
		return nil
	}
	handle := stringValue(dig(payload, "user")["screen_name"])
	name := stringValue(dig(payload, "user")["name"])
	profileImage := stringValue(dig(payload, "user")["profile_image_url_https"])
	resolvedID := firstNonEmpty(stringValue(payload["id_str"]), fallbackID)
	mediaDetails := listValue(payload["mediaDetails"])

	snapshot := &xpost.Snapshot{
		ID:                    resolvedID,
		Text:                  text,
		Language:              "",
		AuthorHandle:          handle,
		AuthorName:            name,
		AuthorProfileImageURL: profileImage,
		PostedAt:              xpost.NormalizeTimestamp(stringValue(payload["created_at"])),
		URL:                   "https://x.com/" + firstNonEmpty(handle, "_") + "/status/" + resolvedID,
		Links:                 extractEntityExpandedURLs(mediaDetails, dig(payload, "entities")["urls"]),
		Raw:                   payload,
	}
	for _, media := range mediaDetails {
		mediaURL := selectMediaURL(media)
		snapshot.Media = append(snapshot.Media, mediaURL)
		snapshot.MediaObjects = append(snapshot.MediaObjects, xpost.MediaObject{
			Type:   stringValue(media["type"]),
			URL:    mediaURL,
			Width:  intValue(dig(media, "original_info")["width"]),
			Height: intValue(dig(media, "original_info")["height"]),
		})
	}
	quotedPayload := mapValue(payload["quoted_tweet"])
	if len(quotedPayload) == 0 {
		quotedPayload = mapValue(mapValue(payload["parent"])["quoted_tweet"])
	}
	if quoted := parseSyndicationSnapshotNode(quotedPayload, ""); quoted != nil {
		snapshot.QuotedPost = quoted
	}
	return snapshot
}

func tweetURLEntitySets(tweet, legacy map[string]any) []any {
	sets := []any{dig(legacy, "entities")["urls"]}
	noteResult := dig(tweet, "note_tweet", "note_tweet_results", "result")
	if len(noteResult) > 0 {
		sets = append(sets, dig(noteResult, "entity_set")["urls"], dig(noteResult, "entities")["urls"])
	}
	return sets
}

func extractEntityExpandedURLs(mediaEntities []map[string]any, urlEntitySets ...any) []string {
	seen := map[string]struct{}{}
	links := make([]string, 0, 4)
	for _, urlEntities := range urlEntitySets {
		for _, entity := range listValue(urlEntities) {
			if expanded := stringValue(entity["expanded_url"]); expanded != "" {
				if _, exists := seen[expanded]; !exists {
					seen[expanded] = struct{}{}
					links = append(links, expanded)
				}
			}
		}
	}
	for _, media := range mediaEntities {
		if expanded := stringValue(media["expanded_url"]); expanded != "" {
			if _, exists := seen[expanded]; !exists {
				seen[expanded] = struct{}{}
				links = append(links, expanded)
			}
		}
	}
	return links
}

func selectMediaURL(media map[string]any) string {
	mediaType := stringValue(media["type"])
	if mediaType == "video" || mediaType == "animated_gif" {
		if variant := bestVideoVariantURL(media); variant != "" {
			return variant
		}
	}
	return firstNonEmpty(stringValue(media["media_url_https"]), stringValue(media["media_url"]))
}

func bestVideoVariantURL(media map[string]any) string {
	var bestURL string
	bestBitrate := -1
	for _, variant := range listValue(dig(media, "video_info")["variants"]) {
		url := stringValue(variant["url"])
		contentType := strings.ToLower(stringValue(variant["content_type"]))
		if url == "" {
			continue
		}
		if contentType != "" && !strings.Contains(contentType, "mp4") {
			continue
		}
		bitrate := intValue(variant["bitrate"])
		if bitrate > bestBitrate {
			bestBitrate = bitrate
			bestURL = url
		}
	}
	return bestURL
}

func dig(m map[string]any, keys ...string) map[string]any {
	current := m
	for _, key := range keys {
		next := mapValue(current[key])
		if len(next) == 0 {
			return nil
		}
		current = next
	}
	return current
}

func mapValue(value any) map[string]any {
	if value == nil {
		return nil
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func listValue(value any) []map[string]any {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if m, ok := entry.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func intValue(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func trimForError(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "..."
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}
