package topics

import (
	"sort"
	"strings"
)

func bestSentence(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")
	value = strings.Join(strings.Fields(replacer.Replace(value)), " ")
	if value == "" {
		return ""
	}
	for _, delimiter := range []string{". ", "? ", "! "} {
		if idx := strings.Index(value, delimiter); idx > 0 {
			return strings.TrimSpace(value[:idx+1])
		}
	}
	return trimRunes(value, 220)
}

func looksQuestionLike(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return false
	}
	if strings.Contains(value, "?") {
		return true
	}
	for _, prefix := range []string{"how ", "what ", "why ", "which ", "who ", "when ", "where ", "should ", "can "} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func describeSourceMix(nodes []TopicMapNode) string {
	counts := map[string]int{}
	for _, node := range nodes {
		if node.SourceType == "" {
			continue
		}
		counts[sourceTypeFamily(strings.ToLower(strings.TrimSpace(node.SourceType)))]++
	}
	type bucket struct {
		Key   string
		Count int
	}
	ordered := make([]bucket, 0, len(counts))
	for key, count := range counts {
		ordered = append(ordered, bucket{Key: key, Count: count})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Count != ordered[j].Count {
			return ordered[i].Count > ordered[j].Count
		}
		return ordered[i].Key < ordered[j].Key
	})
	labels := make([]string, 0, len(ordered))
	for _, entry := range ordered {
		switch entry.Key {
		case "x":
			labels = append(labels, "saved X posts")
		case "web":
			labels = append(labels, "linked web sources")
		case "github":
			labels = append(labels, "GitHub repos")
		case "youtube":
			labels = append(labels, "YouTube sources")
		default:
			labels = append(labels, entry.Key+" sources")
		}
	}
	return joinLabels(labels)
}

func trimRunes(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= max {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(string(runes[:max])) + "..."
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}
