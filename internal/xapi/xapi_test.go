package xapi

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/xpost"
)

func TestBuildTweetResultByRestIDURLIncludesArticleFieldToggles(t *testing.T) {
	t.Parallel()

	rawURL := buildTweetResultByRestIDURL("2028894099483578872")
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	if got := parsed.Path; got != "/i/api/graphql/"+tweetResultByRestIDQueryID+"/"+tweetResultByRestIDOperation {
		t.Fatalf("unexpected path: %q", got)
	}

	query := parsed.Query()

	var variables map[string]any
	if err := json.Unmarshal([]byte(query.Get("variables")), &variables); err != nil {
		t.Fatalf("unmarshal variables: %v", err)
	}
	if variables["tweetId"] != "2028894099483578872" {
		t.Fatalf("unexpected tweet id: %#v", variables["tweetId"])
	}
	if variables["includePromotedContent"] != true {
		t.Fatalf("expected includePromotedContent=true, got %#v", variables["includePromotedContent"])
	}
	if variables["withCommunity"] != true {
		t.Fatalf("expected withCommunity=true, got %#v", variables["withCommunity"])
	}
	if variables["withVoice"] != true {
		t.Fatalf("expected withVoice=true, got %#v", variables["withVoice"])
	}

	var fieldToggles map[string]bool
	if err := json.Unmarshal([]byte(query.Get("fieldToggles")), &fieldToggles); err != nil {
		t.Fatalf("unmarshal field toggles: %v", err)
	}
	for _, key := range []string{
		"withArticleRichContentState",
		"withArticlePlainText",
		"withArticleSummaryText",
		"withArticleVoiceOver",
	} {
		if !fieldToggles[key] {
			t.Fatalf("expected %s=true", key)
		}
	}
}

func TestParseGraphQLSnapshotIncludesNoteTweetLinks(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"data": map[string]any{
			"tweetResult": map[string]any{
				"result": map[string]any{
					"rest_id": "2048567034506838416",
					"core": map[string]any{
						"user_results": map[string]any{
							"result": map[string]any{
								"core": map[string]any{
									"screen_name": "BillboardChris",
									"name":        "Billboard Chris",
								},
							},
						},
					},
					"note_tweet": map[string]any{
						"note_tweet_results": map[string]any{
							"result": map[string]any{
								"text": "Please read https://t.co/ZF83vL2QsR",
								"entity_set": map[string]any{
									"urls": []any{
										map[string]any{
											"url":          "https://t.co/ZF83vL2QsR",
											"expanded_url": "https://example.com/cass-review",
											"display_url":  "example.com/cass-review",
										},
									},
								},
							},
						},
					},
					"legacy": map[string]any{
						"id_str":     "2048567034506838416",
						"full_text":  "Please read",
						"created_at": "Mon Apr 27 00:00:00 +0000 2026",
						"lang":       "en",
						"entities": map[string]any{
							"urls": []any{},
						},
					},
				},
			},
		},
	}

	snapshot := parseGraphQLSnapshot("2048567034506838416", payload)
	if snapshot == nil {
		t.Fatal("expected snapshot")
	}
	if snapshot.Text != "Please read https://t.co/ZF83vL2QsR" {
		t.Fatalf("text = %q", snapshot.Text)
	}
	if got, want := snapshot.Links, []string{"https://example.com/cass-review"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("links = %#v, want %#v", got, want)
	}
}

func TestShouldFetchItemRepairsQuotedSnapshotOnlyHydration(t *testing.T) {
	t.Parallel()

	snapshotOnlyQuote := model.Item{
		SourceType:  "x_quote",
		XPostStatus: "ok_graphql",
		XPostJSON: `{
			"source":"graphql",
			"snapshot":{"id":"2040448463540830705","text":"Quoted child context only"}
		}`,
	}
	if !shouldFetchItem(snapshotOnlyQuote, false) {
		t.Fatal("expected snapshot-only quoted item to be refetched directly")
	}

	directGraphQLQuote := snapshotOnlyQuote
	directGraphQLQuote.XPostJSON = `{
			"source":"graphql",
			"snapshot":{"id":"2040448463540830705","text":"Quoted child context only"},
			"raw":{"data":{"tweetResult":{"result":{"rest_id":"2040448463540830705"}}}}
		}`
	if shouldFetchItem(directGraphQLQuote, false) {
		t.Fatal("expected directly hydrated quoted item to skip refetch")
	}
}

