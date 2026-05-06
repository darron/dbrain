package topics

import (
	"strings"
	"unicode"
)

func extractSignalPhrases(topic string, values ...string) []string {
	stopwords := topicSignalStopwords(topic)
	seen := map[string]struct{}{}
	out := make([]string, 0, 12)
	for _, value := range values {
		tokens := signalTokens(value)
		maxN := min(3, len(tokens))
		for size := maxN; size >= 1; size-- {
			for i := 0; i+size <= len(tokens); i++ {
				candidate := tokens[i : i+size]
				if !validSignalPhrase(candidate, stopwords) {
					continue
				}
				phrase := strings.Join(candidate, " ")
				if _, exists := seen[phrase]; exists {
					continue
				}
				seen[phrase] = struct{}{}
				out = append(out, phrase)
			}
		}
	}
	return out
}

func signalTokens(value string) []string {
	return strings.Fields(strings.ToLower(strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r), r == ' ':
			return r
		default:
			return ' '
		}
	}, value)))
}

func validSignalPhrase(tokens []string, stopwords map[string]struct{}) bool {
	if len(tokens) == 0 {
		return false
	}
	for _, token := range tokens {
		if token == "" {
			return false
		}
		if len([]rune(token)) < 2 && !shortTechToken(token) {
			return false
		}
		if _, skip := stopwords[token]; skip {
			return false
		}
	}
	if len(tokens) == 1 {
		token := tokens[0]
		if _, skip := genericSingleSignalStopwords[token]; skip {
			return false
		}
		if len(token) >= 5 || shortTechToken(token) {
			return true
		}
		return false
	}
	hasMeaningfulToken := false
	for _, token := range tokens {
		if len(token) >= 4 || shortTechToken(token) {
			hasMeaningfulToken = true
			break
		}
	}
	return hasMeaningfulToken
}

func topicSignalStopwords(topic string) map[string]struct{} {
	stopwords := make(map[string]struct{}, len(baseSignalStopwords)+8)
	for word := range baseSignalStopwords {
		stopwords[word] = struct{}{}
	}
	for _, token := range signalTokens(topic) {
		if token == "" {
			continue
		}
		for _, variant := range topicTokenVariants(token) {
			stopwords[variant] = struct{}{}
		}
	}
	return stopwords
}

func topicTokenVariants(token string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	add := func(value string) {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}

	add(token)
	if strings.HasSuffix(token, "ies") && len(token) > 3 {
		add(strings.TrimSuffix(token, "ies") + "y")
	}
	if strings.HasSuffix(token, "y") && len(token) > 2 {
		add(strings.TrimSuffix(token, "y") + "ies")
	}
	if strings.HasSuffix(token, "s") && len(token) > 3 {
		add(strings.TrimSuffix(token, "s"))
	} else if len(token) > 2 {
		add(token + "s")
	}
	return out
}

func shortTechToken(token string) bool {
	switch token {
	case "ai", "api", "cli", "cpu", "gpu", "json", "llm", "mcp", "orm", "pdf", "rag", "sdk", "sql", "ui", "ux":
		return true
	default:
		return false
	}
}

func formatSignalTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
