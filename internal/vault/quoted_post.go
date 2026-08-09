package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/xpost"
)

type quotedPostPresentation struct {
	Heading      string
	NotePath     string
	URL          string
	AuthorHandle string
	AuthorName   string
	PublishedAt  string
	Links        []string
	Text         string
}

func writeQuotedPostSection(b *strings.Builder, quote quotedPostPresentation) {
	if strings.TrimSpace(quote.Heading) == "" {
		return
	}

	b.WriteString("\n## ")
	b.WriteString(quote.Heading)
	b.WriteString("\n\n")
	if notePath := strings.TrimSpace(quote.NotePath); notePath != "" {
		b.WriteString("- Linked item: [[")
		b.WriteString(notePath)
		b.WriteString("]]\n")
	}
	if url := strings.TrimSpace(quote.URL); url != "" {
		b.WriteString("- URL: ")
		b.WriteString(url)
		b.WriteString("\n")
	}
	if quote.AuthorHandle != "" || quote.AuthorName != "" {
		b.WriteString("- Author: ")
		if quote.AuthorName != "" {
			b.WriteString(quote.AuthorName)
			if quote.AuthorHandle != "" {
				b.WriteString(" ")
			}
		}
		if quote.AuthorHandle != "" {
			b.WriteString("(@")
			b.WriteString(quote.AuthorHandle)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if postedAt := strings.TrimSpace(quote.PublishedAt); postedAt != "" {
		b.WriteString("- Published: ")
		b.WriteString(postedAt)
		b.WriteString("\n")
	}
	if len(quote.Links) > 0 {
		b.WriteString("- Links:\n")
		for _, link := range quote.Links {
			link = strings.TrimSpace(link)
			if link == "" {
				continue
			}
			b.WriteString("  - ")
			b.WriteString(link)
			b.WriteString("\n")
		}
	}
	if text := strings.TrimSpace(quote.Text); text != "" {
		b.WriteString("\n")
		b.WriteString(text)
		b.WriteString("\n")
	}
}

func xQuotedPostPresentation(snapshot *xpost.Snapshot) quotedPostPresentation {
	if snapshot == nil {
		return quotedPostPresentation{}
	}
	return quotedPostPresentation{
		Heading:      "Quoted X Post",
		NotePath:     xpost.NotePath(snapshot),
		URL:          snapshot.URL,
		AuthorHandle: snapshot.AuthorHandle,
		AuthorName:   snapshot.AuthorName,
		PublishedAt:  snapshot.PostedAt,
		Links:        append([]string(nil), snapshot.Links...),
		Text:         snapshot.Text,
	}
}

type storedBskyItem struct {
	URI    string           `json:"uri"`
	Author storedBskyAuthor `json:"author"`
	Record json.RawMessage  `json:"record"`
	Embed  json.RawMessage  `json:"embed"`
}

type storedBskyAuthor struct {
	DID         string `json:"did"`
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
}

type storedBskyBookmark struct {
	Item storedBskyItem `json:"item"`
}

type storedBskyQuoteView struct {
	Type   string            `json:"$type"`
	URI    string            `json:"uri"`
	Author storedBskyAuthor  `json:"author"`
	Value  json.RawMessage   `json:"value"`
	Embeds []json.RawMessage `json:"embeds"`
}

type storedBskyRecord struct {
	Text      string            `json:"text"`
	CreatedAt string            `json:"createdAt"`
	Facets    []storedBskyFacet `json:"facets"`
	Embed     json.RawMessage   `json:"embed"`
}

type storedBskyFacet struct {
	Features []storedBskyFacetFeature `json:"features"`
}

type storedBskyFacetFeature struct {
	Type string `json:"$type"`
	URI  string `json:"uri"`
}

func blueskyQuotePresentation(item model.Item) (quotedPostPresentation, bool) {
	if !strings.HasPrefix(strings.TrimSpace(item.SourceType), "bsky_") || strings.TrimSpace(item.RawJSON) == "" {
		return quotedPostPresentation{}, false
	}
	var stored storedBskyBookmark
	if err := json.Unmarshal([]byte(item.RawJSON), &stored); err != nil {
		return quotedPostPresentation{}, false
	}
	rawEmbed := stored.Item.Embed
	if len(rawEmbed) == 0 || strings.TrimSpace(string(rawEmbed)) == "null" {
		var record storedBskyRecord
		if json.Unmarshal(stored.Item.Record, &record) == nil {
			rawEmbed = record.Embed
		}
	}
	quote := decodeStoredBskyQuote(rawEmbed)
	if quote == nil {
		return quotedPostPresentation{}, false
	}
	var record storedBskyRecord
	if err := json.Unmarshal(quote.Value, &record); err != nil {
		return quotedPostPresentation{}, false
	}
	did, rkey := parseStoredBskyPostURI(quote.URI)
	handle := strings.TrimSpace(quote.Author.Handle)
	if handle == "" {
		handle = did
	}
	authorName := strings.TrimSpace(quote.Author.DisplayName)
	if authorName == "" {
		authorName = handle
	}
	links := make([]string, 0)
	seen := map[string]struct{}{}
	addStoredBskyLink := func(raw string) {
		raw = strings.Trim(strings.TrimSpace(raw), ".,;:!?)]}")
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return
		}
		if _, ok := seen[raw]; ok {
			return
		}
		seen[raw] = struct{}{}
		links = append(links, raw)
	}
	collectStoredBskyRecordLinks(record, addStoredBskyLink)
	collectStoredBskyEmbedLinks(record.Embed, addStoredBskyLink)
	for _, embed := range quote.Embeds {
		collectStoredBskyEmbedLinks(embed, addStoredBskyLink)
	}
	return quotedPostPresentation{
		Heading:      "Quoted Bluesky Post",
		NotePath:     storedBskyNotePath(quote.URI, record.CreatedAt),
		URL:          storedBskyPostURL(handle, did, rkey),
		AuthorHandle: handle,
		AuthorName:   authorName,
		PublishedAt:  normalizeStoredBskyTimestamp(record.CreatedAt),
		Links:        links,
		Text:         record.Text,
	}, true
}

func decodeStoredBskyQuote(raw json.RawMessage) *storedBskyQuoteView {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" || strings.TrimSpace(string(raw)) == "{}" {
		return nil
	}
	var object struct {
		Type   string          `json:"$type"`
		Record json.RawMessage `json:"record"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	if object.Type == "app.bsky.embed.recordWithMedia#view" || object.Type == "app.bsky.embed.recordWithMedia" {
		return decodeStoredBskyQuote(object.Record)
	}
	if object.Type != "app.bsky.embed.record#viewRecord" && object.Type != "app.bsky.embed.record#view" {
		return nil
	}
	var view storedBskyQuoteView
	if err := json.Unmarshal(raw, &view); err != nil || strings.TrimSpace(view.URI) == "" || len(view.Value) == 0 {
		return nil
	}
	return &view
}

func collectStoredBskyRecordLinks(record storedBskyRecord, add func(string)) {
	for _, match := range storedBskyHTTPURLPattern.FindAllString(record.Text, -1) {
		add(match)
	}
	for _, facet := range record.Facets {
		for _, feature := range facet.Features {
			if strings.HasSuffix(feature.Type, "#link") {
				add(feature.URI)
			}
		}
	}
}

func collectStoredBskyEmbedLinks(raw json.RawMessage, add func(string)) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" || strings.TrimSpace(string(raw)) == "{}" {
		return
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return
	}
	if external, ok := object["external"]; ok {
		var card struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(external, &card) == nil {
			add(card.URI)
		}
	}
	typeName := storedBskyRawString(object["$type"])
	if typeName == "app.bsky.embed.recordWithMedia#view" || typeName == "app.bsky.embed.recordWithMedia" {
		collectStoredBskyEmbedLinks(object["media"], add)
		collectStoredBskyEmbedLinks(object["record"], add)
	}
	if typeName == "app.bsky.embed.record#viewRecord" || typeName == "app.bsky.embed.record#view" {
		var view storedBskyQuoteView
		if json.Unmarshal(raw, &view) == nil {
			var record storedBskyRecord
			if json.Unmarshal(view.Value, &record) == nil {
				collectStoredBskyRecordLinks(record, add)
				collectStoredBskyEmbedLinks(record.Embed, add)
			}
			for _, embed := range view.Embeds {
				collectStoredBskyEmbedLinks(embed, add)
			}
		}
	}
}

func storedBskyRawString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	return ""
}

func parseStoredBskyPostURI(raw string) (string, string) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(raw), "at://"), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] != "app.bsky.feed.post" || parts[2] == "" {
		return "", ""
	}
	return parts[0], parts[2]
}

func storedBskyPostURL(handle, did, rkey string) string {
	if strings.TrimSpace(handle) == "" {
		handle = did
	}
	if handle == "" || rkey == "" {
		return ""
	}
	return fmt.Sprintf("https://bsky.app/profile/%s/post/%s", url.PathEscape(handle), url.PathEscape(rkey))
}

var storedBskySafeRKey = regexp.MustCompile(`[^A-Za-z0-9._~-]+`)
var storedBskyHTTPURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

func storedBskyNotePath(uri, publishedAt string) string {
	did, rkey := parseStoredBskyPostURI(uri)
	if did == "" || rkey == "" {
		return ""
	}
	safe := strings.Trim(storedBskySafeRKey.ReplaceAllString(rkey, "-"), "-")
	if safe == "" {
		safe = "post"
	}
	digest := sha256.Sum256([]byte(uri))
	noteID := fmt.Sprintf("%s-%s", safe, hex.EncodeToString(digest[:])[:12])
	return NoteRelativePath("bsky", storedBskyYear(publishedAt), noteID)
}

func storedBskyYear(value string) string {
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, time.RubyDate} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed.Format("2006")
		}
	}
	return "unknown"
}

func normalizeStoredBskyTimestamp(value string) string {
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, time.RubyDate} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return strings.TrimSpace(value)
}