func TestNormalizeHydrationBackfillsQuotedPostFromSyndicationRaw(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 25, 21, 0, 0, 0, time.UTC)
	hydration := model.XHydration{
		FullText:  "Oh this is delicious...",
		Language:  "",
		Status:    "ok_syndication",
		FetchedAt: now,
		APIJSON: `{
			"source":"syndication",
			"fetched_at":"2026-04-25T21:00:00Z",
			"snapshot":{"id":"2030852374739198197","text":"Oh this is delicious..."},
			"raw":{
				"id_str":"2030852374739198197",
				"text":"Oh this is delicious...",
				"user":{"screen_name":"acoyne","name":"Andrew Coyne"},
				"quoted_tweet":{
					"id_str":"2030838203549184127",
					"text":"Quoted context that should be preserved.",
					"user":{"screen_name":"dtripi","name":"Dominic Tripi"}
				}
			}
		}`,
	}

	normalized, snapshot, changed, err := normalizeHydration(hydration, "2030852374739198197")
	if err != nil {
		t.Fatalf("normalizeHydration: %v", err)
	}
	if !changed {
		t.Fatal("expected hydration normalization to rewrite the snapshot")
	}
	if snapshot == nil || snapshot.QuotedPost == nil {
		t.Fatalf("expected quoted post in normalized snapshot, got %#v", snapshot)
	}
	if snapshot.QuotedPost.ID != "2030838203549184127" {
		t.Fatalf("unexpected quoted post id: %#v", snapshot.QuotedPost)
	}
	if snapshot.QuotedPost.AuthorHandle != "dtripi" {
		t.Fatalf("unexpected quoted post author: %#v", snapshot.QuotedPost)
	}
	if snapshot.QuotedPost.Raw == nil {
		t.Fatalf("expected quoted post raw payload to be preserved")
	}
	if normalized.APIJSON == hydration.APIJSON {
		t.Fatal("expected normalized API JSON to differ from the original payload")
	}
}

func TestNormalizeHydrationBackfillsQuotedPostFromSyndicationParentWrapper(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 26, 2, 30, 0, 0, time.UTC)
	hydration := model.XHydration{
		FullText:  "Wrapper-shaped syndication payload",
		Language:  "",
		Status:    "ok_syndication",
		FetchedAt: now,
		APIJSON: `{
			"source":"syndication",
			"fetched_at":"2026-04-26T02:30:00Z",
			"snapshot":{"id":"1701330175886008610","text":"Wrapper-shaped syndication payload"},
			"raw":{
				"id_str":"1701330175886008610",
				"text":"Wrapper-shaped syndication payload",
				"user":{"screen_name":"example","name":"Example"},
				"parent":{
					"quoted_tweet":{
						"id_str":"1698063279158073704",
						"text":"Quoted tweet nested under parent.",
						"user":{"screen_name":"quoted","name":"Quoted Person"}
					}
				}
			}
		}`,
	}

	normalized, snapshot, changed, err := normalizeHydration(hydration, "1701330175886008610")
	if err != nil {
		t.Fatalf("normalizeHydration: %v", err)
	}
	if !changed {
		t.Fatal("expected parent-wrapped syndication payload to rewrite the snapshot")
	}
	if snapshot == nil || snapshot.QuotedPost == nil {
		t.Fatalf("expected quoted post in normalized snapshot, got %#v", snapshot)
	}
	if snapshot.QuotedPost.ID != "1698063279158073704" {
		t.Fatalf("unexpected quoted post id: %#v", snapshot.QuotedPost)
	}
	if snapshot.QuotedPost.AuthorHandle != "quoted" {
		t.Fatalf("unexpected quoted post author: %#v", snapshot.QuotedPost)
	}
	if normalized.APIJSON == hydration.APIJSON {
		t.Fatal("expected normalized API JSON to differ from the original payload")
	}
}

