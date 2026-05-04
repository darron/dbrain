package queryterms

import (
	"strings"
	"unicode"
)

// Terms normalizes a natural-language question into stable retrieval terms.
func Terms(question string) []string {
	stopwords := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "are": {}, "can": {}, "did": {}, "do": {}, "does": {},
		"about": {}, "as": {}, "at": {}, "be": {}, "been": {}, "being": {}, "brain": {}, "by": {}, "context": {}, "current": {}, "data": {}, "dbrain": {}, "evidence": {}, "expansion": {}, "find": {}, "for": {}, "from": {}, "github": {}, "have": {}, "her": {}, "him": {}, "his": {}, "how": {}, "i": {}, "if": {}, "in": {}, "include": {}, "information": {}, "into": {}, "is": {}, "it": {}, "its": {}, "key": {}, "keys": {}, "know": {}, "local": {}, "look": {}, "looks": {}, "me": {}, "metadata": {}, "my": {}, "of": {}, "on": {}, "or": {}, "present": {}, "prior": {}, "query": {}, "question": {}, "questions": {}, "recent": {}, "related": {}, "relevant": {}, "saved": {}, "stories": {}, "story": {}, "that": {}, "the": {}, "their": {}, "them": {}, "there": {}, "these": {}, "this": {}, "those": {},
		"repo": {}, "repos": {}, "repository": {}, "repositories": {},
		"show": {}, "source": {}, "sources": {}, "src": {}, "tag": {}, "tags": {}, "tell": {}, "tweet": {}, "tweets": {},
		"to": {}, "use": {}, "user": {}, "using": {}, "was": {}, "we": {}, "were": {}, "what": {}, "when": {}, "where": {}, "which": {}, "who": {}, "why": {}, "with": {}, "x": {}, "you": {}, "your": {},
	}

	parts := strings.Fields(normalizeQuestionText(question))
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

func canonicalTerm(term string) string {
	switch term {
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
