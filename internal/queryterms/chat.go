package queryterms

import "strings"

// SearchText returns the portion of a retrieval prompt that should be treated
// as primary searchable text.
func SearchText(question string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return ""
	}
	return strings.TrimSpace(chatSearchText(question))
}

func chatSearchText(question string) string {
	question = normalizeChatRetrievalHeadings(question)
	lines := strings.Split(question, "\n")
	if !hasChatRetrievalHeading(lines) {
		return question
	}

	var parts []string
	section := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		switch {
		case strings.HasPrefix(lower, "current question:"):
			section = "search"
			if value := strings.TrimSpace(trimmed[len("current question:"):]); value != "" {
				parts = append(parts, value)
			}
			continue
		case strings.HasPrefix(lower, "recent user questions:"):
			section = "search"
			if value := strings.TrimSpace(trimmed[len("recent user questions:"):]); value != "" {
				parts = append(parts, strings.TrimSpace(strings.TrimPrefix(value, "-")))
			}
			continue
		case isPriorEvidenceHeading(lower):
			section = "ignore"
			continue
		case looksLikeChatHeading(lower):
			section = "ignore"
			continue
		}
		if section != "search" {
			continue
		}
		parts = append(parts, strings.TrimSpace(strings.TrimPrefix(trimmed, "-")))
	}
	if len(parts) == 0 {
		return question
	}
	return strings.Join(parts, " ")
}

func normalizeChatRetrievalHeadings(question string) string {
	question = strings.NewReplacer(`\n`, "\n", `\r`, "\n", `\t`, " ").Replace(question)
	for _, heading := range []string{
		"Current question:",
		"Recent user questions:",
		"Prior evidence titles for query focus:",
		"Prior evidence metadata for query expansion:",
		"Relevant prior evidence source keys:",
		"Pinned evidence keys:",
	} {
		question = breakBeforeHeading(question, heading)
	}
	return question
}

func breakBeforeHeading(value string, heading string) string {
	lower := strings.ToLower(value)
	needle := strings.ToLower(heading)
	if !strings.Contains(lower, needle) {
		return value
	}

	var b strings.Builder
	start := 0
	for {
		idx := strings.Index(lower[start:], needle)
		if idx < 0 {
			b.WriteString(value[start:])
			break
		}
		pos := start + idx
		b.WriteString(value[start:pos])
		if pos > 0 && value[pos-1] != '\n' {
			b.WriteByte('\n')
		}
		end := pos + len(heading)
		b.WriteString(value[pos:end])
		start = end
	}
	return b.String()
}

func hasChatRetrievalHeading(lines []string) bool {
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(lower, "current question:") || strings.HasPrefix(lower, "recent user questions:") || isPriorEvidenceHeading(lower) {
			return true
		}
	}
	return false
}

func isPriorEvidenceHeading(lower string) bool {
	return strings.HasPrefix(lower, "prior evidence ") ||
		strings.HasPrefix(lower, "relevant prior evidence ") ||
		strings.HasPrefix(lower, "pinned evidence ")
}

func looksLikeChatHeading(lower string) bool {
	return strings.HasSuffix(lower, ":") && (strings.Contains(lower, "evidence") || strings.Contains(lower, "question"))
}
