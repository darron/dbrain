package queryterms

import (
	"strings"
	"unicode"
)

// Terms normalizes a natural-language question into stable retrieval terms.
func Terms(question string) []string {
	stopwords := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "are": {}, "can": {}, "could": {}, "did": {}, "do": {}, "does": {},
		"about": {}, "as": {}, "at": {}, "be": {}, "been": {}, "being": {}, "brain": {}, "by": {}, "context": {}, "current": {}, "data": {}, "dbrain": {}, "evidence": {}, "expansion": {}, "find": {}, "for": {}, "from": {}, "github": {}, "have": {}, "her": {}, "him": {}, "his": {}, "how": {}, "i": {}, "if": {}, "in": {}, "include": {}, "information": {}, "into": {}, "is": {}, "it": {}, "its": {}, "key": {}, "keys": {}, "know": {}, "like": {}, "local": {}, "look": {}, "looks": {}, "me": {}, "metadata": {}, "my": {}, "of": {}, "on": {}, "or": {}, "other": {}, "present": {}, "prior": {}, "query": {}, "question": {}, "questions": {}, "recent": {}, "related": {}, "relevant": {}, "saved": {}, "stories": {}, "story": {}, "that": {}, "the": {}, "their": {}, "them": {}, "there": {}, "these": {}, "this": {}, "those": {},
		"repo": {}, "repos": {}, "repository": {}, "repositories": {},
		"favored": {}, "favoured": {}, "favorite": {}, "favorites": {}, "favourite": {}, "favourites": {},
		"show": {}, "should": {}, "source": {}, "sources": {}, "src": {}, "tag": {}, "tags": {}, "tell": {}, "tweet": {}, "tweets": {},
		"to": {}, "use": {}, "user": {}, "using": {}, "was": {}, "we": {}, "were": {}, "what": {}, "when": {}, "where": {}, "which": {}, "who": {}, "why": {}, "with": {}, "would": {}, "x": {}, "you": {}, "your": {},
	}

	parts := strings.Fields(normalizeQuestionText(SearchText(question)))
	if len(parts) == 0 {
		return nil
	}

	terms := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimFunc(part, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		})
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		part = canonicalTerm(part)
		if _, skip := stopwords[part]; skip {
			continue
		}
		if looksLikeSourceKeyFragment(part) {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		terms = append(terms, part)
	}
	if len(terms) == 0 {
		return strings.Fields(strings.ToLower(strings.TrimSpace(question)))
	}
	return terms
}

// SearchText returns the portion of a retrieval prompt that should be treated
// as primary searchable text.
func SearchText(question string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return ""
	}
	return strings.TrimSpace(chatSearchText(question))
}

func canonicalTerm(term string) string {
	switch term {
	case "models":
		return "model"
	case "kid", "kids":
		return "children"
	case "child":
		return "children"
	case "killed", "killing", "kills":
		return "kill"
	case "charged", "charges", "charging":
		return "charge"
	}
	return term
}

func normalizeQuestionText(question string) string {
	question = stripCorpusFramePhrases(question)
	question = strings.NewReplacer(`\n`, " ", `\r`, " ", `\t`, " ", "-", " ", "_", " ").Replace(strings.TrimSpace(question))
	var b strings.Builder
	b.Grow(len(question))
	lastSpace := false
	for _, r := range question {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return b.String()
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

func stripCorpusFramePhrases(question string) string {
	question = strings.ToLower(question)
	for _, phrase := range []string{
		"in my research",
		"from my research",
		"my research",
		"in your research",
		"from your research",
		"your research",
		"in the research",
		"from the research",
	} {
		question = strings.ReplaceAll(question, phrase, " ")
	}
	return question
}

func looksLikeSourceKeyFragment(term string) bool {
	if len(term) < 8 {
		return false
	}
	allDigits := true
	allHex := true
	for _, r := range term {
		if !unicode.IsDigit(r) {
			allDigits = false
		}
		if !unicode.IsDigit(r) && (r < 'a' || r > 'f') {
			allHex = false
		}
	}
	return allDigits || (allHex && len(term) >= 10)
}

// TagQueries returns hyphenated tag aliases that match dbrain user_tags.
func TagQueries(terms []string) []string {
	if len(terms) < 2 {
		return nil
	}
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" || len([]rune(term)) < 3 {
			continue
		}
		parts = append(parts, term)
	}
	if len(parts) < 2 {
		return nil
	}
	seen := map[string]struct{}{}
	var queries []string
	add := func(query string) {
		if query == "" {
			return
		}
		if _, ok := seen[query]; ok {
			return
		}
		seen[query] = struct{}{}
		queries = append(queries, query)
	}
	add(strings.Join(parts, "-"))
	if len(parts) > 2 {
		for i := 0; i < len(parts)-1; i++ {
			add(parts[i] + "-" + parts[i+1])
		}
	}
	return queries
}
