package mastodonapi

import (
	"fmt"
	"net/url"
	"strings"
)

const maxBookmarksCursorLength = 4096

// ParseNextBookmarksCursor extracts and validates only the server-provided
// rel=next URL. The URL is returned byte-for-byte after validation; callers
// must treat its query values as opaque.
func ParseNextBookmarksCursor(header, origin string) (string, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", nil
	}
	canonical, err := canonicalOrigin(origin)
	if err != nil {
		return "", fmt.Errorf("validate bookmarks origin: %w", err)
	}
	for _, part := range splitLinkHeader(header) {
		part = strings.TrimSpace(part)
		if part == "" || !strings.HasPrefix(part, "<") {
			return "", fmt.Errorf("malformed Link header entry")
		}
		end := strings.IndexByte(part, '>')
		if end <= 1 {
			return "", fmt.Errorf("malformed Link header URL")
		}
		rawURL := part[1:end]
		params := strings.Split(part[end+1:], ";")
		relNext := false
		for _, param := range params {
			key, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok || !strings.EqualFold(key, "rel") {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), "\"")
			for _, rel := range strings.Fields(value) {
				if strings.EqualFold(rel, "next") {
					relNext = true
				}
			}
		}
		if !relNext {
			continue
		}
		if err := validateBookmarksCursor(rawURL, canonical); err != nil {
			return "", err
		}
		return rawURL, nil
	}
	return "", nil
}

func splitLinkHeader(header string) []string {
	parts := make([]string, 0, 2)
	start := 0
	angle := false
	for index, r := range header {
		switch r {
		case '<':
			angle = true
		case '>':
			angle = false
		case ',':
			if !angle {
				parts = append(parts, header[start:index])
				start = index + 1
			}
		}
	}
	return append(parts, header[start:])
}

func validateBookmarksCursor(raw, origin string) error {
	if len(raw) > maxBookmarksCursorLength {
		return fmt.Errorf("bookmarks cursor exceeds %d bytes", maxBookmarksCursorLength)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery == "" {
		return fmt.Errorf("bookmarks cursor is malformed")
	}
	cursorOrigin, err := canonicalOrigin(parsed.Scheme + "://" + parsed.Host)
	if err != nil || cursorOrigin != origin {
		return fmt.Errorf("bookmarks cursor origin differs from configured origin")
	}
	if parsed.Path != "/api/v1/bookmarks" {
		return fmt.Errorf("bookmarks cursor path must be /api/v1/bookmarks")
	}
	allowed := map[string]bool{"limit": true, "min_id": true, "max_id": true, "since_id": true}
	for key := range parsed.Query() {
		if !allowed[key] {
			return fmt.Errorf("bookmarks cursor query key %q is not allowed", key)
		}
	}
	return nil
}
