package topics

import (
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/entities"
)

func collectTopicEntities(graph TopicMap, index []entities.Entity, topic string, limit int) []TopicEntity {
	if len(graph.Nodes) == 0 || len(index) == 0 {
		return nil
	}

	nodeKeys := map[string]struct{}{}
	for _, node := range graph.Nodes {
		nodeKeys[node.SourceKey] = struct{}{}
	}
	topicTerms := topicQueryTerms(topic)

	type scoredTopicEntity struct {
		Entity       TopicEntity
		SortScore    int
		KindPriority int
	}

	matched := make([]scoredTopicEntity, 0, len(index))
	for _, entity := range index {
		sourceKeys := make([]string, 0, 4)
		seen := map[string]struct{}{}
		for _, ref := range entity.References {
			if _, ok := nodeKeys[ref.SourceKey]; !ok {
				continue
			}
			if _, exists := seen[ref.SourceKey]; exists {
				continue
			}
			seen[ref.SourceKey] = struct{}{}
			sourceKeys = append(sourceKeys, ref.SourceKey)
		}
		if len(sourceKeys) == 0 {
			continue
		}
		score := scoreTopicEntity(entity, topic, topicTerms, len(sourceKeys))
		if score <= 0 {
			continue
		}
		matched = append(matched, scoredTopicEntity{
			Entity: TopicEntity{
				Key:               entity.Key,
				Name:              entity.Name,
				Kind:              string(entity.Kind),
				NotePath:          entity.NotePath,
				CanonicalURL:      entity.CanonicalURL,
				ReferenceCount:    entity.ReferenceCount,
				MatchedReferences: len(sourceKeys),
				MatchedSourceKeys: sourceKeys,
			},
			SortScore:    score,
			KindPriority: topicEntityKindPriority(entity.Kind),
		})
	}

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].SortScore != matched[j].SortScore {
			return matched[i].SortScore > matched[j].SortScore
		}
		if matched[i].Entity.MatchedReferences != matched[j].Entity.MatchedReferences {
			return matched[i].Entity.MatchedReferences > matched[j].Entity.MatchedReferences
		}
		if matched[i].KindPriority != matched[j].KindPriority {
			return matched[i].KindPriority > matched[j].KindPriority
		}
		if matched[i].Entity.ReferenceCount != matched[j].Entity.ReferenceCount {
			return matched[i].Entity.ReferenceCount > matched[j].Entity.ReferenceCount
		}
		if matched[i].Entity.Kind != matched[j].Entity.Kind {
			return matched[i].Entity.Kind < matched[j].Entity.Kind
		}
		return strings.ToLower(matched[i].Entity.Name) < strings.ToLower(matched[j].Entity.Name)
	})

	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	result := make([]TopicEntity, 0, len(matched))
	for _, entry := range matched {
		result = append(result, entry.Entity)
	}
	return result
}

func buildTopicPivots(graph TopicMap) TopicPivots {
	pivots := TopicPivots{
		Projects:     make([]TopicEntity, 0, 3),
		Orgs:         make([]TopicEntity, 0, 3),
		Sites:        make([]TopicEntity, 0, 3),
		People:       make([]TopicEntity, 0, 3),
		SeedNodes:    make([]TopicMapNode, 0, len(graph.Nodes)),
		RelatedNodes: make([]TopicMapNode, 0, len(graph.Nodes)),
	}

	for _, entity := range graph.Entities {
		switch entity.Kind {
		case "project":
			if len(pivots.Projects) < 3 {
				pivots.Projects = append(pivots.Projects, entity)
			}
		case "org":
			if len(pivots.Orgs) < 3 {
				pivots.Orgs = append(pivots.Orgs, entity)
			}
		case "site":
			if len(pivots.Sites) < 3 {
				pivots.Sites = append(pivots.Sites, entity)
			}
		case "person":
			if len(pivots.People) < 3 {
				pivots.People = append(pivots.People, entity)
			}
		}
	}

	for _, node := range graph.Nodes {
		switch node.Role {
		case "seed":
			if len(pivots.SeedNodes) < 5 {
				pivots.SeedNodes = append(pivots.SeedNodes, node)
			}
		case "related":
			if len(pivots.RelatedNodes) < 5 {
				pivots.RelatedNodes = append(pivots.RelatedNodes, node)
			}
		}
	}

	return pivots
}

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
