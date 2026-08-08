package bskyapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/vault"
)

var httpURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

var errUnsupportedBookmark = errors.New("unsupported Bluesky bookmark")
var errBlockedBookmark = errors.New("blocked Bluesky bookmark")
var errNotFoundBookmark = errors.New("not-found Bluesky bookmark")

type postView struct {
	URI           string          `json:"uri"`
	CID           string          `json:"cid"`
	Author        postAuthor      `json:"author"`
	Record        json.RawMessage `json:"record"`
	Embed         json.RawMessage `json:"embed,omitempty"`
	IndexedAt     string          `json:"indexedAt"`
	LikeCount     int             `json:"likeCount"`
	RepostCount   int             `json:"repostCount"`
	ReplyCount    int             `json:"replyCount"`
	QuoteCount    int             `json:"quoteCount"`
	BookmarkCount int             `json:"bookmarkCount"`
}

type postAuthor struct {
	DID         string `json:"did"`
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
}

type postRecord struct {
	Text      string          `json:"text"`
	CreatedAt string          `json:"createdAt"`
	Langs     []string        `json:"langs"`
	Facets    []postFacet     `json:"facets"`
	Embed     json.RawMessage `json:"embed,omitempty"`
}

type postFacet struct {
	Features []postFacetFeature `json:"features"`
}

type postFacetFeature struct {
	Type string `json:"$type"`
	URI  string `json:"uri"`
}

type sanitizedBookmarkView struct {
	CreatedAt string          `json:"createdAt,omitempty"`
	Subject   bookmarkSubject `json:"subject"`
	Item      postView        `json:"item"`
}

func bookmarkViewToItem(view bookmarkView, now time.Time) (model.Item, error) {
	projection, err := bookmarkViewToProjection(context.Background(), view, now, nil)
	if err != nil {
		return model.Item{}, err
	}
	return projection.Item, nil
}

func bookmarkViewToProjection(ctx context.Context, view bookmarkView, now time.Time, resolver videoBlobResolver) (bookmarkProjection, error) {
	item, err := bookmarkViewToItemBase(view, now)
	if err != nil {
		return bookmarkProjection{}, err
	}
	var post postView
	if err := json.Unmarshal(view.Item, &post); err != nil {
		return bookmarkProjection{}, fmt.Errorf("decode Bluesky post %s for media: %w", view.Subject.URI, err)
	}
	var record postRecord
	if err := json.Unmarshal(post.Record, &record); err != nil {
		return bookmarkProjection{}, fmt.Errorf("decode Bluesky post record %s for media: %w", view.Subject.URI, err)
	}
	media := decodeBookmarkMedia(ctx, post.Author.DID, item.CanonicalURL, record.Embed, post.Embed, resolver)
	if strings.TrimSpace(record.Text) == "" && !media.supported {
		return bookmarkProjection{}, fmt.Errorf("%w: bookmark has no text or supported embed", errUnsupportedBookmark)
	}
	return bookmarkProjection{Item: item, MediaCandidates: media.candidates, MediaKnown: media.known, MediaUnavailable: media.unavailable}, nil
}

