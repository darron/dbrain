package sourceenrich

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func looksLikeNonTextExtractContent(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	if strings.ContainsRune(content, '\x00') {
		return true
	}
	if !utf8.ValidString(content) {
		return true
	}

	runes := 0
	replacementRunes := 0
	controlRunes := 0
	for _, r := range content {
		runes++
		if r == utf8.RuneError {
			replacementRunes++
			continue
		}
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			controlRunes++
		}
	}
	if runes == 0 {
		return false
	}
	if replacementRunes >= 3 && replacementRunes*20 >= runes {
		return true
	}
	return controlRunes >= 8 && controlRunes*10 >= runes
}

func looksLikePlaceholderExtractContent(content string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(content)), " "))
	if normalized == "" {
		return false
	}
	switch {
	case strings.Contains(normalized, "redirecting"),
		strings.Contains(normalized, "you will be redirected"),
		strings.Contains(normalized, "if you are not redirected automatically"),
		strings.Contains(normalized, "loading..."),
		strings.Contains(normalized, "coming soon"),
		strings.Contains(normalized, "<div></div>"),
		strings.Contains(normalized, "we use cookies to improve user experience"),
		strings.Contains(normalized, "nothing to see here"),
		strings.Contains(normalized, "google drive"),
		strings.Contains(normalized, "your browser does not support frames"),
		strings.Contains(normalized, "click here to enter the site"):
		return len(normalized) <= 300
	case strings.Contains(normalized, "sign in or sign up"),
		strings.Contains(normalized, "you are not logged in"),
		strings.Contains(normalized, "manage account"),
		strings.Contains(normalized, "your profile"),
		strings.Contains(normalized, "continue with google"),
		strings.Contains(normalized, "continue with github"),
		strings.Contains(normalized, "open full screen to view more"),
		strings.Contains(normalized, "google apps"):
		return len(normalized) <= 300
	default:
		return false
	}
}
