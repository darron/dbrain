package topics

import (
	"strings"

	"github.com/darron/dbrain/internal/entities"
)

func scoreTopicEntity(entity entities.Entity, topic string, topicTerms []string, matchedReferences int) int {
	score := matchedReferences * 20
	score += min(entity.ReferenceCount, 10)
	score += topicEntityKindPriority(entity.Kind)

	textScore := scoreTopicEntityText(entity, topic, topicTerms)
	score += textScore

	if entity.Kind == entities.KindPerson && matchedReferences == 1 && textScore == 0 {
		score -= 8
	}
	if entity.Kind == entities.KindSite && textScore == 0 {
		score -= 2
	}
	return score
}

func scoreTopicEntityText(entity entities.Entity, topic string, topicTerms []string) int {
	candidates := []string{
		strings.ToLower(strings.TrimSpace(entity.Name)),
		strings.ToLower(strings.TrimSpace(entity.Domain)),
		strings.ToLower(strings.TrimSpace(topicEntityKeyValue(entity.Key))),
	}
	for _, alias := range entity.Aliases {
		candidates = append(candidates, strings.ToLower(strings.TrimSpace(alias)))
	}

	topic = strings.ToLower(strings.TrimSpace(topic))
	score := 0
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if topic != "" {
			switch {
			case candidate == topic:
				score = max(score, 16)
			case strings.Contains(candidate, topic):
				score = max(score, 12)
			}
		}
		for _, term := range topicTerms {
			if term == "" {
				continue
			}
			switch {
			case candidate == term:
				score = max(score, 8)
			case len([]rune(term)) >= 5 && strings.Contains(candidate, term):
				score = max(score, 4)
			}
		}
	}
	return score
}

func topicEntityKindPriority(kind entities.Kind) int {
	switch kind {
	case entities.KindProject:
		return 6
	case entities.KindOrg:
		return 5
	case entities.KindSite:
		return 4
	case entities.KindPerson:
		return 2
	default:
		return 1
	}
}

func topicQueryTerms(topic string) []string {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(topic)))
	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || len([]rune(part)) < 3 {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func topicEntityKeyValue(key string) string {
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
