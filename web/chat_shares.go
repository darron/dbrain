package web

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/store"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

const (
	localShareOwnerProvider = "local"
	localShareOwnerSubject  = "local"
)

var (
	shareSlugPattern                   = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
	shareMarkdownLinkPattern           = regexp.MustCompile(`\[([^\]\n]{0,240})\]\(([^)\s]+)[^)]*\)`)
	shareMarkdownURLCodeSpanPattern    = regexp.MustCompile("`(\\[[^\\]\\n]{1,240}\\]\\(https?://[^\\s)]+\\))`")
	shareBracketedURLPattern           = regexp.MustCompile(`\[(https?://[^\s<>"'\]]+)\]`)
	shareAngledURLPattern              = regexp.MustCompile(`<((?:https?://)[^\s<>"']+)>`)
	shareURLPattern                    = regexp.MustCompile("https?://[^\\s<>\"'`]+")
	shareURLTrailingBacktickPattern    = regexp.MustCompile("(https?://[^\\s<>\"'`]+)`+")
	shareSourceKeyPattern              = regexp.MustCompile(`\b(?:src:[A-Za-z0-9_:/.-]*[A-Za-z0-9_-]|apple-note:[A-Za-z0-9_-]+:[A-Za-z0-9_-]+|gh-star:[A-Za-z0-9_:/.-]*[A-Za-z0-9_-]|x:[A-Za-z0-9_-]+|youtube:[A-Za-z0-9_-]+|item:[A-Za-z0-9_-]+)\b`)
	shareInternalFieldPattern          = regexp.MustCompile(`(?im)^\s*(?:[-*]\s*)?(?:source[_ ]?key|lookup|item[_ ]?id|source[_ ]?id|note[_ ]?path|db[_ ]?key|filesystem[_ ]?path)\s*[:=].*$`)
	shareLocalPathPattern              = regexp.MustCompile(`(?:/Users|/private|/var|/tmp|/Volumes)/[^\s)\]]+`)
	shareAbsoluteProtectedRoutePattern = regexp.MustCompile("(?i)https?://[^\\s<>\"'`]+/(?:api|media|auth|login|logout)(?:[/?#][^\\s)\\]`]*)?")
	shareRelativeProtectedRoutePattern = regexp.MustCompile(`(?i)(^|[\s([])(/(?:api|media|auth|login|logout)(?:[/?#][^\s)\]]*)?)`)
	shareWhitespacePattern             = regexp.MustCompile(`[ \t]+`)
)

type chatShareOwner struct {
	Provider string
	Subject  string
	Username string
}

