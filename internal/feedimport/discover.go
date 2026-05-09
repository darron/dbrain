package feedimport

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

type DiscoveryCandidate struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
	Type  string `json:"type,omitempty"`
}

var linkTagPattern = regexp.MustCompile(`(?is)<link\s+[^>]*>`)
var attrPattern = regexp.MustCompile(`(?is)([a-zA-Z_:.-]+)\s*=\s*("[^"]*"|'[^']*')`)

func DiscoverFromHTML(pageURL, htmlBody string) ([]DiscoveryCandidate, error) {
	base, err := url.Parse(strings.TrimSpace(pageURL))
	if err != nil {
		return nil, fmt.Errorf("parse discovery page URL: %w", err)
	}
	var candidates []DiscoveryCandidate
	seen := map[string]struct{}{}
	for _, tag := range linkTagPattern.FindAllString(htmlBody, -1) {
		attrs := parseHTMLAttrs(tag)
		rel := strings.ToLower(attrs["rel"])
		typ := strings.ToLower(attrs["type"])
		if !strings.Contains(rel, "alternate") {
			continue
		}
		if !isFeedMIMEType(typ) {
			continue
		}
		href := strings.TrimSpace(attrs["href"])
		if href == "" {
			continue
		}
		ref, err := url.Parse(href)
		if err != nil {
			continue
		}
		resolved := base.ResolveReference(ref).String()
		normalized, _, err := NormalizeFeedURL(resolved)
		if err != nil {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		candidates = append(candidates, DiscoveryCandidate{
			URL:   normalized,
			Title: strings.TrimSpace(attrs["title"]),
			Type:  typ,
		})
	}
	return candidates, nil
}

func parseHTMLAttrs(tag string) map[string]string {
	out := map[string]string{}
	for _, match := range attrPattern.FindAllStringSubmatch(tag, -1) {
		if len(match) == 3 {
			out[strings.ToLower(match[1])] = htmlEntityTrim(strings.Trim(match[2], `"'`))
		}
	}
	return out
}

func htmlEntityTrim(value string) string {
	value = strings.ReplaceAll(value, "&amp;", "&")
	value = strings.ReplaceAll(value, "&#38;", "&")
	return strings.TrimSpace(value)
}

func isFeedMIMEType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "rss") ||
		strings.Contains(value, "atom") ||
		strings.Contains(value, "feed+json") ||
		strings.Contains(value, "application/feed") ||
		strings.Contains(value, "application/xml") ||
		strings.Contains(value, "text/xml")
}
