package ask

import (
	"strings"

	"github.com/darron/dbrain/internal/entities"
)

func entityMatchesResearchQuery(entity entities.Entity, joined string, terms []string) bool {
	joined = strings.ToLower(strings.TrimSpace(joined))
	if len(terms) <= 1 || joined == "" {
		return true
	}

	candidates := entitySearchCandidates(entity)
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(candidate, joined) {
			return true
		}
	}

	matched := 0
	seen := map[string]struct{}{}
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" || len([]rune(term)) < 3 {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		for _, candidate := range candidates {
			if strings.Contains(candidate, term) {
				seen[term] = struct{}{}
				matched++
				break
			}
		}
		if matched >= 2 {
			return true
		}
	}
	return false
}

func scoreEntityForQuery(entity entities.Entity, query string) int {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || len([]rune(query)) < 3 {
		return 0
	}

	score := 0
	for _, candidate := range entitySearchCandidates(entity) {
		if candidate == "" {
			continue
		}
		switch {
		case candidate == query:
			score = maxInt(score, 10)
		case strings.Contains(candidate, query) && len([]rune(query)) >= 5:
			score = maxInt(score, 6)
		case strings.Contains(candidate, query):
			score = maxInt(score, 4)
		}
	}
	return score
}

func entitySearchCandidates(entity entities.Entity) []string {
	candidates := []string{
		strings.ToLower(strings.TrimSpace(entity.Name)),
		strings.ToLower(strings.TrimSpace(entity.Domain)),
		strings.ToLower(strings.TrimSpace(entityKeySearchValue(entity.Key))),
	}
	for _, alias := range entity.Aliases {
		candidates = append(candidates, strings.ToLower(strings.TrimSpace(alias)))
	}
	return candidates
}

func entityKeySearchValue(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	switch {
	case strings.HasPrefix(key, "github-repo:"):
		return strings.TrimPrefix(key, "github-repo:")
	case strings.HasPrefix(key, "github-owner:"):
		return strings.TrimPrefix(key, "github-owner:")
	case strings.HasPrefix(key, "x-author:name:"):
		return strings.TrimPrefix(key, "x-author:name:")
	case strings.HasPrefix(key, "x-author:"):
		return strings.TrimPrefix(key, "x-author:")
	case strings.HasPrefix(key, "site:"):
		return strings.TrimPrefix(key, "site:")
	default:
		if idx := strings.IndexByte(key, ':'); idx >= 0 && idx+1 < len(key) {
			return key[idx+1:]
		}
		return key
	}
}