func (s *server) handleChatShares(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleChatShareList(w, r)
	case http.MethodPost:
		s.handleChatShareCreate(w, r)
	default:
		writeMethodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *server) handleChatShareCreate(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.chatShareOwner(r)
	if !ok {
		writeMessage(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req ChatShareCreateRequest
	limitedBody := http.MaxBytesReader(w, r.Body, defaultTranscriptBytes)
	if err := json.NewDecoder(limitedBody).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}
	if strings.TrimSpace(req.Turn.Answer) == "" {
		writeMessage(w, http.StatusBadRequest, "completed chat answer is required")
		return
	}
	if status := strings.TrimSpace(req.Turn.Status); status != "" && status != "ready" {
		writeMessage(w, http.StatusBadRequest, "only completed chat answers can be shared")
		return
	}

	input := buildPublicChatShareInput(owner, req.Turn)
	share, err := s.store.SavePublicChatShare(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, chatShareResponse(share))
}

func (s *server) handleChatShareList(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.chatShareOwner(r)
	if !ok {
		writeMessage(w, http.StatusUnauthorized, "authentication required")
		return
	}
	shares, err := s.store.ListPublicChatSharesByOwner(r.Context(), owner.Provider, owner.Subject, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	response := ChatShareListResponse{Shares: make([]ChatShareResponse, 0, len(shares))}
	for _, share := range shares {
		response.Shares = append(response.Shares, chatShareResponse(share))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) handlePublicShare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeMethodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	slug, ok := publicShareSlugFromPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	share, found, err := s.store.GetPublicChatShareBySlug(r.Context(), slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'none'; script-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_ = publicShareTemplate.Execute(w, publicShareTemplateData{
		Title:           fallbackShareTitle(share.Title),
		Summary:         share.Summary,
		Categories:      share.Categories,
		ContentHTML:     renderPublicShareMarkdown(share.SanitizedContent),
		OriginalSources: publicShareOriginalSources(share.OriginalURLs, share.MetadataJSON),
		CreatedAt:       share.CreatedAt.Format("2006-01-02 15:04 MST"),
		Version:         webVersionInfo(),
	})
}

func (s *server) chatShareOwner(r *http.Request) (chatShareOwner, bool) {
	if user, ok := authUserFromContext(r.Context()); ok {
		subject := strings.TrimSpace(user.ID)
		if subject == "" {
			subject = strings.TrimSpace(user.Username)
		}
		if subject == "" {
			return chatShareOwner{}, false
		}
		return chatShareOwner{
			Provider: user.Provider,
			Subject:  subject,
			Username: strings.TrimSpace(user.Username),
		}, true
	}
	if s.auth != nil {
		return chatShareOwner{}, false
	}
	return chatShareOwner{
		Provider: localShareOwnerProvider,
		Subject:  localShareOwnerSubject,
		Username: localShareOwnerSubject,
	}, true
}

func buildPublicChatShareInput(owner chatShareOwner, turn ChatTranscriptTurn) store.PublicChatShareInput {
	keyURLs := sourceKeyURLMap(turn)
	content := sanitizeSharedChatContent(turn.Answer, keyURLs)
	originalURLs := mergeShareURLs(collectOriginalURLs(turn), extractExternalURLs(content))
	summary := summarizeSharedContent(content)
	categories := categorizeSharedContent(content, turn)
	title := shareTitle(turn.Question, summary)
	metadata := publicChatShareMetadata{
		Question: sanitizeSharedChatContent(turn.Question, keyURLs),
		Sources:  publicShareSourceDetails(turn, originalURLs),
	}
	if turn.CreatedAt != "" {
		metadata.ChatCreatedAt = strings.TrimSpace(turn.CreatedAt)
	}
	metadataJSON, _ := json.Marshal(metadata)
	return store.PublicChatShareInput{
		OwnerProvider:    owner.Provider,
		OwnerSubject:     owner.Subject,
		OwnerUsername:    owner.Username,
		Title:            title,
		Summary:          summary,
		Categories:       categories,
		SanitizedContent: content,
		OriginalURLs:     originalURLs,
		MetadataJSON:     string(metadataJSON),
	}
}

func sourceKeyURLMap(turn ChatTranscriptTurn) map[string]string {
	urls := map[string]string{}
	add := func(sourceKey string, rawURL string) {
		sourceKey = strings.TrimSpace(sourceKey)
		if sourceKey == "" {
			return
		}
		if cleanURL, ok := publicExternalURL(rawURL); ok {
			urls[sourceKey] = cleanURL
		}
	}
	for _, evidence := range turn.ResearchPack.Evidence {
		add(evidence.SourceKey, evidence.URL)
	}
	for _, evidence := range turn.ResearchPack.ExactTagEvidence {
		add(evidence.SourceKey, evidence.URL)
	}
	for _, citation := range turn.Citations {
		add(citation.SourceKey, citation.URL)
	}
	return urls
}

type publicChatShareMetadata struct {
	Question      string                      `json:"question,omitempty"`
	ChatCreatedAt string                      `json:"chat_created_at,omitempty"`
	Sources       []publicShareOriginalSource `json:"sources,omitempty"`
}

type publicShareOriginalSource struct {
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Summary string `json:"summary,omitempty"`
	Host    string `json:"host,omitempty"`
}

func collectOriginalURLs(turn ChatTranscriptTurn) []string {
	seen := map[string]struct{}{}
	var urls []string
	add := func(rawURL string) {
		cleanURL, ok := publicExternalURL(rawURL)
		if !ok {
			return
		}
		if _, ok := seen[cleanURL]; ok {
			return
		}
		seen[cleanURL] = struct{}{}
		urls = append(urls, cleanURL)
	}
	for _, evidence := range turn.ResearchPack.Evidence {
		add(evidence.URL)
	}
	for _, evidence := range turn.ResearchPack.ExactTagEvidence {
		add(evidence.URL)
	}
	for _, citation := range turn.Citations {
		add(citation.URL)
	}
	sort.Strings(urls)
	return urls
}

func publicShareSourceDetails(turn ChatTranscriptTurn, originalURLs []string) []publicShareOriginalSource {
	byURL := map[string]publicShareOriginalSource{}
	add := func(rawURL string, title string, summary string) {
		cleanURL, ok := publicExternalURL(rawURL)
		if !ok {
			return
		}
		current := byURL[cleanURL]
		current.URL = cleanURL
		current.Host = publicShareURLHost(cleanURL)
		if current.Title == "" {
			current.Title = publicShareSnippet(title, 140)
		}
		if current.Summary == "" {
			current.Summary = publicShareSnippet(summary, 320)
		}
		byURL[cleanURL] = current
	}
	for _, evidence := range turn.ResearchPack.Evidence {
		add(evidence.URL, evidence.Title, firstNonEmptyString(evidence.Summary, evidence.Excerpt))
	}
	for _, evidence := range turn.ResearchPack.ExactTagEvidence {
		add(evidence.URL, evidence.Title, firstNonEmptyString(evidence.Summary, evidence.Excerpt))
	}
	for _, citation := range turn.Citations {
		add(citation.URL, citation.Title, "")
	}
	ordered := make([]publicShareOriginalSource, 0, len(originalURLs))
	for _, rawURL := range originalURLs {
		cleanURL, ok := publicExternalURL(rawURL)
		if !ok {
			continue
		}
		if source, ok := byURL[cleanURL]; ok {
			ordered = append(ordered, source)
		}
	}
	return ordered
}

func publicShareSnippet(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(sanitizeSharedChatContent(value, nil)), " ")
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}

func publicShareOriginalSources(originalURLs []string, metadataJSON string) []publicShareOriginalSource {
	var metadata publicChatShareMetadata
	_ = json.Unmarshal([]byte(metadataJSON), &metadata)
	byURL := map[string]publicShareOriginalSource{}
	for _, source := range metadata.Sources {
		cleanURL, ok := publicExternalURL(source.URL)
		if !ok {
			continue
		}
		source.URL = cleanURL
		source.Host = publicShareURLHost(cleanURL)
		source.Title = publicShareSnippet(source.Title, 140)
		source.Summary = publicShareSnippet(source.Summary, 320)
		byURL[cleanURL] = source
	}
	seen := map[string]struct{}{}
	var sources []publicShareOriginalSource
	for _, rawURL := range originalURLs {
		cleanURL, ok := publicExternalURL(rawURL)
		if !ok {
			continue
		}
		if _, ok := seen[cleanURL]; ok {
			continue
		}
		seen[cleanURL] = struct{}{}
		source := byURL[cleanURL]
		if source.URL == "" {
			source.URL = cleanURL
		}
		if source.Host == "" {
			source.Host = publicShareURLHost(cleanURL)
		}
		sources = append(sources, source)
	}
	return sources
}

func extractExternalURLs(content string) []string {
	seen := map[string]struct{}{}
	var urls []string
	for _, match := range shareURLPattern.FindAllString(content, -1) {
		cleanURL, ok := publicExternalURL(match)
		if !ok {
			continue
		}
		if _, ok := seen[cleanURL]; ok {
			continue
		}
		seen[cleanURL] = struct{}{}
		urls = append(urls, cleanURL)
	}
	sort.Strings(urls)
	return urls
}

func mergeShareURLs(groups ...[]string) []string {
	seen := map[string]struct{}{}
	var urls []string
	for _, group := range groups {
		for _, rawURL := range group {
			cleanURL, ok := publicExternalURL(rawURL)
			if !ok {
				continue
			}
			if _, ok := seen[cleanURL]; ok {
				continue
			}
			seen[cleanURL] = struct{}{}
			urls = append(urls, cleanURL)
		}
	}
	sort.Strings(urls)
	return urls
}

func sanitizeSharedChatContent(answer string, sourceURLs map[string]string) string {
	text := strings.ReplaceAll(answer, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = shareMarkdownLinkPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := shareMarkdownLinkPattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		label := strings.TrimSpace(parts[1])
		target := strings.TrimSpace(parts[2])
		if cleanURL, ok := publicExternalURL(target); ok {
			if label == "" || shareSourceKeyPattern.MatchString(label) {
				return cleanURL
			}
			return label + " (" + cleanURL + ")"
		}
		if cleanURL := sourceURLs[strings.TrimSpace(target)]; cleanURL != "" {
			if label == "" || shareSourceKeyPattern.MatchString(label) {
				return cleanURL
			}
			return label + " (" + cleanURL + ")"
		}
		return label
	})
	text = shareURLTrailingBacktickPattern.ReplaceAllString(text, "$1")
	text = shareInternalFieldPattern.ReplaceAllString(text, "")
	text = redactProtectedShareRoutes(text)
	text = shareLocalPathPattern.ReplaceAllString(text, "[redacted path]")
	text = shareSourceKeyPattern.ReplaceAllStringFunc(text, func(key string) string {
		if cleanURL := sourceURLs[strings.TrimSpace(key)]; cleanURL != "" {
			return cleanURL
		}
		return "source"
	})
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(shareWhitespacePattern.ReplaceAllString(line, " "), " ")
	}
	text = strings.TrimSpace(strings.Join(lines, "\n"))
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return text
}

