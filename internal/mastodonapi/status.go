package mastodonapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"

	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/vault"
)

var ErrUnsupportedStatus = errors.New("unsupported Mastodon status")
var ErrMalformedStatus = errors.New("malformed Mastodon status")

// StatusRecord is the bounded, source-neutral subset of a Mastodon status
// needed by the importer. Keeping the API shape typed also prevents bearer
// credentials or callback state from being copied into raw item evidence.
type StatusRecord struct {
	ID                      string            `json:"id"`
	URI                     string            `json:"uri"`
	URL                     string            `json:"url"`
	Content                 string            `json:"content"`
	CreatedAt               string            `json:"created_at"`
	EditedAt                string            `json:"edited_at,omitempty"`
	SpoilerText             string            `json:"spoiler_text,omitempty"`
	Sensitive               bool              `json:"sensitive,omitempty"`
	Visibility              string            `json:"visibility,omitempty"`
	Language                string            `json:"language,omitempty"`
	Account                 Account           `json:"account"`
	Card                    *Card             `json:"card,omitempty"`
	MediaAttachments        []MediaAttachment `json:"media_attachments,omitempty"`
	Poll                    *Poll             `json:"poll,omitempty"`
	FavouritesCount         int               `json:"favourites_count,omitempty"`
	ReblogsCount            int               `json:"reblogs_count,omitempty"`
	RepliesCount            int               `json:"replies_count,omitempty"`
	BookmarksCount          int               `json:"bookmarks_count,omitempty"`
	Reblog                  *StatusRecord     `json:"reblog,omitempty"`
	Quote                   json.RawMessage   `json:"quote,omitempty"`
	QuotedStatusID          string            `json:"quoted_status_id,omitempty"`
	Application             json.RawMessage   `json:"application,omitempty"`
	RawExtra                map[string]any    `json:"-"`
	mediaAttachmentsPresent bool              `json:"-"`
	rawJSON                 []byte            `json:"-"`
}

type statusRecordAlias StatusRecord

var statusRecordKnownFields = map[string]struct{}{
	"id": {}, "uri": {}, "url": {}, "content": {}, "created_at": {},
	"edited_at": {}, "spoiler_text": {}, "sensitive": {}, "visibility": {},
	"language": {}, "account": {}, "card": {}, "media_attachments": {},
	"poll": {}, "favourites_count": {}, "reblogs_count": {}, "replies_count": {},
	"bookmarks_count": {}, "reblog": {}, "quote": {}, "quoted_status_id": {},
	"application": {},
}

// UnmarshalJSON keeps unknown Mastodon extension fields as bounded raw
// evidence. Mastodon-compatible servers add fields at different cadences;
// dropping them would make reprojection and repair impossible.
func (s *StatusRecord) UnmarshalJSON(data []byte) error {
	var decoded statusRecordAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&fields); err != nil {
		return err
	}
	extra := make(map[string]any)
	for key, raw := range fields {
		if _, known := statusRecordKnownFields[key]; known {
			continue
		}
		valueDecoder := json.NewDecoder(bytes.NewReader(raw))
		valueDecoder.UseNumber()
		var value any
		if err := valueDecoder.Decode(&value); err != nil {
			return fmt.Errorf("decode Mastodon extension field %q: %w", key, err)
		}
		extra[key] = value
	}
	if len(extra) > 0 {
		decoded.RawExtra = extra
	}
	if raw, present := fields["media_attachments"]; present {
		decoded.mediaAttachmentsPresent = !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	}
	decoded.rawJSON = append([]byte(nil), data...)
	*s = StatusRecord(decoded)
	return nil
}

func (s StatusRecord) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(statusRecordAlias(s))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if s.mediaAttachmentsPresent || s.MediaAttachments != nil {
		encoded, err := json.Marshal(s.MediaAttachments)
		if err != nil {
			return nil, fmt.Errorf("marshal Mastodon media attachments: %w", err)
		}
		fields["media_attachments"] = encoded
	}
	for key, value := range s.RawExtra {
		if _, known := fields[key]; known {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal Mastodon extension field %q: %w", key, err)
		}
		fields[key] = encoded
	}
	return json.Marshal(fields)
}

type Account struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Acct        string `json:"acct,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	URL         string `json:"url,omitempty"`
}

type Card struct {
	URL         string `json:"url,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type Poll struct {
	Question string       `json:"question,omitempty"`
	Options  []PollOption `json:"options,omitempty"`
}

