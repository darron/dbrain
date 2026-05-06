package app

import (
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func categorizeAnalyzeTokenCounts(items []model.Item, sources []model.SourceDocument) map[string]int {
	counts := make(map[string]int)
	for _, item := range items {
		countCategorizeAnalyzeTokens(counts, item.UserTags)
	}
	for _, source := range sources {
		countCategorizeAnalyzeTokens(counts, source.UserTags)
	}
	return counts
}

func countCategorizeAnalyzeTokens(counts map[string]int, userTags string) {
	for _, token := range strings.Split(userTags, ",") {
		token = strings.TrimSpace(token)
		if token != "" {
			counts[token]++
		}
	}
}