func summarizeSharedContent(content string) string {
	plain := strings.Join(strings.Fields(content), " ")
	if plain == "" {
		return "Shared dbrain chat answer."
	}
	runes := []rune(plain)
	if len(runes) <= 320 {
		return plain
	}
	cut := 320
	for i := 180; i < min(len(runes), 320); i++ {
		switch runes[i] {
		case '.', '!', '?':
			cut = i + 1
		}
	}
	return strings.TrimSpace(string(runes[:cut])) + "..."
}

func categorizeSharedContent(content string, turn ChatTranscriptTurn) []string {
	lower := strings.ToLower(content + " " + turn.Question)
	score := map[string]int{}
	keywords := map[string][]string{
		"ai":             {"agent", "model", "llm", "prompt", "inference", "retrieval", "embedding"},
		"software":       {"code", "api", "database", "sqlite", "server", "github", "deploy", "bug", "test"},
		"infrastructure": {"tailscale", "kubernetes", "docker", "cloudflare", "s3", "r2", "oauth", "auth"},
		"media":          {"video", "audio", "ocr", "transcript", "image", "youtube"},
		"research":       {"evidence", "source", "citation", "summary", "study", "article"},
		"security":       {"token", "secret", "vulnerability", "exploit", "malware", "phishing"},
	}
	for category, terms := range keywords {
		for _, term := range terms {
			if strings.Contains(lower, term) {
				score[category]++
			}
		}
	}
	for _, evidence := range turn.ResearchPack.Evidence {
		switch strings.TrimSpace(evidence.SourceType) {
		case "github_star", "github":
			score["software"]++
		case "youtube_watch_later", "youtube_liked", "youtube":
			score["media"]++
		case "web", "feed_entry":
			score["research"]++
		}
	}
	type ranked struct {
		category string
		score    int
	}
	var rankings []ranked
	for category, value := range score {
		if value > 0 {
			rankings = append(rankings, ranked{category: category, score: value})
		}
	}
	sort.Slice(rankings, func(i, j int) bool {
		if rankings[i].score == rankings[j].score {
			return rankings[i].category < rankings[j].category
		}
		return rankings[i].score > rankings[j].score
	})
	categories := make([]string, 0, min(3, len(rankings)))
	for _, ranking := range rankings {
		categories = append(categories, ranking.category)
		if len(categories) == 3 {
			break
		}
	}
	if len(categories) == 0 {
		categories = []string{"general"}
	}
	return categories
}

