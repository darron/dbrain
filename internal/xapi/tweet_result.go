package xapi

import (
	"encoding/json"
	"net/url"
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
