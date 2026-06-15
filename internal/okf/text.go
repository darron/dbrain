package okf

import (
	"strings"
	"unicode"
)

const (
	validationSkipBegin = "<!-- dbrain-okf-validate-skip-begin -->"
	validationSkipEnd   = "<!-- dbrain-okf-validate-skip-end -->"
)

func cleanText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func firstSentence(value string) string {
	value = cleanText(value)
	if value == "" {
		return ""
	}
	for i, r := range value {
		if r == '.' || r == '!' || r == '?' {
			return strings.TrimSpace(value[:i+len(string(r))])
		}
	}
	if len([]rune(value)) > 180 {
		runes := []rune(value)
		return strings.TrimSpace(string(runes[:180])) + "..."
	}
	return value
}

func splitTags(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizeTag(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "`")
	value = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return '-'
		}
		return r
	}, value)
	return strings.Trim(value, "-")
}

func truncateRaw(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if maxChars <= 0 || len([]rune(value)) <= maxChars {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxChars]) + "\n\n[Truncated by OKF export max raw characters setting.]"
}

func writeSection(b *strings.Builder, heading string, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	b.WriteString("\n# ")
	b.WriteString(heading)
	b.WriteString("\n\n")
	b.WriteString(body)
	b.WriteString("\n")
}

func writeUnvalidatedSection(b *strings.Builder, heading string, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	b.WriteString("\n# ")
	b.WriteString(heading)
	b.WriteString("\n\n")
	b.WriteString(validationSkipBegin)
	b.WriteString("\n")
	b.WriteString(body)
	b.WriteString("\n")
	b.WriteString(validationSkipEnd)
	b.WriteString("\n")
}

func writeBullet(b *strings.Builder, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString("- ")
	b.WriteString(label)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\n")
}

func code(value string) string {
	return "`" + strings.ReplaceAll(strings.TrimSpace(value), "`", "'") + "`"
}