func shareTitle(question string, summary string) string {
	title := strings.Join(strings.Fields(sanitizeSharedChatContent(question, nil)), " ")
	if title == "" {
		title = summary
	}
	runes := []rune(title)
	if len(runes) > 90 {
		return strings.TrimSpace(string(runes[:90])) + "..."
	}
	return title
}

func publicExternalURL(raw string) (string, bool) {
	raw = strings.TrimSpace(strings.TrimRight(raw, "`.,);]:"))
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return "", false
	}
	if protectedShareRoutePath(u.EscapedPath()) {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		return "", false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() {
			return "", false
		}
	}
	u.Path = strings.TrimRight(u.Path, "`.,);]:")
	u.RawPath = ""
	u.RawQuery = trimPublicURLComponentRight(u.RawQuery)
	u.Fragment = ""
	return u.String(), true
}

func publicShareURLHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host == "" {
		return raw
	}
	return host
}

func trimPublicURLComponentRight(value string) string {
	value = strings.TrimRight(value, "`.,);]:")
	for strings.HasSuffix(strings.ToLower(value), "%60") {
		value = strings.TrimSuffix(value[:len(value)-3], "`.,);]:")
	}
	return value
}

func redactProtectedShareRoutes(text string) string {
	text = shareAbsoluteProtectedRoutePattern.ReplaceAllString(text, "[redacted internal route]")
	return shareRelativeProtectedRoutePattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := shareRelativeProtectedRoutePattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return "[redacted internal route]"
		}
		return parts[1] + "[redacted internal route]"
	})
}