func bookmarkViewToItemBase(view bookmarkView, now time.Time) (model.Item, error) {
	did, rkey, err := parsePostURI(view.Subject.URI)
	if err != nil {
		return model.Item{}, err
	}
	var post postView
	if err := json.Unmarshal(view.Item, &post); err != nil {
		return model.Item{}, fmt.Errorf("decode Bluesky post %s: %w", view.Subject.URI, err)
	}
	var itemType struct {
		Type string `json:"$type"`
	}
	if err := json.Unmarshal(view.Item, &itemType); err == nil {
		switch itemType.Type {
		case "app.bsky.feed.defs#blockedPost":
			return model.Item{}, errBlockedBookmark
		case "app.bsky.feed.defs#notFoundPost":
			return model.Item{}, errNotFoundBookmark
		}
	}
	if post.URI == "" || post.Author.DID == "" || len(post.Record) == 0 || string(post.Record) == "null" {
		return model.Item{}, fmt.Errorf("%w: bookmark does not contain a supported post view", errUnsupportedBookmark)
	}
	if post.URI != view.Subject.URI {
		return model.Item{}, fmt.Errorf("bluesky bookmark subject does not match post URI")
	}

	var record postRecord
	if err := json.Unmarshal(post.Record, &record); err != nil {
		return model.Item{}, fmt.Errorf("decode Bluesky post record %s: %w", view.Subject.URI, err)
	}
	post.Record, err = redactSessionFields(post.Record)
	if err != nil {
		return model.Item{}, fmt.Errorf("sanitize Bluesky post record %s: %w", view.Subject.URI, err)
	}
	if len(post.Embed) > 0 && string(post.Embed) != "null" {
		post.Embed, err = redactSessionFields(post.Embed)
		if err != nil {
			return model.Item{}, fmt.Errorf("sanitize Bluesky post embed %s: %w", view.Subject.URI, err)
		}
	}
	savedAt := normalizeTimestamp(view.CreatedAt)
	publishedAt := normalizeTimestamp(record.CreatedAt)
	syncedAt := normalizeTimestamp(post.IndexedAt)
	now = now.UTC()
	if syncedAt == "" {
		syncedAt = now.Format(time.RFC3339)
	}

	links := collectPostLinks(record, post.Embed)
	linksJSON, err := json.Marshal(links)
	if err != nil {
		return model.Item{}, fmt.Errorf("marshal Bluesky links: %w", err)
	}
	primaryDomain, domains, githubURLs := deriveDomains(links)
	noteID, err := noteIDForPostURI(view.Subject.URI)
	if err != nil {
		return model.Item{}, err
	}
	rawJSON, err := json.Marshal(sanitizedBookmarkView{
		CreatedAt: view.CreatedAt,
		Subject:   view.Subject,
		Item:      post,
	})
	if err != nil {
		return model.Item{}, fmt.Errorf("marshal raw Bluesky bookmark: %w", err)
	}

	authorHandle := strings.TrimSpace(post.Author.Handle)
	authorName := strings.TrimSpace(post.Author.DisplayName)
	if authorName == "" {
		authorName = authorHandle
	}
	title := deriveBookmarkTitle(record.Text, record.Embed, post.Embed, authorHandle, did)
	canonicalURL := fmt.Sprintf("https://bsky.app/profile/%s/post/%s", url.PathEscape(authorHandleOrDID(authorHandle, did)), url.PathEscape(rkey))
	item := model.Item{
		SourceKey:     "bsky:" + strings.TrimSpace(view.Subject.URI),
		SourceType:    "bsky_bookmark",
		ExternalID:    strings.TrimSpace(view.Subject.URI),
		CanonicalURL:  canonicalURL,
		Title:         title,
		AuthorHandle:  authorHandle,
		AuthorName:    authorName,
		PublishedAt:   publishedAt,
		SavedAt:       savedAt,
		SyncedAt:      syncedAt,
		Language:      firstString(record.Langs),
		Text:          record.Text,
		PrimaryDomain: primaryDomain,
		LinksJSON:     string(linksJSON),
		Domains:       strings.Join(domains, ","),
		GitHubURLs:    strings.Join(githubURLs, ","),
		LikeCount:     post.LikeCount,
		RepostCount:   post.RepostCount,
		ReplyCount:    post.ReplyCount,
		QuoteCount:    post.QuoteCount,
		BookmarkCount: post.BookmarkCount,
		NotePath:      vault.NoteRelativePath("bsky", chooseYear(savedAt, publishedAt, syncedAt), noteID),
		RawJSON:       string(rawJSON),
		ImportedAt:    now,
		UpdatedAt:     now,
		LastSeenAt:    now,
	}
	item.ContentHash = itemhash.Compute(item)
	return item, nil
}

func parsePostURI(rawURI string) (string, string, error) {
	rawURI = strings.TrimSpace(rawURI)
	if !strings.HasPrefix(rawURI, "at://") {
		return "", "", errors.New("invalid Bluesky post URI")
	}
	rest := strings.TrimPrefix(rawURI, "at://")
	separator := strings.IndexByte(rest, '/')
	if separator <= 0 {
		return "", "", errors.New("invalid Bluesky post URI")
	}
	did := rest[:separator]
	parts := strings.Split(strings.Trim(rest[separator:], "/"), "/")
	if !strings.HasPrefix(did, "did:") || len(parts) != 2 || parts[0] != "app.bsky.feed.post" || parts[1] == "" {
		return "", "", errors.New("invalid Bluesky post URI")
	}
	return did, parts[1], nil
}

func collectPostLinks(record postRecord, embed json.RawMessage) []string {
	links := make([]string, 0)
	seen := map[string]struct{}{}
	add := func(raw string) {
		raw = strings.Trim(strings.TrimSpace(raw), ".,;:!?)]}")
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
			return
		}
		if _, ok := seen[raw]; ok {
			return
		}
		seen[raw] = struct{}{}
		links = append(links, raw)
	}
	collectRecordLinks(record, add)
	collectEmbeddedLinks(record.Embed, add)
	collectEmbeddedLinks(embed, add)
	return links
}

func deriveDomains(links []string) (string, []string, []string) {
	domains := make([]string, 0, len(links))
	githubURLs := make([]string, 0)
	seen := map[string]struct{}{}
	for _, link := range links {
		parsed, err := url.Parse(link)
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if _, ok := seen[host]; !ok {
			domains = append(domains, host)
			seen[host] = struct{}{}
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

func authorHandleOrDID(handle, did string) string {
	if handle != "" {
		return handle
	}
	return did
}

func deriveTitle(text string) string {
	text = strings.TrimSpace(text)
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = strings.TrimSpace(text[:index])
	}
	runes := []rune(text)
	if len(runes) > 96 {
		return strings.TrimSpace(string(runes[:95])) + "…"
	}
	return text
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func normalizeTimestamp(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, time.RubyDate} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return value
}

func redactSessionFields(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	redactSessionFieldsValue(value)
	return json.Marshal(value)
}

func redactSessionFieldsValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if strings.EqualFold(key, "accessJwt") || strings.EqualFold(key, "refreshJwt") {
				delete(typed, key)
				continue
			}
			redactSessionFieldsValue(nested)
		}
	case []any:
		for _, nested := range typed {
			redactSessionFieldsValue(nested)
		}
	}
}

func chooseYear(values ...string) string {
	for _, value := range values {
		if value == "" {
			continue
		}
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed.Format("2006")
		}
	}
	return "unknown"
}
