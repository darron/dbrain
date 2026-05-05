package applenotes

import "strings"

func extractLinks(value string) []string {
	matches := urlPattern.FindAllString(value, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	links := make([]string, 0, len(matches))
	for _, match := range matches {
		match = strings.TrimRight(match, ".,;:")
		if _, ok := seen[match]; ok {
			continue
		}
		seen[match] = struct{}{}
		links = append(links, match)
	}
	return links
}

func extractAppleNoteTags(value string) []string {
	words := strings.Fields(value)
	tags := make([]string, 0)
	seen := map[string]struct{}{}
	for _, word := range words {
		word = strings.Trim(word, ".,;:!?()[]{}\"'")
		if !strings.HasPrefix(word, "#") || len(word) < 2 {
			continue
		}
		tag := strings.ToLower(strings.TrimPrefix(word, "#"))
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}
	return tags
}

func firstLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func sanitizeIdentity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