func protectedShareRoutePath(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	for _, prefix := range []string{"/api", "/media", "/auth", "/login", "/logout"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func publicShareSlugFromPath(path string) (string, bool) {
	slug := strings.TrimPrefix(path, "/share/")
	if slug == path || slug == "" || strings.Contains(slug, "/") || !shareSlugPattern.MatchString(slug) {
		return "", false
	}
	return slug, true
}

func chatShareResponse(share store.PublicChatShare) ChatShareResponse {
	return ChatShareResponse{
		Slug:         share.Slug,
		URL:          "/share/" + share.Slug,
		Title:        fallbackShareTitle(share.Title),
		Summary:      share.Summary,
		Categories:   share.Categories,
		OriginalURLs: share.OriginalURLs,
		CreatedAt:    formatShareTime(share.CreatedAt),
		UpdatedAt:    formatShareTime(share.UpdatedAt),
	}
}

func fallbackShareTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "Shared dbrain answer"
	}
	return title
}

func formatShareTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

type publicShareTemplateData struct {
	Title           string
	Summary         string
	Categories      []string
	ContentHTML     template.HTML
	OriginalSources []publicShareOriginalSource
	CreatedAt       string
	Version         WebVersionInfo
}

var publicShareMarkdown = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
	),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(
		html.WithHardWraps(),
		html.WithXHTML(),
	),
)

func renderPublicShareMarkdown(markdown string) template.HTML {
	markdown = linkPublicShareURLs(markdown)
	var buf bytes.Buffer
	if err := publicShareMarkdown.Convert([]byte(markdown), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(markdown))
	}
	return template.HTML(buf.String())
}

func linkPublicShareURLs(markdown string) string {
	markdown = shareBracketedURLPattern.ReplaceAllStringFunc(markdown, func(match string) string {
		parts := shareBracketedURLPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		return publicShareMarkdownURLLink(parts[1])
	})
	markdown = shareAngledURLPattern.ReplaceAllStringFunc(markdown, func(match string) string {
		parts := shareAngledURLPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		return publicShareMarkdownURLLink(parts[1])
	})
	markdown = linkBarePublicShareURLs(markdown)
	return shareMarkdownURLCodeSpanPattern.ReplaceAllString(markdown, "$1")
}

func linkBarePublicShareURLs(markdown string) string {
	var out strings.Builder
	last := 0
	for _, loc := range shareURLPattern.FindAllStringIndex(markdown, -1) {
		start, end := loc[0], loc[1]
		if start > 0 && (markdown[start-1] == '(' || markdown[start-1] == '[' || markdown[start-1] == '<') {
			continue
		}
		out.WriteString(markdown[last:start])
		out.WriteString(publicShareMarkdownURLLink(markdown[start:end]))
		last = end
	}
	if last == 0 {
		return markdown
	}
	out.WriteString(markdown[last:])
	return out.String()
}

func publicShareMarkdownURLLink(raw string) string {
	cleanURL, ok := publicExternalURL(raw)
	if !ok {
		return raw
	}
	return "[" + publicShareURLHost(cleanURL) + "](" + cleanURL + ")" + publicURLTrailingPunctuation(raw)
}

func publicURLTrailingPunctuation(raw string) string {
	raw = strings.TrimRight(raw, "`")
	idx := len(raw)
	for idx > 0 && strings.ContainsRune(".,);]:", rune(raw[idx-1])) {
		idx--
	}
	return raw[idx:]
}

