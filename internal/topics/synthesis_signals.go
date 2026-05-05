package topics

import (
	"sort"
	"strings"
	"unicode"
)

func buildSignalClusters(graph TopicMap, evidence []topicEvidence) []topicSignalCluster {
	type accumulator struct {
		sourceKeys map[string]struct{}
		titles     map[string]struct{}
	}

	accumulators := map[string]*accumulator{}
	for _, entry := range evidence {
		if len(entry.Phrases) == 0 {
			continue
		}
		seen := map[string]struct{}{}
		for _, phrase := range entry.Phrases {
			if _, exists := seen[phrase]; exists {
				continue
			}
			seen[phrase] = struct{}{}

			acc := accumulators[phrase]
			if acc == nil {
				acc = &accumulator{
					sourceKeys: map[string]struct{}{},
					titles:     map[string]struct{}{},
				}
				accumulators[phrase] = acc
			}
			acc.sourceKeys[entry.Node.SourceKey] = struct{}{}
			if title := strings.TrimSpace(entry.Title); title != "" {
				acc.titles[title] = struct{}{}
			}
		}
	}

	clusters := make([]topicSignalCluster, 0, len(accumulators))
	for phrase, acc := range accumulators {
		if len(acc.sourceKeys) <= 1 {
			continue
		}
		clusters = append(clusters, topicSignalCluster{
			Phrase:     phrase,
			SourceKeys: sortedKeys(acc.sourceKeys),
			Titles:     sortedKeys(acc.titles),
			Count:      len(acc.sourceKeys),
		})
	}

	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].Count != clusters[j].Count {
			return clusters[i].Count > clusters[j].Count
		}
		if phraseWordCount(clusters[i].Phrase) != phraseWordCount(clusters[j].Phrase) {
			return phraseWordCount(clusters[i].Phrase) > phraseWordCount(clusters[j].Phrase)
		}
		return clusters[i].Phrase < clusters[j].Phrase
	})

	selected := make([]topicSignalCluster, 0, min(6, len(clusters)))
	for _, cluster := range clusters {
		if overlapsSelectedPhrase(selected, cluster.Phrase, cluster.Count) {
			continue
		}
		selected = append(selected, cluster)
		if len(selected) >= 6 {
			break
		}
	}
	return selected
}

func summarizeSignalClusters(clusters []topicSignalCluster, limit int) string {
	if len(clusters) == 0 {
		return ""
	}
	labels := make([]string, 0, min(limit, len(clusters)))
	for _, cluster := range clusters {
		labels = append(labels, cluster.Phrase)
		if limit > 0 && len(labels) >= limit {
			break
		}
	}
	return joinLabels(labels)
}

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

var baseSignalStopwords = map[string]struct{}{
	"a": {}, "about": {}, "after": {}, "all": {}, "also": {}, "an": {}, "and": {}, "any": {},
	"are": {}, "around": {}, "because": {}, "been": {}, "between": {}, "bookmark": {}, "bookmarks": {},
	"build": {}, "building": {}, "built": {}, "can": {}, "could": {}, "data": {}, "does": {}, "enough": {},
	"co": {}, "com": {}, "even": {}, "for": {}, "from": {}, "gets": {}, "getting": {}, "github": {}, "have": {}, "here": {},
	"how": {}, "http": {}, "https": {}, "into": {}, "just": {}, "like": {}, "links": {}, "linked": {}, "look": {}, "looking": {},
	"looks": {}, "made": {}, "make": {}, "makes": {}, "many": {}, "more": {}, "most": {}, "much": {},
	"note": {}, "notes": {}, "onto": {}, "other": {}, "over": {}, "pic": {}, "post": {}, "posts": {}, "repo": {},
	"repos": {}, "saved": {}, "save": {}, "saving": {}, "show": {}, "showing": {}, "shows": {}, "source": {},
	"sources": {}, "status": {}, "such": {}, "t": {}, "than": {}, "that": {}, "their": {}, "them": {}, "then": {}, "there": {},
	"these": {}, "they": {}, "this": {}, "those": {}, "through": {}, "tweet": {}, "tweets": {}, "using": {},
	"used": {}, "user": {}, "users": {}, "video": {}, "videos": {}, "what": {}, "when": {}, "where": {},
	"which": {}, "while": {}, "who": {}, "why": {}, "will": {}, "with": {}, "without": {}, "work": {},
	"worked": {}, "working": {}, "works": {}, "www": {}, "x": {}, "your": {},
}

var genericSingleSignalStopwords = map[string]struct{}{
	"article": {}, "articles": {}, "headline": {}, "headlines": {}, "latest": {}, "local": {},
	"national": {}, "news": {}, "opinion": {}, "read": {}, "reading": {}, "story": {},
	"stories": {}, "subscribe": {}, "subscription": {}, "today": {},
}

func shortTechToken(token string) bool {
	switch token {
	case "ai", "api", "cli", "cpu", "gpu", "json", "llm", "mcp", "orm", "pdf", "rag", "sdk", "sql", "ui", "ux":
		return true
	default:
		return false
	}
}

func overlapsSelectedPhrase(selected []topicSignalCluster, phrase string, count int) bool {
	for _, current := range selected {
		if current.Count < count {
			continue
		}
		if phraseContains(current.Phrase, phrase) {
			return true
		}
	}
	return false
}

func phraseContains(container string, candidate string) bool {
	if container == candidate {
		return true
	}
	return strings.Contains(" "+container+" ", " "+candidate+" ")
}

func phraseWordCount(value string) int {
	return len(strings.Fields(strings.TrimSpace(value)))
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
