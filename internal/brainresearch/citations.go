package brainresearch

import (
	"regexp"
	"strings"
)

var answerSourceKeyPattern = regexp.MustCompile(`(?i)\b(?:src|x|apple-note|feed-entry|gh-star|github_star|yt|youtube|safari-tab|manual|item):[A-Za-z0-9][A-Za-z0-9._~:/@?&=+%#-]*`)

// AnswerSourceKeys returns the distinct exact source keys used in synthesized
// answer text, in first-use order.
func AnswerSourceKeys(answer string) []string {
	matches := answerSourceKeyPattern.FindAllString(answer, -1)
	seen := map[string]struct{}{}
	keys := make([]string, 0, len(matches))
	for _, match := range matches {
		key := strings.TrimSpace(match)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

// CitationsUsedInAnswer narrows available synthesis-context metadata to the
// sources the final answer actually cites. PreparedSynthesis.Citations remains
// the record of all evidence made available to the model.
func CitationsUsedInAnswer(answer string, available []Citation) []Citation {
	used := make(map[string]struct{})
	for _, key := range AnswerSourceKeys(answer) {
		used[key] = struct{}{}
	}
	out := make([]Citation, 0, len(used))
	for _, citation := range available {
		if _, ok := used[strings.TrimSpace(citation.SourceKey)]; ok {
			out = append(out, citation)
		}
	}
	return out
}