var publicShareTemplate = template.Must(template.New("public-share").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}} · dbrain share</title>
  <style>
    :root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f7f8f5; color: #18221c; }
    body { margin: 0; min-height: 100vh; background: #f7f8f5; color: #18221c; }
    .shell { width: min(100% - 32px, 860px); margin: 0 auto; padding: 28px 0 36px; }
    header { display: flex; justify-content: space-between; gap: 16px; align-items: baseline; padding-bottom: 18px; border-bottom: 1px solid #d9dfd7; }
    .brand { color: #18633a; font-weight: 800; letter-spacing: 0; }
    .stamp { color: #667466; font-size: 0.9rem; }
    main { display: grid; gap: 18px; padding-top: 22px; }
    h1 { margin: 0; font-size: clamp(1.55rem, 5vw, 2.45rem); line-height: 1.12; letter-spacing: 0; color: #111a14; }
    h2 { margin: 0; font-size: 0.82rem; letter-spacing: 0.12em; text-transform: uppercase; color: #56715f; }
    p { margin: 0; line-height: 1.6; }
    .summary { font-size: 1.04rem; color: #3d4d42; }
    .chips { display: flex; flex-wrap: wrap; gap: 8px; }
    .chip { border: 1px solid #cbd8ce; color: #245c3a; background: #edf5ef; border-radius: 999px; padding: 3px 10px; font-size: 0.82rem; }
    .content { overflow-wrap: anywhere; border-top: 1px solid #d9dfd7; border-bottom: 1px solid #d9dfd7; padding: 18px 0; line-height: 1.68; color: #1e2a22; }
    .content > *:first-child { margin-top: 0; }
    .content > *:last-child { margin-bottom: 0; }
    .content p, .content ul, .content ol, .content blockquote, .content pre { margin: 0 0 1rem; }
    .content h1, .content h2, .content h3, .content h4 { margin: 1.4rem 0 0.55rem; letter-spacing: 0; text-transform: none; color: #111a14; }
    .content h1 { font-size: 1.65rem; }
    .content h2 { font-size: 1.35rem; }
    .content h3 { font-size: 1.15rem; }
    .content h4 { font-size: 1rem; }
    .content ul, .content ol { padding-left: 1.45rem; }
    .content li { margin: 0.25rem 0; }
    .content blockquote { border-left: 3px solid #cbd8ce; padding-left: 1rem; color: #4a5c50; }
    .content code { border: 1px solid #d9dfd7; background: #eef3ed; border-radius: 4px; padding: 0.05rem 0.25rem; font-size: 0.92em; }
    .content pre { border: 1px solid #d9dfd7; background: #eef3ed; border-radius: 8px; padding: 0.85rem; overflow-x: auto; }
    .content pre code { border: 0; background: transparent; padding: 0; }
    .sources { display: grid; gap: 10px; }
    .sources ul { margin: 0; padding-left: 1.2rem; display: grid; gap: 8px; }
    a { color: #0f6b40; overflow-wrap: anywhere; }
    footer { margin-top: 28px; padding-top: 14px; border-top: 1px solid #d9dfd7; color: #758176; font-size: 0.82rem; display: flex; gap: 8px; flex-wrap: wrap; }
    @media (prefers-color-scheme: dark) {
      :root, body { background: #08110c; color: #dce9df; }
      header, .content, footer { border-color: #203328; }
      h1 { color: #f0fbf2; }
      h2, .stamp, footer { color: #8fa393; }
      .summary, .content { color: #c9d8cd; }
      .content h1, .content h2, .content h3, .content h4 { color: #f0fbf2; }
      .content blockquote { border-color: #29553a; color: #afc1b4; }
      .content code, .content pre { border-color: #203328; background: #0e1c14; }
      .brand, a { color: #64d58d; }
      .chip { border-color: #29553a; color: #95e6b0; background: #102317; }
    }
  </style>
</head>
<body>
  <div class="shell">
    <header>
      <div class="brand">dbrain share</div>
      <div class="stamp">{{.CreatedAt}}</div>
    </header>
    <main>
      <h1>{{.Title}}</h1>
      {{if .Categories}}
      <div class="chips" aria-label="Categories">
        {{range .Categories}}<span class="chip">{{.}}</span>{{end}}
      </div>
      {{end}}
      <section class="content" aria-label="Shared answer">{{.ContentHTML}}</section>
      {{if .OriginalSources}}
      <section class="sources" aria-label="Original URLs">
        <h2>Original URLs</h2>
        <ul>
          {{range .OriginalSources}}<li><a href="{{.URL}}" rel="noreferrer noopener" target="_blank">{{.URL}}</a>{{if .Summary}}: {{.Summary}}{{else if .Title}}: {{.Title}}{{end}}</li>{{end}}
        </ul>
      </section>
      {{end}}
    </main>
    <footer>
      <span>dbrain</span>
      {{if .Version.ReleaseVersion}}<span>{{.Version.ReleaseVersion}}</span>{{end}}
      {{if .Version.Short}}<span>{{.Version.Short}}</span>{{end}}
    </footer>
  </div>
</body>
</html>
`))