type PollOption struct {
	Title string `json:"title,omitempty"`
}

type MediaAttachment struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	URL         string         `json:"url,omitempty"`
	RemoteURL   string         `json:"remote_url,omitempty"`
	PreviewURL  string         `json:"preview_url,omitempty"`
	Description string         `json:"description,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

type StatusProjection struct {
	Item             model.Item
	MediaCandidates  []model.MediaCandidate
	MediaUnavailable bool
	MediaComplete    bool
}

// NormalizeStatus turns one Mastodon status into the existing item/media
// pipeline. Direct bookmarks use mastodon_bookmark; PR B can use
// NormalizeStatusAs for quote/reblog children without duplicating parsing.
func NormalizeStatus(status StatusRecord, origin string, now time.Time) (StatusProjection, error) {
	return NormalizeStatusForAccount(status, origin, status.Account.ID, now)
}

func NormalizeStatusAs(status StatusRecord, origin, sourceType string, now time.Time) (StatusProjection, error) {
	return NormalizeStatusAsForAccount(status, origin, sourceType, status.Account.ID, now)
}

func NormalizeStatusForAccount(status StatusRecord, origin, accountID string, now time.Time) (StatusProjection, error) {
	return NormalizeStatusAsForAccount(status, origin, "mastodon_bookmark", accountID, now)
}

func NormalizeStatusAsForAccount(status StatusRecord, origin, sourceType, accountID string, now time.Time) (StatusProjection, error) {
	origin, err := canonicalOrigin(origin)
	if err != nil {
		return StatusProjection{}, fmt.Errorf("validate Mastodon status origin: %w", err)
	}
	accountID = strings.TrimSpace(accountID)
	if strings.TrimSpace(status.ID) == "" || strings.TrimSpace(status.URI) == "" || strings.TrimSpace(status.Account.ID) == "" || accountID == "" {
		return StatusProjection{}, fmt.Errorf("%w: status ID, URI, and account IDs are required", ErrMalformedStatus)
	}
	publishedAt, timestampErr := strictMastodonTimestamp(status.CreatedAt)
	if timestampErr != nil {
		return StatusProjection{}, fmt.Errorf("%w: created_at: %v", ErrMalformedStatus, timestampErr)
	}
	statusURI, err := validHTTPURL(status.URI)
	if err != nil {
		return StatusProjection{}, fmt.Errorf("%w: status URI: %v", ErrMalformedStatus, err)
	}
	if hasCredentialQuery(statusURI) {
		return StatusProjection{}, fmt.Errorf("%w: status URI contains credential query parameters", ErrMalformedStatus)
	}
	canonicalURL := statusURI
	if candidate, candidateErr := validHTTPURL(status.URL); candidateErr == nil && !hasCredentialQuery(candidate) {
		canonicalURL = candidate
	}

	attachmentURLs := make(map[string]struct{}, len(status.MediaAttachments)*2)
	mediaCandidates := make([]model.MediaCandidate, 0, len(status.MediaAttachments))
	for _, attachment := range status.MediaAttachments {
		for _, raw := range []string{attachment.URL, attachment.RemoteURL, attachment.PreviewURL} {
			if value, valueErr := validHTTPURL(raw); valueErr == nil {
				attachmentURLs[value] = struct{}{}
			}
		}
		candidate, ok := mediaCandidate(attachment)
		if !ok {
			continue
		}
		mediaCandidates = append(mediaCandidates, candidate)
	}

	bodyText, links := normalizeHTMLContent(status.Content, func(raw string) bool {
		return excludeStatusLink(raw, statusURI, canonicalURL, attachmentURLs)
	})
	text := bodyText
	if spoiler := normalizePlainText(status.SpoilerText); spoiler != "" {
		text = joinEvidence(spoiler, text)
	}
	if status.Poll != nil {
		pollText := normalizePoll(*status.Poll)
		if pollText != "" {
			text = joinEvidence(text, pollText)
		}
	}
	if status.Card != nil {
		if cardURL, cardErr := validHTTPURL(status.Card.URL); cardErr == nil && !excludeStatusLink(cardURL, statusURI, canonicalURL, attachmentURLs) {
			links = appendUnique(links, cardURL)
		}
	}
	if len(status.MediaAttachments) == 0 && status.Card == nil && status.Reblog == nil && len(status.Quote) == 0 && strings.TrimSpace(status.QuotedStatusID) == "" && strings.TrimSpace(text) == "" {
		return StatusProjection{}, fmt.Errorf("%w: status has no supported content", ErrUnsupportedStatus)
	}

	authorHandle := strings.TrimSpace(status.Account.Acct)
	if authorHandle == "" {
		authorHandle = strings.TrimSpace(status.Account.Username)
	}
	authorName := normalizePlainText(status.Account.DisplayName)
	if authorName == "" {
		authorName = authorHandle
	}
	title := firstLine(bodyText)
	if title == "" {
		title = firstLine(text)
	}
	if title == "" && status.Card != nil {
		title = normalizePlainText(status.Card.Title)
	}
	if title == "" {
		title = authorName
	}
	if title == "" {
		title = "Mastodon status"
	}

	links = uniqueLinks(links)
	linksJSON, err := json.Marshal(links)
	if err != nil {
		return StatusProjection{}, fmt.Errorf("marshal Mastodon links: %w", err)
	}
	primaryDomain, domains, githubURLs := deriveLinkFields(links)
	now = now.UTC()
	item := model.Item{
		SourceKey:     mastodonSourceKey(origin, accountID, statusURI),
		SourceType:    sourceType,
		ExternalID:    strings.TrimSpace(status.ID),
		CanonicalURL:  canonicalURL,
		Title:         title,
		AuthorHandle:  authorHandle,
		AuthorName:    authorName,
		PublishedAt:   publishedAt,
		SavedAt:       "",
		SyncedAt:      now.Format(time.RFC3339),
		Language:      strings.TrimSpace(status.Language),
		Text:          strings.TrimSpace(text),
		PrimaryDomain: primaryDomain,
		LinksJSON:     string(linksJSON),
		Domains:       strings.Join(domains, ","),
		GitHubURLs:    strings.Join(githubURLs, ","),
		LikeCount:     status.FavouritesCount,
		RepostCount:   status.ReblogsCount,
		ReplyCount:    status.RepliesCount,
		BookmarkCount: status.BookmarksCount,
		NotePath:      vault.NoteRelativePath("mastodon", chooseStatusYear(publishedAt, now), statusIdentity(origin, accountID, statusURI)),
		ImportedAt:    now,
		UpdatedAt:     now,
		LastSeenAt:    now,
	}
	if status.Card != nil && normalizePlainText(status.Card.Description) != "" && item.Text == "" {
		item.Text = normalizePlainText(status.Card.Description)
	}
	rawJSON, err := sanitizedStatusJSON(status)
	if err != nil {
		return StatusProjection{}, fmt.Errorf("marshal sanitized Mastodon status: %w", err)
	}
	if len(rawJSON) > int(maxMastodonAPIResponseBytes) {
		return StatusProjection{}, fmt.Errorf("mastodon status exceeds %d bytes", maxMastodonAPIResponseBytes)
	}
	item.RawJSON = string(rawJSON)
	item.ContentHash = itemhash.Compute(item)
	mediaAttachmentsPresent := status.mediaAttachmentsPresent || status.MediaAttachments != nil
	return StatusProjection{Item: item, MediaCandidates: mediaCandidates, MediaUnavailable: mediaAttachmentsPresent && len(status.MediaAttachments) > len(mediaCandidates), MediaComplete: mediaAttachmentsPresent && len(status.MediaAttachments) == len(mediaCandidates)}, nil
}

func sanitizedStatusJSON(status StatusRecord) ([]byte, error) {
	raw := status.rawJSON
	if len(raw) == 0 {
		var err error
		raw, err = json.Marshal(status)
		if err != nil {
			return nil, err
		}
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	redactMastodonJSON(value)
	return json.Marshal(value)
}

func strictMastodonTimestamp(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("timestamp is empty")
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC().Format(time.RFC3339Nano), nil
		}
	}
	return "", fmt.Errorf("timestamp %q is not parseable", value)
}

func redactMastodonJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if isSensitiveMastodonKey(key) {
				delete(typed, key)
				continue
			}
			typed[key] = redactMastodonJSON(nested)
		}
	case []any:
		for index, nested := range typed {
			typed[index] = redactMastodonJSON(nested)
		}
	case string:
		return redactMastodonURLQuery(typed)
	}
	return value
}

func redactMastodonURLQuery(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return value
	}
	query := parsed.Query()
	changed := false
	for key := range query {
		if isSensitiveMastodonQueryKey(key) {
			delete(query, key)
			changed = true
		}
	}
	if !changed {
		return value
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func mastodonSourceKey(origin, accountID, statusURI string) string {
	encoded := base64URL([]byte(statusURI))
	return "mastodon:" + origin + ":account:" + strings.TrimSpace(accountID) + ":uri:" + encoded
}

func base64URL(value []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var out strings.Builder
	for len(value) >= 3 {
		triple := uint(value[0])<<16 | uint(value[1])<<8 | uint(value[2])
		out.WriteByte(alphabet[(triple>>18)&63])
		out.WriteByte(alphabet[(triple>>12)&63])
		out.WriteByte(alphabet[(triple>>6)&63])
		out.WriteByte(alphabet[triple&63])
		value = value[3:]
	}
	if len(value) == 1 {
		triple := uint(value[0]) << 16
		out.WriteByte(alphabet[(triple>>18)&63])
		out.WriteByte(alphabet[(triple>>12)&63])
	} else if len(value) == 2 {
		triple := uint(value[0])<<16 | uint(value[1])<<8
		out.WriteByte(alphabet[(triple>>18)&63])
		out.WriteByte(alphabet[(triple>>12)&63])
		out.WriteByte(alphabet[(triple>>6)&63])
	}
	return out.String()
}

func statusIdentity(origin, accountID, statusURI string) string {
	sum := sha256.Sum256([]byte(origin + "\x00" + accountID + "\x00" + statusURI))
	return hex.EncodeToString(sum[:])
}

func chooseStatusYear(publishedAt string, now time.Time) string {
	if publishedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, publishedAt); err == nil {
			return fmt.Sprintf("%04d", parsed.UTC().Year())
		}
	}
	return fmt.Sprintf("%04d", now.UTC().Year())
}

func validHTTPURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("URL is empty")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", errors.New("URL must be HTTP(S) without credentials")
	}
	return parsed.String(), nil
}

func excludeStatusLink(raw, statusURI, canonicalURL string, attachmentURLs map[string]struct{}) bool {
	value, err := validHTTPURL(raw)
	if err != nil {
		return true
	}
	if value == statusURI || value == canonicalURL {
		return true
	}
	if _, ok := attachmentURLs[value]; ok {
		return true
	}
	if hasCredentialQuery(value) {
		return true
	}
	return false
}

func normalizeHTMLContent(raw string, exclude func(string) bool) (string, []string) {
	root, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return normalizePlainText(raw), nil
	}
	var body strings.Builder
	links := make([]string, 0)
	seen := map[string]struct{}{}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "noscript" || node.Data == "template") {
			return
		}
		if node.Type == html.ElementNode {
			if node.Data == "a" {
				for _, attr := range node.Attr {
					if attr.Key == "href" && (exclude(attr.Val) || func() bool {
						_, err := validHTTPURL(attr.Val)
						return err != nil
					}()) {
						// The destination is not safe for discovery, but the
						// anchor's visible text is still source evidence.
						break
					}
				}
			}
			switch node.Data {
			case "br":
				body.WriteByte('\n')
			case "p", "div", "li", "blockquote", "pre", "section", "article", "h1", "h2", "h3", "h4", "h5", "h6":
				if body.Len() > 0 {
					body.WriteByte('\n')
				}
			}
			if node.Data == "a" {
				for _, attr := range node.Attr {
					if attr.Key == "href" && !exclude(attr.Val) {
						if value, err := validHTTPURL(attr.Val); err == nil {
							if _, ok := seen[value]; !ok {
								seen[value] = struct{}{}
								links = append(links, value)
							}
						}
					}
				}
			}
		}
		if node.Type == html.TextNode {
			body.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if node.Type == html.ElementNode && (node.Data == "p" || node.Data == "div" || node.Data == "li" || node.Data == "blockquote" || node.Data == "pre" || node.Data == "section" || node.Data == "article" || strings.HasPrefix(node.Data, "h")) {
			body.WriteByte('\n')
		}
	}
	walk(root)
	return normalizePlainText(body.String()), links
}

func normalizePlainText(value string) string {
	value = strings.ReplaceAll(value, "\u00a0", " ")
	lines := strings.Split(value, "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return ' '
			}
			return r
		}, line)
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			clean = append(clean, line)
		} else if len(clean) > 0 && clean[len(clean)-1] != "" {
			clean = append(clean, "")
		}
	}
	return strings.TrimSpace(strings.Join(clean, "\n"))
}

func normalizePoll(poll Poll) string {
	parts := make([]string, 0, len(poll.Options)+1)
	if question := normalizePlainText(poll.Question); question != "" {
		parts = append(parts, "Poll: "+question)
	}
	for _, option := range poll.Options {
		if title := normalizePlainText(option.Title); title != "" {
			parts = append(parts, "- "+title)
		}
	}
	return strings.Join(parts, "\n")
}

func joinEvidence(left, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	switch {
	case left == "":
		return right
	case right == "":
		return left
	default:
		return left + "\n" + right
	}
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	runes := []rune(value)
	if len(runes) > 120 {
		return strings.TrimSpace(string(runes[:119])) + "…"
	}
	return value
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func uniqueLinks(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func deriveLinkFields(links []string) (string, []string, []string) {
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
	sort.Strings(githubURLs)
	return primary, domains, githubURLs
}

func mediaCandidate(attachment MediaAttachment) (model.MediaCandidate, bool) {
	remote := ""
	for _, candidate := range []string{attachment.URL, attachment.RemoteURL} {
		if value, err := validHTTPSURL(candidate); err == nil && !hasCredentialQuery(value) {
			remote = value
			break
		}
	}
	if remote == "" {
		return model.MediaCandidate{}, false
	}
	mediaType := strings.ToLower(strings.TrimSpace(attachment.Type))
	switch mediaType {
	case "image":
		mediaType = "photo"
	case "video":
		mediaType = "video"
	case "gifv":
		mediaType = "animated_gif"
	case "audio":
		mediaType = "audio"
	default:
		return model.MediaCandidate{}, false
	}
	width, height := attachmentDimensions(attachment.Meta)
	return model.MediaCandidate{RemoteURL: remote, ExpandedURL: remote, MediaType: mediaType, Width: width, Height: height}, true
}

func validHTTPSURL(raw string) (string, error) {
	value, err := validHTTPURL(raw)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(value)
	if err != nil || strings.ToLower(parsed.Scheme) != "https" {
		return "", errors.New("URL must use HTTPS")
	}
	return value, nil
}

func hasCredentialQuery(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return true
	}
	for key, values := range parsed.Query() {
		if !isSensitiveMastodonQueryKey(key) {
			continue
		}
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				return true
			}
		}
	}
	return false
}

func isSensitiveMastodonQueryKey(key string) bool {
	return isSensitiveMastodonKey(key)
}

func isSensitiveMastodonKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "-", ""), "_", ""))
	switch normalized {
	case "accesstoken", "bearertoken", "token", "refreshtoken", "signature", "sig", "clientsecret", "clientid", "authorization", "authorizationcode", "pkceverifier", "codeverifier", "password", "apikey", "secret", "secretkey":
		return true
	default:
		return false
	}
}

var (
	mastodonBearerCredentialPattern = regexp.MustCompile(`(?i)\bBearer\s+\S+`)
	mastodonKeyCredentialPattern    = regexp.MustCompile(`(?i)\b(access[_-]?token|bearer[_-]?token|refresh[_-]?token|client[_-]?secret|authorization(?:[_-]?code)?|pkce[_-]?verifier|code[_-]?verifier|api[_-]?key|password|secret[_-]?key|secret|signature|sig|token)(\s*[=:]\s*)(?:"[^"]*"|'[^']*'|[^&\s,;]+)`)
)

func redactMastodonCredentialText(value string) string {
	value = mastodonBearerCredentialPattern.ReplaceAllString(value, "Bearer [redacted]")
	return mastodonKeyCredentialPattern.ReplaceAllString(value, "$1$2[redacted]")
}

func attachmentDimensions(meta map[string]any) (int, int) {
	for _, name := range []string{"original", "small"} {
		value, ok := meta[name].(map[string]any)
		if !ok {
			continue
		}
		return number(value["width"]), number(value["height"])
	}
	return 0, 0
}

func number(value any) int {
	switch value := value.(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	default:
		return 0
	}
}