func TestNormalizeHydrationBackfillsQuotedPostFromGraphQLEmptyQuotedStatusResult(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 26, 3, 0, 0, 0, time.UTC)
	hydration := model.XHydration{
		FullText:  "GraphQL payload with empty quoted_status_result",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"source":"graphql",
			"fetched_at":"2026-04-26T03:00:00Z",
			"snapshot":{"id":"2043290674808684987","text":"GraphQL payload with empty quoted_status_result"},
			"raw":{
				"data":{
					"tweetResult":{
						"result":{
							"rest_id":"2043290674808684987",
							"legacy":{
								"id_str":"2043290674808684987",
								"full_text":"GraphQL payload with empty quoted_status_result",
								"lang":"en",
								"quoted_status_id_str":"2043068495915814954",
								"quoted_status_permalink":{"expanded":"https://x.com/example/status/2043068495915814954"}
							},
							"core":{"user_results":{"result":{"core":{"screen_name":"example","name":"Example"}}}},
							"quoted_status_result":{}
						}
					}
				}
			}
		}`,
	}

	normalized, snapshot, changed, err := normalizeHydration(hydration, "2043290674808684987")
	if err != nil {
		t.Fatalf("normalizeHydration: %v", err)
	}
	if !changed {
		t.Fatal("expected empty quoted_status_result payload to rewrite the snapshot")
	}
	if snapshot == nil || snapshot.QuotedPost == nil {
		t.Fatalf("expected quoted post placeholder in normalized snapshot, got %#v", snapshot)
	}
	if snapshot.QuotedPost.ID != "2043068495915814954" {
		t.Fatalf("unexpected quoted post id: %#v", snapshot.QuotedPost)
	}
	if snapshot.QuotedPost.URL != "https://x.com/example/status/2043068495915814954" {
		t.Fatalf("unexpected quoted post url: %#v", snapshot.QuotedPost)
	}
	if snapshot.QuotedPost.Raw == nil {
		t.Fatalf("expected quoted post placeholder raw payload to be preserved")
	}
	if normalized.APIJSON == hydration.APIJSON {
		t.Fatal("expected normalized API JSON to differ from the original payload")
	}
}

func TestSyncQuotedPostsCreatesFirstClassQuotedItemAndRelationship(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 22, 0, 0, 0, time.UTC)
	parentItem, err := bookmarkRecordToItem(bookmarkRecord{
		ID:           "2040000000000000000",
		TweetID:      "2040000000000000000",
		URL:          "https://x.com/example/status/2040000000000000000",
		Text:         "Parent post text",
		AuthorHandle: "example",
		AuthorName:   "Example",
		PostedAt:     now.Format(time.RFC3339),
		SyncedAt:     now.Format(time.RFC3339),
		Language:     "en",
		Links:        []string{"https://example.com/parent"},
	}, now)
	if err != nil {
		t.Fatalf("bookmarkRecordToItem: %v", err)
	}
	upserted, err := st.UpsertItem(context.Background(), parentItem)
	if err != nil {
		t.Fatalf("UpsertItem parent: %v", err)
	}

	snapshot := &xpost.Snapshot{
		ID:           "2040000000000000000",
		Text:         "Parent post text",
		Language:     "en",
		AuthorHandle: "example",
		AuthorName:   "Example",
		PostedAt:     now.Format(time.RFC3339),
		URL:          "https://x.com/example/status/2040000000000000000",
		QuotedPost: &xpost.Snapshot{
			ID:           "2030838203549184127",
			Text:         "Quoted child context with its own links and media.",
			Language:     "en",
			AuthorHandle: "quoted",
			AuthorName:   "Quoted Person",
			PostedAt:     now.Add(-time.Hour).Format(time.RFC3339),
			URL:          "https://x.com/quoted/status/2030838203549184127",
			Links:        []string{"https://example.com/quoted"},
			MediaObjects: []xpost.MediaObject{
				{
					Type:        "photo",
					URL:         "https://example.invalid/quoted.jpg",
					ExpandedURL: "https://x.com/quoted/status/2030838203549184127/photo/1",
					Width:       1200,
					Height:      800,
				},
			},
			Raw: map[string]any{
				"article": map[string]any{
					"title":        "Quoted article title",
					"preview_text": "Quoted article preview body.",
					"rest_id":      "2013912039379648512",
				},
			},
		},
	}
	hydration, err := buildSnapshotHydration("ok_graphql", snapshot, nil, now)
	if err != nil {
		t.Fatalf("buildSnapshotHydration: %v", err)
	}
	if _, err := st.SaveXHydration(context.Background(), upserted.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration parent: %v", err)
	}

	parent, err := st.GetItem(context.Background(), parentItem.SourceKey)
	if err != nil {
		t.Fatalf("GetItem parent: %v", err)
	}

	_, changed, rendered, err := syncQuotedPosts(context.Background(), cfg, st, parent, hydration, snapshot, Options{
		Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("syncQuotedPosts: %v", err)
	}
	if !changed {
		t.Fatal("expected quoted post relationship to be written")
	}
	if rendered == 0 {
		t.Fatal("expected quoted child note to be rendered")
	}

	child, err := st.GetItem(context.Background(), "x:2030838203549184127")
	if err != nil {
		t.Fatalf("GetItem quoted child: %v", err)
	}
	if child.SourceType != "x_quote" {
		t.Fatalf("expected x_quote child type, got %q", child.SourceType)
	}
	if child.XPostText != "Quoted child context with its own links and media." {
		t.Fatalf("unexpected child x post text: %q", child.XPostText)
	}
	if child.XPostStatus != "ok_graphql" {
		t.Fatalf("unexpected child x post status: %q", child.XPostStatus)
	}
	if !strings.Contains(child.XPostJSON, "\"rest_id\":\"2013912039379648512\"") {
		t.Fatalf("expected child x post json to retain raw article payload, got %q", child.XPostJSON)
	}
	if !strings.Contains(child.LinksJSON, "https://example.com/quoted") {
		t.Fatalf("expected child links to be preserved, got %q", child.LinksJSON)
	}

	refs, err := st.ListItemMediaRefs(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs child: %v", err)
	}
	if len(refs) != 1 || refs[0].ExpandedURL != "https://x.com/quoted/status/2030838203549184127/photo/1" {
		t.Fatalf("expected quoted child media refs, got %#v", refs)
	}

	childLinks, err := st.ListItemChildLinks(context.Background(), parent.ID, "quoted_post")
	if err != nil {
		t.Fatalf("ListItemChildLinks: %v", err)
	}
	if len(childLinks) != 1 || childLinks[0] != child.ID {
		t.Fatalf("expected parent->child relationship, got %#v want child_id=%d", childLinks, child.ID)
	}

	childNotePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(child.NotePath))
	if _, err := os.Stat(childNotePath); err != nil {
		t.Fatalf("expected quoted child note to exist at %s: %v", childNotePath, err)
	}
}

func TestSyncQuotedPostsDoesNotDowngradeDirectlyHydratedQuotedChild(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 26, 1, 45, 0, 0, time.UTC)
	parentItem, err := bookmarkRecordToItem(bookmarkRecord{
		ID:           "2041012360668750229",
		TweetID:      "2041012360668750229",
		URL:          "https://x.com/example/status/2041012360668750229",
		Text:         "Parent post text",
		AuthorHandle: "example",
		AuthorName:   "Example",
		PostedAt:     now.Format(time.RFC3339),
		SyncedAt:     now.Format(time.RFC3339),
		Language:     "en",
	}, now)
	if err != nil {
		t.Fatalf("bookmarkRecordToItem: %v", err)
	}
	upserted, err := st.UpsertItem(context.Background(), parentItem)
	if err != nil {
		t.Fatalf("UpsertItem parent: %v", err)
	}

	snapshot := &xpost.Snapshot{
		ID:           "2041012360668750229",
		Text:         "Parent post text",
		Language:     "en",
		AuthorHandle: "example",
		AuthorName:   "Example",
		PostedAt:     now.Format(time.RFC3339),
		URL:          "https://x.com/example/status/2041012360668750229",
		QuotedPost: &xpost.Snapshot{
			ID:           "2040464914855100670",
			Text:         "Quoted snapshot preview",
			Language:     "en",
			AuthorHandle: "quoted",
			AuthorName:   "Quoted Person",
			PostedAt:     now.Add(-time.Hour).Format(time.RFC3339),
			URL:          "https://x.com/quoted/status/2040464914855100670",
			Raw: map[string]any{
				"article": map[string]any{
					"title":        "Quoted article",
					"preview_text": "Quoted preview",
					"rest_id":      "2040440595642994688",
				},
			},
		},
	}
	hydration, err := buildSnapshotHydration("ok_graphql", snapshot, nil, now)
	if err != nil {
		t.Fatalf("buildSnapshotHydration: %v", err)
	}
	if _, err := st.SaveXHydration(context.Background(), upserted.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration parent: %v", err)
	}

	parent, err := st.GetItem(context.Background(), parentItem.SourceKey)
	if err != nil {
		t.Fatalf("GetItem parent: %v", err)
	}
	if _, _, _, err := syncQuotedPosts(context.Background(), cfg, st, parent, hydration, snapshot, Options{Timeout: 100 * time.Millisecond}); err != nil {
		t.Fatalf("syncQuotedPosts initial: %v", err)
	}

	child, err := st.GetItem(context.Background(), "x:2040464914855100670")
	if err != nil {
		t.Fatalf("GetItem child: %v", err)
	}
	directHydration := model.XHydration{
		FullText:  "Direct child full text",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now.Add(5 * time.Minute),
		APIJSON: `{
			"source":"graphql",
			"fetched_at":"2026-04-26T01:50:00Z",
			"snapshot":{"id":"2040464914855100670","text":"Direct child full text"},
			"raw":{"data":{"tweetResult":{"result":{"rest_id":"2040464914855100670","legacy":{"full_text":"Direct child full text"}}}}}
		}`,
	}
	if _, err := st.SaveXHydration(context.Background(), child.ID, directHydration); err != nil {
		t.Fatalf("SaveXHydration child direct: %v", err)
	}

	if _, _, _, err := syncQuotedPosts(context.Background(), cfg, st, parent, hydration, snapshot, Options{Timeout: 100 * time.Millisecond}); err != nil {
		t.Fatalf("syncQuotedPosts repeat: %v", err)
	}

	refreshed, err := st.GetItem(context.Background(), "x:2040464914855100670")
	if err != nil {
		t.Fatalf("GetItem refreshed child: %v", err)
	}
	if refreshed.XPostText != "Direct child full text" {
		t.Fatalf("expected direct child text to be preserved, got %q", refreshed.XPostText)
	}
	if !strings.Contains(refreshed.XPostJSON, `"tweetResult"`) {
		t.Fatalf("expected direct child graphql payload to be preserved, got %q", refreshed.XPostJSON)
	}
}

func TestSyncQuotedPostsUsesPreservedDirectHydrationForNestedLinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 26, 1, 45, 0, 0, time.UTC)
	parentItem, err := bookmarkRecordToItem(bookmarkRecord{
		ID:           "2041012360668750229",
		TweetID:      "2041012360668750229",
		URL:          "https://x.com/example/status/2041012360668750229",
		Text:         "Parent post text",
		AuthorHandle: "example",
		AuthorName:   "Example",
		PostedAt:     now.Format(time.RFC3339),
		SyncedAt:     now.Format(time.RFC3339),
		Language:     "en",
	}, now)
	if err != nil {
		t.Fatalf("bookmarkRecordToItem: %v", err)
	}
	upserted, err := st.UpsertItem(context.Background(), parentItem)
	if err != nil {
		t.Fatalf("UpsertItem parent: %v", err)
	}

	parentSnapshot := &xpost.Snapshot{
		ID:           "2041012360668750229",
		Text:         "Parent post text",
		Language:     "en",
		AuthorHandle: "example",
		AuthorName:   "Example",
		PostedAt:     now.Format(time.RFC3339),
		URL:          "https://x.com/example/status/2041012360668750229",
		QuotedPost: &xpost.Snapshot{
			ID:           "2040464914855100670",
			Text:         "Quoted snapshot preview",
			Language:     "en",
			AuthorHandle: "quoted",
			AuthorName:   "Quoted Person",
			PostedAt:     now.Add(-time.Hour).Format(time.RFC3339),
			URL:          "https://x.com/quoted/status/2040464914855100670",
		},
	}
	parentHydration, err := buildSnapshotHydration("ok_graphql", parentSnapshot, nil, now)
	if err != nil {
		t.Fatalf("buildSnapshotHydration parent: %v", err)
	}
	if _, err := st.SaveXHydration(context.Background(), upserted.ItemID, parentHydration); err != nil {
		t.Fatalf("SaveXHydration parent: %v", err)
	}

	parent, err := st.GetItem(context.Background(), parentItem.SourceKey)
	if err != nil {
		t.Fatalf("GetItem parent: %v", err)
	}
	if _, _, _, err := syncQuotedPosts(context.Background(), cfg, st, parent, parentHydration, parentSnapshot, Options{Timeout: 100 * time.Millisecond}); err != nil {
		t.Fatalf("syncQuotedPosts initial: %v", err)
	}

	child, err := st.GetItem(context.Background(), "x:2040464914855100670")
	if err != nil {
		t.Fatalf("GetItem child: %v", err)
	}
	directHydration := model.XHydration{
		FullText:  "Direct child full text",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now.Add(5 * time.Minute),
		APIJSON: `{
			"source":"graphql",
			"fetched_at":"2026-04-26T01:50:00Z",
			"snapshot":{
				"id":"2040464914855100670",
				"text":"Direct child full text",
				"quoted_post":{
					"id":"2040448463540830705",
					"text":"Nested quoted post",
					"url":"https://x.com/quoted/status/2040448463540830705"
				}
			},
			"raw":{
				"data":{
					"tweetResult":{
						"result":{
							"rest_id":"2040464914855100670",
							"legacy":{
								"full_text":"Direct child full text",
								"quoted_status_id_str":"2040448463540830705",
								"quoted_status_permalink":{
									"expanded":"https://x.com/quoted/status/2040448463540830705"
								}
							}
						}
					}
				}
			}
		}`,
	}
	if _, err := st.SaveXHydration(context.Background(), child.ID, directHydration); err != nil {
		t.Fatalf("SaveXHydration child direct: %v", err)
	}

	if _, _, _, err := syncQuotedPosts(context.Background(), cfg, st, parent, parentHydration, parentSnapshot, Options{Timeout: 100 * time.Millisecond}); err != nil {
		t.Fatalf("syncQuotedPosts repeat: %v", err)
	}

	refreshedChild, err := st.GetItem(context.Background(), "x:2040464914855100670")
	if err != nil {
		t.Fatalf("GetItem refreshed child: %v", err)
	}
	childLinks, err := st.ListItemChildLinks(context.Background(), refreshedChild.ID, "quoted_post")
	if err != nil {
		t.Fatalf("ListItemChildLinks child: %v", err)
	}
	if len(childLinks) != 1 {
		t.Fatalf("expected direct child hydration to materialize nested quote link, got %#v", childLinks)
	}
	grandchild, err := st.GetItem(context.Background(), "x:2040448463540830705")
	if err != nil {
		t.Fatalf("GetItem grandchild: %v", err)
	}
	if childLinks[0] != grandchild.ID {
		t.Fatalf("expected child->grandchild relationship, got %#v want %d", childLinks, grandchild.ID)
	}
}
